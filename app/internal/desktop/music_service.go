package desktop

import (
	"context"
	"encoding/json"
			"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
		"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

const musicRequestQueueCapacity = 100
const searchPlayMarker = "__my_livehub_search_play__"

type MusicSearchResponse struct {
	Tracks []music.Track `json:"tracks"`
	Errors map[string]string `json:"errors"`
	Page int `json:"page"`
	PageSize int `json:"page_size"`
	Total int64 `json:"total"`
	MaxPage int `json:"max_page"`
}

type MusicStatus struct {
	Enabled bool `json:"enabled"`
	Running bool `json:"running"`
	PendingRequests int `json:"pending_requests"`
	DroppedRequests uint64 `json:"dropped_requests"`
	LastError string `json:"last_error,omitempty"`
}

type MusicService struct {
	manager *config.Manager
	buffer *events.Buffer
	mu sync.RWMutex
	lifecycleMu sync.Mutex
	value config.MusicConfig
	sources *music.SourceRegistry
	engine music.PlaybackEngine
	queue *music.Queue
	history []music.QueueItem

	started bool
	ctx context.Context
	cancel context.CancelFunc
	unsubscribe func()
	workerDone chan struct{}
	requests chan events.Event
	requestDropped atomic.Uint64
	lastError string

	watchCancel context.CancelFunc
	playGeneration uint64
	searcher *music.LXSearchClient
	lyricsMu sync.Mutex
	lyricsCache map[string]string
	fanMedalChecker interface { Check(context.Context, int, int) (bool, int, error) }
}

func NewMusicService(manager *config.Manager, value config.MusicConfig, buffers ...*events.Buffer) *MusicService {
	sources := music.NewSourceRegistry()
	return NewMusicServiceWithDependencies(manager, value, sources, NewLXPlaybackEngine(value.LXAPIEndpoints), buffers...)
}

// NewMusicServiceWithFanMedalChecker wires the medal checker as an internal dependency.
func NewMusicServiceWithFanMedalChecker(manager *config.Manager, value config.MusicConfig, checker interface { Check(context.Context, int, int) (bool, int, error) }, buffers ...*events.Buffer) *MusicService {
	service := NewMusicService(manager, value, buffers...)
	service.fanMedalChecker = checker
	return service
}

func NewMusicServiceWithDependencies(manager *config.Manager, value config.MusicConfig, sources *music.SourceRegistry, engine music.PlaybackEngine, buffers ...*events.Buffer) *MusicService {
	if sources == nil { sources = music.NewSourceRegistry() }
	if engine == nil { engine = NewLXPlaybackEngine(value.LXAPIEndpoints) }
	var buffer *events.Buffer
	if len(buffers) > 0 { buffer = buffers[0] }
	return &MusicService{
		manager: manager, buffer: buffer, value: cloneMusicConfig(value),
		sources: sources, engine: engine, queue: music.NewQueue(value.QueueCapacity),
		searcher: music.NewLXSearchClient(nil), lyricsCache: make(map[string]string),
	}
}

func (s *MusicService) start(ctx context.Context) error {
	if s == nil || ctx == nil { return errors.New("music service and startup context are required") }
	if s.buffer == nil { return errors.New("music event buffer is required") }
	if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok { if err := lifecycle.Start(ctx); err != nil { return fmt.Errorf("启动音乐播放服务失败：%w", err) } }
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return errors.New("music service has already started")
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.requests = make(chan events.Event, musicRequestQueueCapacity)
	s.workerDone = make(chan struct{})
	s.started = true
	s.unsubscribe = s.buffer.Subscribe(s.handleEvent)
	s.mu.Unlock()
	go s.worker()
	return nil
}

func (s *MusicService) stop(ctx context.Context) error {
	if s == nil { return nil }
	if ctx == nil { return errors.New("music shutdown context is required") }
	s.lifecycleMu.Lock()
	s.mu.Lock()
	if !s.started {
		s.mu.Unlock()
		s.lifecycleMu.Unlock()
		s.cancelPlaybackWatcher()
		return s.Shutdown()
	}
	cancel, unsubscribe, done := s.cancel, s.unsubscribe, s.workerDone
	s.started, s.unsubscribe = false, nil
	s.mu.Unlock()
	if unsubscribe != nil { unsubscribe() }
	if cancel != nil { cancel() }
	s.cancelPlaybackWatcher()
	s.lifecycleMu.Unlock()

	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	s.queue.Clear()
	s.queue.FinishCurrent()
	return s.Shutdown()
}

func (s *MusicService) handleEvent(event events.Event) {
	if event.Type != "danmaku" { return }
	s.mu.RLock()
	enabled := s.value.Enabled && (s.value.CommandEnabled || s.value.SkipCommandEnabled) && s.started
	requests := s.requests
	prefix := s.value.CommandPrefix
	skipEnabled := s.value.SkipCommandEnabled
	skipCommand := strings.TrimSpace(s.value.SkipCommand)
	s.mu.RUnlock()
	if !enabled || requests == nil { return }
	if _, ok := parseSongRequest(event.Content, prefix); !ok && !(skipEnabled && isSkipCommand(event.Content, skipCommand)) { return }
	select {
	case requests <- event:
	default:
		s.requestDropped.Add(1)
	}
}

func (s *MusicService) worker() {
	s.mu.RLock()
	ctx, done, requests := s.ctx, s.workerDone, s.requests
	s.mu.RUnlock()
	defer close(done)
	for {
		select {
		case <-ctx.Done():
			return
		case event := <-requests:
			s.processSongRequest(ctx, event)
		}
	}
}

func (s *MusicService) setFanMedalChecker(checker interface { Check(context.Context, int, int) (bool, int, error) }) {
	if s == nil { return }
	s.mu.Lock()
	s.fanMedalChecker = checker
	s.mu.Unlock()
}

func (s *MusicService) processSongRequest(ctx context.Context, event events.Event) {
	s.mu.RLock()
	enabled := s.value.Enabled && s.started
	commandEnabled := s.value.CommandEnabled
	prefix := s.value.CommandPrefix
	skipEnabled := s.value.SkipCommandEnabled
	skipCommand := strings.TrimSpace(s.value.SkipCommand)
	requireFanMedal := s.value.RequireFanMedal
	fanMedalLevel := s.value.FanMedalLevel
	checker := s.fanMedalChecker
	s.mu.RUnlock()
	if !enabled { return }
	if skipEnabled && isSkipCommand(event.Content, skipCommand) {
		if !canSkipMusic(event) { return }
		if err := s.Next(); err != nil { s.setLastError(fmt.Errorf("执行弹幕切歌失败：%w", err)) }
		return
	}
	if !commandEnabled { return }
	keyword, ok := parseSongRequest(event.Content, prefix)
	if !ok { return }
	if requireFanMedal && !event.IsRoomOwner && !event.IsModerator {
		if checker == nil { s.setLastError(errors.New("音乐点播粉丝勋章校验服务不可用，已拒绝本次点播")); return }
		allowed, level, checkErr := checker.Check(ctx, event.UserID, fanMedalLevel)
		if checkErr != nil { s.setLastError(fmt.Errorf("校验观众 %s 的主播粉丝勋章失败，已拒绝本次点播：%w", event.UserName, checkErr)); return }
		if !allowed { s.setLastError(fmt.Errorf("观众 %s 的主播粉丝勋章等级不足（当前 %d，要求 %d）", event.UserName, level, fanMedalLevel)); return }
	}
	result, err := s.search(ctx, keyword)
	if err != nil {
		s.setLastError(fmt.Errorf("搜索弹幕点歌失败：%w", err))
		return
	}
	if len(result.Tracks) == 0 {
		s.setLastError(fmt.Errorf("观众 %s 点播的歌曲未找到：%s", event.UserName, keyword))
		return
	}
	if _, err := s.Enqueue(result.Tracks[0], event.UserName); err != nil {
		s.setLastError(fmt.Errorf("加入观众点播队列失败：%w", err))
	}
}

func isSkipCommand(content, command string) bool { return command != "" && strings.TrimSpace(content) == command }
func canSkipMusic(event events.Event) bool { return event.IsRoomOwner || event.IsModerator }

func parseSongRequest(content, prefix string) (string, bool) {
	content = strings.TrimSpace(content)
	prefix = strings.TrimSpace(prefix)
	if content == "" || prefix == "" || !strings.HasPrefix(content, prefix) { return "", false }
	remainder := strings.TrimPrefix(content, prefix)
	if remainder != "" {
		next, _ := utf8.DecodeRuneInString(remainder)
		if !unicode.IsSpace(next) { return "", false }
	}
	keyword := strings.TrimSpace(remainder)
	return keyword, keyword != ""
}

func (s *MusicService) GetConfig() config.MusicConfig {
	if s == nil { return config.MusicConfig{} }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMusicConfig(s.value)
}

func (s *MusicService) SaveConfig(value config.MusicConfig) error {
	if s == nil || s.manager == nil { return errors.New("music configuration service is unavailable") }
	if err := value.Validate(); err != nil { return err }
	oldCapacity := s.queue.Snapshot().Capacity
	if err := s.queue.Resize(value.QueueCapacity); err != nil { return err }
	if err := config.SaveTyped(s.manager, "music.json", value, func(candidate config.MusicConfig) error { return candidate.Validate() }); err != nil {
		_ = s.queue.Resize(oldCapacity)
		return err
	}
	s.mu.Lock()
	wasEnabled := s.value.Enabled
	s.value = cloneMusicConfig(value)
	s.mu.Unlock()
	if player, ok := s.engine.(*LXPlaybackEngine); ok { player.SetEndpoints(value.LXAPIEndpoints) }
	if wasEnabled && !value.Enabled {
		s.cancelPlaybackWatcher()
		_ = s.engine.Stop(context.Background())
		s.queue.Clear()
		s.queue.FinishCurrent()
		s.mu.Lock()
		s.history = nil
		s.mu.Unlock()
	}
	return nil
}

func (s *MusicService) GetSources() []music.SourceSnapshot {
	if s == nil || s.searcher == nil { return []music.SourceSnapshot{} }
	priority := s.activeLXSearchSourceIDs()
	rank := make(map[string]int, len(priority))
	for i, id := range priority { rank[id] = i }
	result := s.searcher.Sources()
	for i := range result {
		result[i].Enabled = false
		for _, id := range priority { if id == result[i].ID { result[i].Enabled = true; break } }
	}
	sort.SliceStable(result, func(i, j int) bool {
		ri, iok := rank[result[i].ID]; rj, jok := rank[result[j].ID]
		if iok != jok { return iok }
		if iok { return ri < rj }
		return result[i].ID < result[j].ID
	})
	return result
}

func (s *MusicService) Search(keyword string) (MusicSearchResponse, error) {
	return s.SearchPage(keyword, 1)
}

func (s *MusicService) SearchPage(keyword string, page int) (MusicSearchResponse, error) {
	return s.searchPage(context.Background(), keyword, page)
}

func (s *MusicService) SearchSourcePage(keyword, source string, page int) (MusicSearchResponse, error) {
	if s == nil || s.searcher == nil {
		return MusicSearchResponse{}, errors.New("洛雪搜索 SDK 未初始化")
	}
	if strings.TrimSpace(keyword) == "" {
		return MusicSearchResponse{}, errors.New("请输入歌曲名称或歌手")
	}
	if page < 1 {
		return MusicSearchResponse{}, errors.New("搜索页码必须大于 0")
	}
	declaredSource := false
	for _, id := range s.activeLXSearchSourceIDs() {
		if id == source { declaredSource = true; break }
	}
	if !declaredSource {
		return MusicSearchResponse{}, fmt.Errorf("当前未启用 %s 搜索来源，或该来源暂不支持搜索", source)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	result, err := s.searcher.SearchProvider(ctx, source, keyword, page)
	if err != nil {
		return MusicSearchResponse{}, err
	}
	return MusicSearchResponse{
		Tracks: result.Tracks, Page: result.Page, PageSize: result.PageSize,
		Total: result.Total, MaxPage: result.MaxPage, Errors: map[string]string{},
	}, nil
}

// activeLXSearchSourceIDs reads the independent platform selection from music.json.
func (s *MusicService) activeLXSearchSourceIDs() []string {
	if s == nil || s.searcher == nil { return []string{} }
	s.mu.RLock()
	configured := append([]string(nil), s.value.EnabledSearchSources...)
	s.mu.RUnlock()
	if configured == nil {
		for _, item := range s.searcher.Sources() { configured = append(configured, item.ID) }
	}
	available := make(map[string]bool)
	for _, item := range s.searcher.Sources() { available[item.ID] = true }
	result := make([]string, 0, len(configured))
	seen := make(map[string]bool)
	for _, id := range configured {
		if available[id] && !seen[id] { result = append(result, id); seen[id] = true }
	}
	return result
}

func containsLXAction(actions []string, target string) bool {
	for _, action := range actions { if action == target { return true } }
	return false
}

func (s *MusicService) search(parent context.Context, keyword string) (MusicSearchResponse, error) {
	return s.searchPage(parent, keyword, 1)
}

func (s *MusicService) searchPage(parent context.Context, keyword string, page int) (MusicSearchResponse, error) {
	if s == nil || s.searcher == nil { return MusicSearchResponse{}, errors.New("洛雪搜索 SDK 未初始化") }
	if parent == nil { parent = context.Background() }
	sourceIDs := s.activeLXSearchSourceIDs()
	if len(sourceIDs) == 0 {
		return MusicSearchResponse{Tracks: []music.Track{}, Errors: map[string]string{}, Page: page, PageSize: 30, MaxPage: 1}, nil
	}
	ctx, cancel := context.WithTimeout(parent, 18*time.Second)
	defer cancel()
	result, sourceErrors, err := s.searcher.SearchSources(ctx, keyword, page, sourceIDs)
	response := MusicSearchResponse{
		Tracks: result.Tracks, Errors: sourceErrors, Page: result.Page,
		PageSize: result.PageSize, Total: result.Total, MaxPage: result.MaxPage,
	}
	if response.Tracks == nil { response.Tracks = []music.Track{} }
	return response, err
}


func (s *MusicService) Enqueue(track music.Track, requestedBy string) (music.QueueItem, error) {
	if s == nil || s.queue == nil { return music.QueueItem{}, errors.New("music queue is unavailable") }
	s.mu.RLock(); enabled := s.value.Enabled; s.mu.RUnlock()
	if !enabled { return music.QueueItem{}, errors.New("music module is disabled") }
	startImmediately := s.queue.Snapshot().Current == nil
	item, err := s.queue.Enqueue(track, requestedBy)
	if err != nil { return music.QueueItem{}, err }
	if startImmediately {
		if err := s.PlayNext(); err != nil {
			if failed := s.queue.Snapshot().Current; failed != nil && failed.ID == item.ID {
				s.queue.FinishCurrent()
				_ = s.queue.EnqueueFront(*failed)
			}
			return item, err
		}
	}
	return item, nil
}

func (s *MusicService) PlayTrackNow(track music.Track) error {
	if s == nil || s.queue == nil || s.engine == nil { return errors.New("music playback service is unavailable") }
	s.mu.RLock(); enabled := s.value.Enabled; s.mu.RUnlock()
	if !enabled { return errors.New("music module is disabled") }
	current := s.queue.Snapshot().Current
	if current != nil { s.pushHistory(*current) }
	s.cancelPlaybackWatcher()
	if _, err := s.queue.SetCurrentTrack(track, searchPlayMarker); err != nil { return err }
	if err := s.PlayNext(); err != nil {
		s.queue.FinishCurrent()
		if current != nil {
			_ = s.queue.SetCurrent(*current)
			if restoreErr := s.PlayNext(); restoreErr != nil { s.setLastError(restoreErr) }
		}
		return err
	}
	return nil
}

func (s *MusicService) GetQueue() music.QueueSnapshot {
	if s == nil || s.queue == nil { return music.QueueSnapshot{Items:[]music.QueueItem{}} }
	return s.queue.Snapshot()
}

// RemoveQueueItem removes one pending audience request. It never interrupts current playback.
func (s *MusicService) RemoveQueueItem(id string) error {
	if s == nil || s.queue == nil { return errors.New("music queue is unavailable") }
	if !s.queue.Remove(id) { return fmt.Errorf("待播歌曲不存在或已被播放：%s", id) }
	return nil
}

// ClearQueue removes all pending audience requests and preserves the current track.
func (s *MusicService) ClearQueue() {
	if s == nil || s.queue == nil { return }
	s.queue.Clear()
}

// MoveQueueItem moves a pending audience request to a zero-based position.
func (s *MusicService) MoveQueueItem(id string, targetIndex int) error {
	if s == nil || s.queue == nil { return errors.New("music queue is unavailable") }
	return s.queue.Move(id, targetIndex)
}

func (s *MusicService) PlayNext() error { return s.playNext(context.Background()) }

func (s *MusicService) playNext(parent context.Context) error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	if parent == nil { parent = context.Background() }
	s.mu.RLock()
	enabled := s.value.Enabled
	s.mu.RUnlock()
	if !enabled { return errors.New("music module is disabled") }

	current := s.queue.Snapshot().Current
	if current == nil {
		item, ok := s.queue.TakeNext()
		if !ok { return errors.New("播放队列为空") }
		current = &item
	}
	resolvedTrack, err := s.resolveAndStartLXTrack(parent, current.Track, false)
	if err != nil {
		return err
	}
	current.Track = resolvedTrack
	if err := s.queue.SetCurrent(*current); err != nil {
		return fmt.Errorf("更新自动换源后的当前歌曲失败：%w", err)
	}
	s.startPlaybackWatcher()
	return nil
}


func (s *MusicService) Pause() error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	return s.engine.Pause(context.Background())
}
func (s *MusicService) Resume() error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	return s.engine.Resume(context.Background())
}
func (s *MusicService) Seek(positionMS int64) error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	return s.engine.Seek(context.Background(), positionMS)
}
func (s *MusicService) SetVolume(volume int) error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	if volume < 0 || volume > 100 { return fmt.Errorf("音量必须在 0 到 100 之间") }
	return s.engine.SetVolume(context.Background(), volume)
}

func (s *MusicService) Next() error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	s.cancelPlaybackWatcher()
	if current := s.queue.Snapshot().Current; current != nil { s.pushHistory(*current) }
	_ = s.engine.Stop(context.Background())
	s.queue.FinishCurrent()
	if len(s.queue.Snapshot().Items) == 0 { return nil }
	return s.PlayNext()
}

func (s *MusicService) Previous() error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	s.mu.RLock()
	if len(s.history) == 0 {
		s.mu.RUnlock()
		return errors.New("no previous track in playback history")
	}
	previous := s.history[len(s.history)-1]
	s.mu.RUnlock()

	s.cancelPlaybackWatcher()
	if current := s.queue.Snapshot().Current; current != nil {
		if err := s.queue.EnqueueFront(*current); err != nil {
			s.startPlaybackWatcher()
			return fmt.Errorf("cannot preserve current track before going back: %w", err)
		}
	}
	s.mu.Lock()
	if len(s.history) > 0 && s.history[len(s.history)-1].ID == previous.ID {
		s.history = s.history[:len(s.history)-1]
	}
	s.mu.Unlock()
	_ = s.engine.Stop(context.Background())
	if err := s.queue.SetCurrent(previous); err != nil { return err }
	return s.PlayNext()
}

func (s *MusicService) pushHistory(item music.QueueItem) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.history) > 0 && s.history[len(s.history)-1].ID == item.ID { return }
	if len(s.history) >= 100 {
		copy(s.history, s.history[1:])
		s.history = s.history[:len(s.history)-1]
	}
	s.history = append(s.history, item)
}
func (s *MusicService) Stop() error {
	if s == nil || s.engine == nil || s.queue == nil { return nil }
	s.cancelPlaybackWatcher()
	err := s.engine.Stop(context.Background())
	s.queue.FinishCurrent()
	return err
}
type musicLyricsResponse struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics string `json:"plainLyrics"`
	TrackName string `json:"trackName"`
	ArtistName string `json:"artistName"`
	Duration float64 `json:"duration"`
}

// GetLyrics retrieves time-synchronized lyrics for the current track.
// LRCLIB is used only for lyrics; audio playback remains on the selected LX source.
// Successful results are cached so the main player and desktop lyrics window
// reuse the same lyric response instead of issuing duplicate provider requests.
func (s *MusicService) GetLyrics(track music.Track) (string, error) {
	if s == nil { return "", errors.New("music lyrics service is unavailable") }
	source := track.LXSourceKey
	if source == "" { source = track.SourceID }
	cacheKey := strings.Join([]string{source, track.ID, track.Title, track.Artist}, "\x00")
	s.lyricsMu.Lock()
	if cached, ok := s.lyricsCache[cacheKey]; ok {
		s.lyricsMu.Unlock()
		return cached, nil
	}
	s.lyricsMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	var lyrics string
	var err error
	switch source {
	case music.LXSearchTX:
		lyrics, err = music.GetQQMusicLyrics(ctx, nil, track)
	case music.LXSearchKG:
		lyrics, err = music.GetKugouMusicLyrics(ctx, nil, track)
	case music.LXSearchKW:
		lyrics, err = music.GetKuwoMusicLyrics(ctx, nil, track)
	case music.LXSearchWY:
		lyrics, err = music.GetNeteaseMusicLyrics(ctx, nil, track)
	case music.LXSearchMG:
		lyrics, err = music.GetMiguMusicLyrics(ctx, nil, track)
	default:
		lyrics, err = s.getLRCLIBLyrics(track)
	}
	if err != nil { return "", err }
	s.lyricsMu.Lock()
	if s.lyricsCache == nil { s.lyricsCache = make(map[string]string) }
	if len(s.lyricsCache) >= 128 { s.lyricsCache = make(map[string]string) }
	s.lyricsCache[cacheKey] = lyrics
	s.lyricsMu.Unlock()
	return lyrics, nil
}

func (s *MusicService) getLRCLIBLyrics(track music.Track) (string, error) {
	if strings.TrimSpace(track.Title) == "" { return "", errors.New("歌曲名称不能为空") }
	ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
	defer cancel()
	client := &http.Client{Timeout: 18*time.Second}
	// Search first: exact-match requires exact duration and album values, often
	// unavailable or different in metadata returned by LX source providers.
	query := url.Values{}
	query.Set("track_name", strings.TrimSpace(track.Title))
	// Do not constrain the remote search by artist metadata: LX providers may return aliases or multiple artists.
	// The candidate scorer below prefers an exact artist match without hiding otherwise valid lyrics.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://lrclib.net/api/search?"+query.Encode(), nil)
	if err != nil { return "", err }
	req.Header.Set("User-Agent", "My-LiveHub/1.0")
	resp, err := client.Do(req)
	if err != nil { return "", fmt.Errorf("搜索歌词失败：%w", err) }
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK { return "", fmt.Errorf("歌词服务返回 HTTP %d", resp.StatusCode) }
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil { return "", fmt.Errorf("读取歌词搜索结果失败：%w", err) }
	var matches []musicLyricsResponse
	if err := json.Unmarshal(raw, &matches); err != nil { return "", fmt.Errorf("解析歌词搜索结果失败：%w", err) }
	normalize := func(value string) string { return strings.ToLower(strings.Join(strings.Fields(value), "")) }
	title, artist := normalize(track.Title), normalize(track.Artist)
	bestScore, bestLyrics := -1, ""
	for _, match := range matches {
		if strings.TrimSpace(match.SyncedLyrics) == "" { continue }
		score := 0
		if normalize(match.TrackName) == title { score += 2 }
		if artist != "" && normalize(match.ArtistName) == artist { score += 2 }
		if track.DurationMS > 0 && match.Duration > 0 && absFloat(match.Duration-float64(track.DurationMS)/1000) <= 3 { score++ }
		if score > bestScore { bestScore, bestLyrics = score, strings.TrimSpace(match.SyncedLyrics) }
	}
	return bestLyrics, nil

}
func absFloat(value float64) float64 { if value < 0 { return -value }; return value }

func (s *MusicService) GetPlaybackStatus() (music.PlaybackSnapshot, error) {
	if s == nil || s.engine == nil { return music.PlaybackSnapshot{}, music.ErrPlaybackEngineUnavailable }
	snapshot, err := s.engine.Snapshot(context.Background())
	if err != nil { return snapshot, err }
	if current := s.queue.Snapshot().Current; current != nil {
		if snapshot.Track == nil || sameLXTrackIdentity(current.Track, *snapshot.Track) {
			track := current.Track
			snapshot.Track = &track
		}
	}
	return snapshot, nil
}

func (s *MusicService) GetStatus() MusicStatus {
	if s == nil { return MusicStatus{} }
	s.mu.RLock()
	status := MusicStatus{Enabled:s.value.Enabled, Running:s.started, PendingRequests:len(s.requests), DroppedRequests:s.requestDropped.Load(), LastError:s.lastError}
	s.mu.RUnlock()
	return status
}
func (s *MusicService) Shutdown() error {
	if s == nil { return nil }
	s.cancelPlaybackWatcher()
	if s.engine == nil { return nil }
	if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok { return lifecycle.Close() }
	return nil
}

func (s *MusicService) setLastError(err error) {
	if err == nil { return }
	s.mu.Lock()
	s.lastError = err.Error()
	s.mu.Unlock()
}

func (s *MusicService) cancelPlaybackWatcher() {
	s.mu.Lock()
	if s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	s.playGeneration++
	s.mu.Unlock()
}

func (s *MusicService) startPlaybackWatcher() {
	s.mu.Lock()
	if s.watchCancel != nil { s.watchCancel() }
	ctx, cancel := context.WithCancel(context.Background())
	s.watchCancel = cancel
	s.playGeneration++
	generation := s.playGeneration
	s.mu.Unlock()
	go s.watchPlayback(ctx, generation)
}

func (s *MusicService) watchPlayback(ctx context.Context, generation uint64) {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	startedAt := time.Now()
	sawEndPosition := false
	sawPlaying := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := s.engine.Snapshot(ctx)
			if err != nil { continue }
			if snapshot.State == music.PlaybackPlaying { sawPlaying = true }
			if snapshot.DurationMS > 0 {
				threshold := snapshot.DurationMS - 1000
				if threshold < snapshot.DurationMS/2 { threshold = snapshot.DurationMS/2 }
				if snapshot.PositionMS >= threshold { sawEndPosition = true }
			}
			if snapshot.State == music.PlaybackError {
				s.mu.RLock()
				generationCurrent := s.playGeneration == generation
				s.mu.RUnlock()
				if !generationCurrent || ctx.Err() != nil {
					return
				}
				message := strings.TrimSpace(snapshot.Error)
				if message == "" { message = "mpv 加载音频失败" }
				currentItem := s.queue.Snapshot().Current
				if currentItem != nil {
					if resolvedTrack, retryErr := s.resolveAndStartLXTrack(ctx, currentItem.Track, true); retryErr == nil {
						if ctx.Err() != nil {
							return
						}
						currentItem.Track = resolvedTrack
						if updateErr := s.queue.SetCurrent(*currentItem); updateErr == nil {
							s.startPlaybackWatcher()
							return
						} else {
							message += "；更新换源结果失败：" + updateErr.Error()
						}
					} else {
						message += "；自动换源失败：" + retryErr.Error()
					}
				}
				s.setLastError(errors.New(message))
				s.queue.FinishCurrent()
				if len(s.queue.Snapshot().Items) > 0 { _ = s.PlayNext() }
				return
			}
			if snapshot.State != music.PlaybackStopped || (!sawEndPosition && !sawPlaying) { continue }
			s.mu.RLock()
			current := s.playGeneration == generation
			s.mu.RUnlock()
			if !current { return }
			if sawEndPosition {
				currentItem := s.queue.Snapshot().Current
				if currentItem != nil { s.pushHistory(*currentItem) }
				s.queue.FinishCurrent()
				if len(s.queue.Snapshot().Items) > 0 { _ = s.PlayNext() }
				return
			}
			if time.Since(startedAt) >= 20*time.Second {
				if ctx.Err() != nil { return }
				s.setLastError(errors.New("播放器未能开始播放；请检查音源解析结果和网络连接"))
				return
			}
		}
	}
}

func cloneMusicConfig(value config.MusicConfig) config.MusicConfig { return value }

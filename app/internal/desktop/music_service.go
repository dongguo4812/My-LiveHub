package desktop

import (
	"context"
			"errors"
	"fmt"
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
	notices *RequestNoticeBroker
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
	service := &MusicService{
		manager: manager, buffer: buffer, value: cloneMusicConfig(value),
		sources: sources, engine: engine, queue: music.NewQueue(value.QueueCapacity),
		searcher: music.NewLXSearchClient(nil), lyricsCache: make(map[string]string),
	}
	// Keep the lightweight command listener alive while the module is stopped,
	// matching the video module so users still receive an explicit failure notice.
	if buffer != nil { service.unsubscribe = buffer.Subscribe(service.handleEvent) }
	return service
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
	if s.unsubscribe == nil { s.unsubscribe = s.buffer.Subscribe(s.handleEvent) }
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
	cancel, done := s.cancel, s.workerDone
	// Keep the command listener subscribed while stopped so disabled-command
	// attempts can still be reported through the shared request-notice stream.
	s.started = false
	s.mu.Unlock()
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
	started := s.started
	requests := s.requests
	prefix := s.value.CommandPrefix
	skipCommand := strings.TrimSpace(s.value.SkipCommand)
	s.mu.RUnlock()
	isRequest := isSongRequestCommand(event.Content, prefix)
	isSkip := isSkipCommand(event.Content, skipCommand)
	if !isRequest && !isSkip { return }
	if !started || requests == nil {
		if isSkip {
			s.publishRequestNotice("music_skip_failed", event.UserName, "", "音乐模块未启动")
		} else {
			s.publishRequestNotice("music_request_failed", event.UserName, "", "音乐模块未启动")
		}
		return
	}
	// Route configured commands even when their feature toggle is off. The
	// worker reports the precise disabled reason through the shared request-notice stream.
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

func (s *MusicService) SetRequestNoticeBroker(broker *RequestNoticeBroker) {
	if s == nil { return }
	s.mu.Lock()
	s.notices = broker
	s.mu.Unlock()
}

func (s *MusicService) publishRequestNotice(kind, userName, itemName, reason string) {
	if s == nil { return }
	s.mu.RLock()
	broker := s.notices
	s.mu.RUnlock()
	if broker != nil { broker.Publish(RequestNotice{Kind:kind, UserName:userName, ItemName:itemName, Reason:reason}) }
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
	started := s.started
	commandEnabled := s.value.CommandEnabled
	prefix := s.value.CommandPrefix
	skipEnabled := s.value.SkipCommandEnabled
	skipCommand := strings.TrimSpace(s.value.SkipCommand)
	requireFanMedal := s.value.RequireFanMedal
	fanMedalLevel := s.value.FanMedalLevel
	checker := s.fanMedalChecker
	s.mu.RUnlock()
	if isSkipCommand(event.Content, skipCommand) {
		if !started || !enabled {
			s.publishRequestNotice("music_skip_failed", event.UserName, "", "音乐模块当前未启用")
			return
		}
		if !skipEnabled {
			s.publishRequestNotice("music_skip_failed", event.UserName, "", "音乐切歌指令当前已关闭")
			return
		}
		if !canSkipMusic(event) {
			s.publishRequestNotice("music_skip_failed", event.UserName, "", "仅主播或房管可以切歌")
			return
		}
		if err := s.Next(); err != nil {
			s.setLastError(fmt.Errorf("执行弹幕切歌失败：%w", err))
			s.publishRequestNotice("music_skip_failed", event.UserName, "", err.Error())
		} else {
			s.publishRequestNotice("music_skip_success", event.UserName, "", "")
		}
		return
	}
	if !isSongRequestCommand(event.Content, prefix) { return }
	if !started || !enabled {
		s.publishRequestNotice("music_request_failed", event.UserName, "", "音乐模块当前未启用")
		return
	}
	if !commandEnabled {
		s.publishRequestNotice("music_request_failed", event.UserName, "", "音乐点歌指令当前已关闭")
		return
	}
	keyword, ok := parseSongRequest(event.Content, prefix)
	if !ok {
		s.publishRequestNotice("music_request_failed", event.UserName, "", "请在点歌指令后输入歌曲名称")
		return
	}
	if requireFanMedal && !event.IsRoomOwner && !event.IsModerator {
		if checker == nil { reason := "音乐点播粉丝勋章校验服务不可用"; s.setLastError(errors.New(reason)); s.publishRequestNotice("music_request_failed", event.UserName, "", reason); return }
		allowed, level, checkErr := checker.Check(ctx, event.UserID, fanMedalLevel)
		if checkErr != nil { reason := "粉丝勋章校验失败：" + checkErr.Error(); s.setLastError(fmt.Errorf("校验观众 %s 的主播粉丝勋章失败，已拒绝本次点播：%w", event.UserName, checkErr)); s.publishRequestNotice("music_request_failed", event.UserName, "", reason); return }
		if !allowed { reason := fmt.Sprintf("粉丝勋章等级不足（当前 %d 级，要求 %d 级）", level, fanMedalLevel); s.setLastError(fmt.Errorf("观众 %s 的主播粉丝勋章等级不足（当前 %d，要求 %d）", event.UserName, level, fanMedalLevel)); s.publishRequestNotice("music_request_failed", event.UserName, "", reason); return }
	}
	track, matched, sourceFailures := s.searchSongRequestTrack(ctx, keyword, keyword, "")
	if !matched {
		reason := "所有已启用音源均未找到匹配歌曲：" + keyword
		if len(sourceFailures) > 0 {
			reason += "（" + strings.Join(sourceFailures, "；") + "）"
		}
		s.setLastError(fmt.Errorf("观众 %s 点播的歌曲未找到匹配结果：%s；音源尝试：%s", event.UserName, keyword, strings.Join(sourceFailures, "；")))
		s.publishRequestNotice("music_request_failed", event.UserName, "", reason)
		return
	}
	if _, err := s.Enqueue(track, event.UserName); err != nil {
		s.setLastError(fmt.Errorf("加入观众点播队列失败：%w", err))
		s.publishRequestNotice("music_request_failed", event.UserName, "", err.Error())
	} else {
		s.publishRequestNotice("music_request_success", event.UserName, track.Title, "")
	}
}

// searchSongRequestTrack tries enabled sources in configured priority order.
func (s *MusicService) searchSongRequestTrack(parent context.Context, keyword, requestedTitle, requestedArtist string) (music.Track, bool, []string) {
	if s == nil || s.searcher == nil {
		return music.Track{}, false, []string{"洛雪搜索 SDK 未初始化"}
	}
	if parent == nil { parent = context.Background() }
	sourceIDs := s.enabledSearchSourceIDs()
	if len(sourceIDs) == 0 { return music.Track{}, false, []string{"没有启用任何搜索音源"} }
	return searchSongRequestAcrossSources(parent, sourceIDs, func(source string) ([]music.Track, error) {
		ctx, cancel := context.WithTimeout(parent, 18*time.Second)
		defer cancel()
		result, err := s.searcher.SearchProvider(ctx, source, keyword, 1)
		if err != nil { return nil, err }
		return result.Tracks, nil
	}, requestedTitle, requestedArtist)
}

// searchSongRequestAcrossSources keeps provider fallback deterministic and testable.
func searchSongRequestAcrossSources(parent context.Context, sourceIDs []string, search func(string) ([]music.Track, error), requestedTitle, requestedArtist string) (music.Track, bool, []string) {
	if parent == nil { parent = context.Background() }
	failures := make([]string, 0)
	for _, source := range sourceIDs {
		if err := parent.Err(); err != nil {
			failures = append(failures, "搜索已取消："+err.Error())
			break
		}
		tracks, err := search(source)
		if err != nil {
			failures = append(failures, source+" 搜索失败："+err.Error())
			continue
		}
		// First try the complete user query against the combined title and artist.
		// Only if that fails, interpret the final whitespace-delimited token as artist.
		if track, ok := selectSongRequestTrackByCombinedText(tracks, requestedTitle); ok {
			return track, true, failures
		}
		title, artist := splitSongRequestArtist(requestedTitle)
		if requestedArtist != "" {
			title, artist = requestedTitle, requestedArtist
		}
		if track, ok := selectSongRequestTrack(tracks, title, artist); ok {
			return track, true, failures
		}
		failures = append(failures, source+" 无匹配结果")
	}
	return music.Track{}, false, failures
}

// splitSongRequestArtist accepts an optional explicit separator or a final whitespace-delimited artist.
func splitSongRequestArtist(keyword string) (string, string) {
	keyword = strings.TrimSpace(keyword)
	for _, separator := range []string{" - ", "－", "—"} {
		if index := strings.LastIndex(keyword, separator); index >= 0 {
			title := strings.TrimSpace(keyword[:index])
			artist := strings.TrimSpace(keyword[index+len(separator):])
			if title != "" && artist != "" { return title, artist }
		}
	}
	// Unicode whitespace handling covers both ASCII spaces/tabs and full-width/ideographic spaces.
	lastSpace := -1
	for index, r := range keyword {
		if unicode.IsSpace(r) { lastSpace = index }
	}
	if lastSpace >= 0 {
		title := strings.TrimSpace(keyword[:lastSpace])
		artist := strings.TrimSpace(keyword[lastSpace:])
		if title != "" && artist != "" { return title, artist }
	}
	return keyword, ""
}

func selectSongRequestTrackByCombinedText(tracks []music.Track, keyword string) (music.Track, bool) {
	wanted := normalizeSongRequestText(keyword)
	if wanted == "" { return music.Track{}, false }
	var containsMatch music.Track
	hasContainsMatch := false
	for _, track := range tracks {
		combined := normalizeSongRequestText(track.Title + track.Artist)
		if combined == "" { continue }
		if combined == wanted { return track, true }
		if !hasContainsMatch && strings.Contains(combined, wanted) {
			containsMatch, hasContainsMatch = track, true
		}
	}
	return containsMatch, hasContainsMatch
}
func normalizeSongRequestText(value string) string {
	var normalized strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(r) || unicode.IsNumber(r) { normalized.WriteRune(r) }
	}
	return normalized.String()
}

// Exact title wins; only candidate-title contains request-title is accepted.
func selectSongRequestTrack(tracks []music.Track, requestedTitle, requestedArtist string) (music.Track, bool) {
	wantedTitle := normalizeSongRequestText(requestedTitle)
	wantedArtist := normalizeSongRequestText(requestedArtist)
	if wantedTitle == "" { return music.Track{}, false }
	var containsMatch music.Track
	hasContainsMatch := false
	for _, track := range tracks {
		candidateTitle := normalizeSongRequestText(track.Title)
		if candidateTitle == "" { continue }
		if wantedArtist != "" {
			candidateArtist := normalizeSongRequestText(track.Artist)
			if candidateArtist == "" || !strings.Contains(candidateArtist, wantedArtist) { continue }
		}
		if candidateTitle == wantedTitle { return track, true }
		if !hasContainsMatch && strings.Contains(candidateTitle, wantedTitle) {
			containsMatch, hasContainsMatch = track, true
		}
	}
	return containsMatch, hasContainsMatch
}

func isSkipCommand(content, command string) bool { return command != "" && strings.TrimSpace(content) == command }
func isSongRequestCommand(content, prefix string) bool {
	content = strings.TrimSpace(content)
	prefix = strings.TrimSpace(prefix)
	if content == "" || prefix == "" || !strings.HasPrefix(content, prefix) { return false }
	remainder := strings.TrimPrefix(content, prefix)
	if remainder == "" { return true }
	next, _ := utf8.DecodeRuneInString(remainder)
	return unicode.IsSpace(next)
}
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
	priority := s.enabledSearchSourceIDs()
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
	for _, id := range s.enabledSearchSourceIDs() {
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

// enabledSearchSourceIDs reads the independent platform selection from music.json.
func (s *MusicService) enabledSearchSourceIDs() []string {
	if s == nil || s.searcher == nil { return []string{} }
	s.mu.RLock()
	var configured []string
	if s.value.EnabledSearchSources != nil { configured = append([]string{}, s.value.EnabledSearchSources...) }
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

func (s *MusicService) search(parent context.Context, keyword string) (MusicSearchResponse, error) {
	return s.searchPage(parent, keyword, 1)
}

func (s *MusicService) searchPage(parent context.Context, keyword string, page int) (MusicSearchResponse, error) {
	if s == nil || s.searcher == nil { return MusicSearchResponse{}, errors.New("洛雪搜索 SDK 未初始化") }
	if parent == nil { parent = context.Background() }
	sourceIDs := s.enabledSearchSourceIDs()
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

	// A stale queue.Current must not strand new requests after 洛雪 was stopped
	// outside My-LiveHub. Keep the current item only while playback is actually
	// playing or intentionally paused.
	queueBefore := s.queue.Snapshot()
	startImmediately := queueBefore.Current == nil
	staleCurrent := false
	if queueBefore.Current != nil && s.engine != nil {
		if playback, statusErr := s.engine.Snapshot(context.Background()); statusErr == nil {
			staleCurrent = playback.State == music.PlaybackIdle ||
				playback.State == music.PlaybackStopped ||
				playback.State == music.PlaybackError
		}
	}

	item, err := s.queue.Enqueue(track, requestedBy)
	if err != nil { return music.QueueItem{}, err }
	if staleCurrent {
		s.cancelPlaybackWatcher()
		s.queue.FinishCurrent()
		startImmediately = true
	}
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
	if err := s.engine.Pause(context.Background()); err != nil { return err }
	s.cancelPlaybackWatcher()
	return nil
}
func (s *MusicService) Resume() error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	if err := s.engine.Resume(context.Background()); err != nil { return err }
	s.startPlaybackWatcher()
	return nil
}
func (s *MusicService) Next() error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	snapshot := s.queue.Snapshot()
	var nextTrack interface { NextTrack(context.Context) error }
	if len(snapshot.Items) == 0 {
		// Validate 洛雪 queue support before retiring the current track. A
		// failed capability check must leave the current playback untouched.
		var ok bool
		nextTrack, ok = s.engine.(interface { NextTrack(context.Context) error })
		if !ok { return errors.New("当前播放引擎不支持切换洛雪队列") }
	}
	s.cancelPlaybackWatcher()
	if current := snapshot.Current; current != nil { s.pushHistory(*current) }
	s.queue.FinishCurrent()
	if len(snapshot.Items) > 0 { return s.PlayNext() }
	// When the My-LiveHub request queue is empty, advance 洛雪's own playlist.
	if err := nextTrack.NextTrack(context.Background()); err != nil {
		s.startPlaybackWatcher()
		return fmt.Errorf("切换洛雪队列下一首失败：%w", err)
	}
	return nil
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
	// lxmusic:// 播放协议会直接切换歌曲；不要调用 Stop，因为洛雪的
	// Stop 兼容实现实际会调用 /pause。
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

func (s *MusicService) GetLyrics(track music.Track) (string, error) {
	if s == nil || s.engine == nil { return "", errors.New("music lyrics service is unavailable") }
	provider, ok := s.engine.(interface {
		GetCurrentLyrics(context.Context) (string, error)
	})
	if !ok { return "", errors.New("当前播放引擎不支持读取洛雪歌词 API") }
	if normalizeLXTrackIdentity(track.Title) == "" || normalizeLXTrackIdentity(track.Artist) == "" {
		return "", errors.New("请求歌曲缺少歌名或歌手，无法核对洛雪当前播放歌曲")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	// 先确认洛雪当前播放的歌曲，不能直接把 /lyric-all 的结果归属给请求歌曲。
	before, err := s.engine.Snapshot(ctx)
	if err != nil { return "", fmt.Errorf("读取洛雪当前播放状态失败，暂不显示歌词：%w", err) }
	if before.Track == nil || !sameLXTrackIdentity(track, *before.Track) {
		return "", nil
	}

	source := firstNonEmpty(track.LXSourceKey, track.SourceID)
	cacheKey := strings.Join([]string{source, track.ID, track.Title, track.Artist}, "\x00")
	s.lyricsMu.Lock()
	cached, cachedOK := s.lyricsCache[cacheKey]
	s.lyricsMu.Unlock()
	if cachedOK {
		// 缓存也必须以本次已核对的洛雪播放状态为前提。
		return cached, nil
	}

	lyrics, err := provider.GetCurrentLyrics(ctx)
	if err != nil { return "", err }
	if strings.TrimSpace(lyrics) == "" { return "", nil }

	// 歌词读取期间可能刚好切歌；再次核对，避免把上一首/下一首歌词写入当前歌曲缓存。
	after, err := s.engine.Snapshot(ctx)
	if err != nil { return "", fmt.Errorf("读取洛雪歌词后无法再次确认当前歌曲：%w", err) }
	if after.Track == nil || !sameLXTrackIdentity(track, *after.Track) || !sameLXTrackIdentity(*before.Track, *after.Track) {
		return "", nil
	}

	s.lyricsMu.Lock()
	if s.lyricsCache == nil { s.lyricsCache = make(map[string]string) }
	if len(s.lyricsCache) >= 128 { s.lyricsCache = make(map[string]string) }
	s.lyricsCache[cacheKey] = lyrics
	s.lyricsMu.Unlock()
	return lyrics, nil
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
	sawPlaying := true
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
				if message == "" {
					message = "洛雪播放器报告播放错误"
				}
				s.setLastError(errors.New(message))
				s.queue.FinishCurrent()
				if advanceErr := s.advanceAfterPlaybackEnd(); advanceErr != nil {
					s.setLastError(fmt.Errorf("播放失败后推进下一首失败：%w", advanceErr))
				}
				return
			}
			// 洛雪可能在歌曲结束时直接切到自己的下一首，状态仍保持 Playing。
			// 如果洛雪当前歌曲已不是 My-LiveHub 正在管理的歌曲，立即接管：
			// 有待播项就播放队列第一首；队列为空则保留洛雪已经切到的歌曲。
			if currentItem := s.queue.Snapshot().Current; currentItem != nil &&
				lxPlaybackMovedAwayFromCurrent(currentItem, snapshot) {
				s.mu.RLock()
				generationCurrent := s.playGeneration == generation
				s.mu.RUnlock()
				if !generationCurrent || ctx.Err() != nil {
					return
				}
				s.pushHistory(*currentItem)
				s.queue.FinishCurrent()
				if len(s.queue.Snapshot().Items) > 0 {
					if advanceErr := s.PlayNext(); advanceErr != nil {
						s.setLastError(fmt.Errorf("洛雪自动切歌后接管 My-LiveHub 待播队列失败：%w", advanceErr))
					}
				}
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
				if advanceErr := s.advanceAfterPlaybackEnd(); advanceErr != nil {
					s.setLastError(fmt.Errorf("歌曲播放结束后推进下一首失败：%w", advanceErr))
				}
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

// lxPlaybackMovedAwayFromCurrent detects 洛雪 auto-advancing its own playlist
// while My-LiveHub still has a different track marked as current.
func lxPlaybackMovedAwayFromCurrent(current *music.QueueItem, snapshot music.PlaybackSnapshot) bool {
	return current != nil &&
		snapshot.State == music.PlaybackPlaying &&
		snapshot.Track != nil &&
		!sameLXTrackIdentity(current.Track, *snapshot.Track)
}

// advanceAfterPlaybackEnd advances My-LiveHub's request queue first. Only when
// that queue is empty does it delegate to 洛雪's own playlist.
func (s *MusicService) advanceAfterPlaybackEnd() error {
	if s == nil || s.engine == nil || s.queue == nil {
		return errors.New("music playback service is unavailable")
	}
	if len(s.queue.Snapshot().Items) > 0 {
		return s.PlayNext()
	}
	nextTrack, ok := s.engine.(interface { NextTrack(context.Context) error })
	if !ok {
		return errors.New("当前播放引擎不支持切换洛雪队列")
	}
	if err := nextTrack.NextTrack(context.Background()); err != nil {
		return fmt.Errorf("My-LiveHub 队列为空，切换洛雪下一首失败：%w", err)
	}
	return nil
}

func cloneMusicConfig(value config.MusicConfig) config.MusicConfig { return value }

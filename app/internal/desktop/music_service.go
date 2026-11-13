package desktop

import (
	"context"
	"encoding/json"
			"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
	lxStoreOnce sync.Once
	lxStore *music.LXUserSourceStore
	lxStoreErr error
	lxLifecycleMu sync.Mutex
	lxSessionMu sync.Mutex
	lxSession *music.LXUserSourceProcess
	lxSessionID string
	lxUpdateMu sync.Mutex
	lxUpdates []music.LXUserSourceUpdate
	searcher *music.LXSearchClient
	lyricsMu sync.Mutex
	lyricsCache map[string]string
	fanMedalChecker interface { Check(context.Context, int, int) (bool, int, error) }
}

func NewMusicService(manager *config.Manager, value config.MusicConfig, buffers ...*events.Buffer) *MusicService {
	sources := music.NewSourceRegistry()
	return NewMusicServiceWithDependencies(manager, value, sources, music.NewMpvEngine(""), buffers...)
}

// NewMusicServiceWithFanMedalChecker wires the medal checker as an internal dependency.
func NewMusicServiceWithFanMedalChecker(manager *config.Manager, value config.MusicConfig, checker interface { Check(context.Context, int, int) (bool, int, error) }, buffers ...*events.Buffer) *MusicService {
	service := NewMusicService(manager, value, buffers...)
	service.fanMedalChecker = checker
	return service
}

func NewMusicServiceWithDependencies(manager *config.Manager, value config.MusicConfig, sources *music.SourceRegistry, engine music.PlaybackEngine, buffers ...*events.Buffer) *MusicService {
	if sources == nil { sources = music.NewSourceRegistry() }
	if engine == nil { engine = music.NewMpvEngine("") }
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
	// The mpv engine owns a separate process and IPC pipe. Starting the music
	// service without starting that lifecycle leaves Load() with no IPC pipe,
	// which is reported as ErrPlaybackEngineUnavailable on the first track.
	if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok {
		if err := lifecycle.Start(ctx); err != nil {
			return fmt.Errorf("启动 mpv 播放引擎失败：%w", err)
		}
	}
	if err := s.engine.SetVolume(ctx, s.value.Volume); err != nil { return fmt.Errorf("恢复音乐音量失败：%w", err) }
	if processor, ok := s.engine.(music.PlaybackEngineAudioProcessing); ok {
		if err := processor.SetLoudnessNormalization(ctx, s.value.LoudnessNormalization); err != nil {
			return fmt.Errorf("恢复响度标准化设置失败：%w", err)
		}
	} else if s.value.LoudnessNormalization {
		return errors.New("当前播放引擎不支持响度标准化")
	}
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
	if store, err := s.lxUserSourceStore(); err == nil {
		if activeID, activeErr := store.GetActiveID(); activeErr == nil && activeID != "" {
			if err := s.SetActiveLXUserSource(activeID); err != nil { s.setLastError(err) }
		} else if activeErr != nil { s.setLastError(activeErr) }
	}
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
	oldConfig := s.GetConfig()
	processor, supportsAudioProcessing := s.engine.(music.PlaybackEngineAudioProcessing)
	normalizationChanged := oldConfig.LoudnessNormalization != value.LoudnessNormalization
	if value.LoudnessNormalization && !supportsAudioProcessing {
		return errors.New("当前播放引擎不支持响度标准化")
	}
	if normalizationChanged && supportsAudioProcessing {
		if err := processor.SetLoudnessNormalization(context.Background(), value.LoudnessNormalization); err != nil {
			return fmt.Errorf("应用响度标准化设置失败：%w", err)
		}
	}
	rollbackNormalization := func() error {
		if normalizationChanged && supportsAudioProcessing {
			return processor.SetLoudnessNormalization(context.Background(), oldConfig.LoudnessNormalization)
		}
		return nil
	}
	oldCapacity := s.queue.Snapshot().Capacity
	if err := s.queue.Resize(value.QueueCapacity); err != nil {
		if rollbackErr := rollbackNormalization(); rollbackErr != nil { return fmt.Errorf("调整播放队列失败：%v；恢复原响度标准化设置失败：%w", err, rollbackErr) }
		return err
	}
	if err := config.SaveTyped(s.manager, "music.json", value, func(candidate config.MusicConfig) error { return candidate.Validate() }); err != nil {
		_ = s.queue.Resize(oldCapacity)
		if rollbackErr := rollbackNormalization(); rollbackErr != nil { return fmt.Errorf("保存音乐配置失败：%v；恢复原响度标准化设置失败：%w", err, rollbackErr) }
		return err
	}
	s.mu.Lock(); wasEnabled := s.value.Enabled; s.value = cloneMusicConfig(value); s.mu.Unlock()
	if wasEnabled && !value.Enabled { s.cancelPlaybackWatcher(); _ = s.engine.Stop(context.Background()); s.queue.Clear(); s.queue.FinishCurrent(); s.mu.Lock(); s.history = nil; s.mu.Unlock() }
	return nil
}

func (s *MusicService) GetSources() []music.SourceSnapshot {
	if s == nil { return []music.SourceSnapshot{} }
	result := []music.SourceSnapshot{}
	if s.searcher != nil { result = append(result, s.searcher.Sources()...) }
	if s.sources != nil {
		result = append(result, s.sources.List(nil)...)
		for i := range result { result[i].Enabled = true }
	}
	s.lxSessionMu.Lock()
	session := s.lxSession
	s.lxSessionMu.Unlock()
	if session != nil && session.IsRunning() {
		state := session.Snapshot()
		for _, declared := range state.DeclaredSources {
			index := -1
			for i := range result { if result[i].ID == declared.Key { index = i; break } }
			canResolve := false
			for _, action := range declared.Actions { if action == "musicUrl" { canResolve = true; break } }
			if index >= 0 {
				result[index].Capabilities.Resolve = result[index].Capabilities.Resolve || canResolve
				result[index].Actions = append([]string(nil), declared.Actions...)
				result[index].Qualitys = append([]string(nil), declared.Qualitys...)
				result[index].Available = state.Initialized
				result[index].Enabled = true
				continue
			}
			result = append(result, music.SourceSnapshot{
				ID: declared.Key, Name: lxPlatformDisplayName(declared.Key),
				Capabilities: music.SourceCapabilities{Resolve: canResolve},
				Actions: append([]string(nil), declared.Actions...),
				Qualitys: append([]string(nil), declared.Qualitys...),
				Enabled: true, Available: state.Initialized, Error: state.Message,
			})
		}
	}
	sort.Slice(result, func(i,j int) bool { return result[i].ID < result[j].ID })
	return result
}

func lxPlatformDisplayName(key string) string {
	switch key {
	case "kw": return "酷我音乐"
	case "kg": return "酷狗音乐"
	case "tx": return "QQ 音乐"
	case "wy": return "网易云音乐"
	case "mg": return "咪咕音乐"
	case "local": return "本地音乐"
	default: return key
	}
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
		return MusicSearchResponse{}, fmt.Errorf("当前启用的音源脚本未声明 %s 的 musicUrl 能力，或该来源暂不支持搜索", source)
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

// activeLXSearchSourceIDs returns only searchable platform keys declared by
// the currently enabled and initialized LX user script with musicUrl support.
func (s *MusicService) activeLXSearchSourceIDs() []string {
	if s == nil || s.searcher == nil { return []string{} }
	s.lxSessionMu.Lock()
	session := s.lxSession
	sessionID := s.lxSessionID
	s.lxSessionMu.Unlock()
	if session == nil || !session.IsRunning() { return []string{} }
	state := session.Snapshot()
	store, err := s.lxUserSourceStore()
	if err != nil { return []string{} }
	metadata, err := store.Get(sessionID)
	if err != nil { return []string{} }
	declared := make(map[string]bool)
	for _, item := range state.DeclaredSources {
		if !containsLXAction(item.Actions, "musicUrl") { continue }
		declared[item.Key] = true
	}
	priority := metadata.EnabledSearchSources
	if priority == nil {
		priority = make([]string, 0, len(s.searcher.Sources()))
		for _, item := range s.searcher.Sources() { priority = append(priority, item.ID) }
	}
	searchable := make(map[string]bool)
	for _, item := range s.searcher.Sources() { searchable[item.ID] = true }
	result := make([]string, 0, len(priority))
	seen := make(map[string]bool)
	for _, id := range priority {
		if !declared[id] || !searchable[id] || seen[id] { continue }
		result = append(result, id)
		seen[id] = true
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


func shouldAutoStartSongRequest(current *music.QueueItem) bool {
	return current == nil || current.RequestedBy == hostQueueMarker
}
func (s *MusicService) Enqueue(track music.Track, requestedBy string) (music.QueueItem, error) {
	if s == nil || s.queue == nil { return music.QueueItem{}, errors.New("music queue is unavailable") }
	s.mu.RLock(); enabled := s.value.Enabled; s.mu.RUnlock()
	if !enabled { return music.QueueItem{}, errors.New("music module is disabled") }
	state := hostStateFor(s)
	state.mu.Lock()
	current := s.queue.Snapshot().Current
	preempt := current != nil && current.RequestedBy == hostQueueMarker
	// If playback is idle, the first accepted request must start immediately.
	// Previously only requests interrupting the host playlist called PlayNext,
	// leaving requests accepted while idle stranded in the pending queue.
	startImmediately := current == nil
	if preempt {
		snapshot, err := s.engine.Snapshot(context.Background())
		if err != nil { state.mu.Unlock(); return music.QueueItem{}, fmt.Errorf("保存主播歌曲播放进度失败：%w", err) }
		state.resumeItem = current
		state.resumePosition = snapshot.PositionMS
		state.resumeWasPaused = snapshot.State == music.PlaybackPaused
		state.resumePending = true
		if err := s.engine.Pause(context.Background()); err != nil {
			state.resumePending = false
			state.resumeItem = nil
			state.resumeWasPaused = false
			state.mu.Unlock()
			return music.QueueItem{}, fmt.Errorf("暂停主播歌曲失败：%w", err)
		}
		s.cancelPlaybackWatcher()
		s.queue.FinishCurrent()
	}
	state.mu.Unlock()
	item, err := s.queue.Enqueue(track, requestedBy)
	if err != nil {
		if preempt { _ = s.resumeHostPlaylist() }
		return music.QueueItem{}, err
	}
	if preempt || startImmediately {
		if err := s.PlayNext(); err != nil {
			if preempt {
				s.queue.FinishCurrent()
				_ = s.resumeHostPlaylist()
			} else {
				// Keep the accepted request queued if playback cannot start yet,
				// rather than silently losing it after TakeNext promoted it.
				if failed := s.queue.Snapshot().Current; failed != nil && failed.ID == item.ID {
					s.queue.FinishCurrent()
					_ = s.queue.EnqueueFront(*failed)
				}
			}
			return item, err
		}
	}
	return item, nil
}

// PlayTrackNow starts a search result immediately without inserting it into
// the pending audience-request queue. Pending audience requests keep their order.
func (s *MusicService) PlayTrackNow(track music.Track) error {
	if s == nil || s.queue == nil || s.engine == nil { return errors.New("music playback service is unavailable") }
	s.mu.RLock()
	enabled := s.value.Enabled
	s.mu.RUnlock()
	if !enabled { return errors.New("music module is disabled") }

	state := hostStateFor(s)
	state.mu.Lock()
	current := s.queue.Snapshot().Current
	preemptHost := current != nil && current.RequestedBy == hostQueueMarker
	if preemptHost {
		snapshot, err := s.engine.Snapshot(context.Background())
		if err != nil { state.mu.Unlock(); return fmt.Errorf("保存主播歌曲播放进度失败：%w", err) }
		state.resumeItem = current
		state.resumePosition = snapshot.PositionMS
		state.resumeWasPaused = snapshot.State == music.PlaybackPaused
		state.resumePending = true
		if err := s.engine.Pause(context.Background()); err != nil {
			state.resumePending = false
			state.resumeItem = nil
			state.resumeWasPaused = false
			state.mu.Unlock()
			return fmt.Errorf("暂停主播歌曲失败：%w", err)
		}
	}
	state.mu.Unlock()

	if current != nil { s.pushHistory(*current) }
	s.cancelPlaybackWatcher()
	if _, err := s.queue.SetCurrentTrack(track, searchPlayMarker); err != nil {
		if preemptHost { _ = s.resumeHostPlaylist() }
		return err
	}
	if err := s.PlayNext(); err != nil {
		s.queue.FinishCurrent()
		if preemptHost {
			_ = s.resumeHostPlaylist()
		} else if current != nil {
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
	track := current.Track
	sourceKey := track.LXSourceKey
	if sourceKey == "" { sourceKey = track.SourceID }

	activeID, err := s.GetActiveLXUserSourceID()
	if err != nil { return fmt.Errorf("读取当前洛雪自定义源失败：%w", err) }
	if activeID == "" { return errors.New("尚未启用洛雪自定义源，无法解析歌曲播放地址") }

	s.lxSessionMu.Lock()
	session := s.lxSession
	sessionID := s.lxSessionID
	s.lxSessionMu.Unlock()
	if session == nil || sessionID != activeID || !session.IsRunning() {
		if err := s.SetActiveLXUserSource(activeID); err != nil { return fmt.Errorf("启动洛雪自定义源失败：%w", err) }
		s.lxSessionMu.Lock()
		session = s.lxSession
		sessionID = s.lxSessionID
		s.lxSessionMu.Unlock()
	}
	if session == nil || sessionID != activeID { return errors.New("当前洛雪自定义源未能启动") }

	var declared *music.LXDeclaredSource
	for _, item := range session.Snapshot().DeclaredSources {
		if item.Key == sourceKey { copy := item; declared = &copy; break }
	}
	if declared == nil { return fmt.Errorf("当前自定义源未声明平台 %s 的音乐解析能力", sourceKey) }
	hasMusicURL := false
	for _, action := range declared.Actions { if action == "musicUrl" { hasMusicURL = true; break } }
	if !hasMusicURL { return fmt.Errorf("当前自定义源未声明 %s 的 musicUrl 动作", sourceKey) }

	selected, err := chooseLXTrackQuality(track, *declared)
	if err != nil { return err }
	payload, err := buildLXMusicURLRequest(track, selected)
	if err != nil { return fmt.Errorf("构建洛雪播放请求失败：%w", err) }
	ctx, cancel := context.WithTimeout(parent, 22*time.Second)
	defer cancel()
	rawURL, err := session.Request(ctx, payload)
	if err != nil { return fmt.Errorf("解析歌曲播放地址失败：%w", err) }
	playbackURL, err := parseLXMusicURLResponse(rawURL, sourceKey, selected.Type)
	if err != nil { return err }
	if err := ctx.Err(); err != nil { return err }
	if err := loadAndStartPlayback(ctx, s.engine, music.PlaybackResource{URL: playbackURL}, track); err != nil {
		return err
	}
	s.startPlaybackWatcher()
	return nil
}


// loadAndStartPlayback explicitly clears mpv's global pause property after
// replacing the current file. mpv retains pause=true across loadfile calls,
// so a viewer request that preempts a paused host track can otherwise appear
// to switch tracks while remaining silent.
func loadAndStartPlayback(ctx context.Context, engine music.PlaybackEngine, resource music.PlaybackResource, track music.Track) error {
	if err := engine.Load(ctx, resource, track); err != nil {
		return fmt.Errorf("mpv 加载歌曲失败：%w", err)
	}
	if err := engine.Resume(ctx); err != nil {
		return fmt.Errorf("启动歌曲播放失败：%w", err)
	}
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
	if err := s.engine.SetVolume(context.Background(), volume); err != nil { return err }
	s.mu.Lock()
	next := s.value
	next.Volume = volume
	if s.manager != nil {
		if err := config.SaveTyped(s.manager, "music.json", next, func(value config.MusicConfig) error { return value.Validate() }); err != nil {
			s.mu.Unlock()
			return fmt.Errorf("保存音乐音量失败：%w", err)
		}
	}
	s.value = next
	s.mu.Unlock()
	return nil
}

func (s *MusicService) Next() error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	s.cancelPlaybackWatcher()
	current := s.queue.Snapshot().Current
	if current != nil { s.pushHistory(*current) }
	_ = s.engine.Stop(context.Background())
	s.queue.FinishCurrent()
	if current != nil && current.RequestedBy == hostQueueMarker { return s.advanceHostPlaylist() }
	if pending := s.queue.Snapshot().Items; len(pending) > 0 { return s.PlayNext() }
	if s.resumeHostPlaylist() { return nil }
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
	return s.engine.Snapshot(context.Background())
}
func (s *MusicService) GetLXUserSourceProxy() config.LXUserSourceProxyConfig {
	if s == nil { return config.LXUserSourceProxyConfig{} }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.value.LXUserSourceProxy
}

func (s *MusicService) UpdateLXUserSourceProxy(enabled bool, host, port string) (config.LXUserSourceProxyConfig, error) {
	if s == nil || s.manager == nil { return config.LXUserSourceProxyConfig{}, errors.New("music configuration service is unavailable") }
	next := config.LXUserSourceProxyConfig{Enabled: enabled, Host: strings.TrimSpace(host), Port: strings.TrimSpace(port)}
	if err := next.Validate(); err != nil { return config.LXUserSourceProxyConfig{}, err }
	s.mu.Lock()
	updated := s.value
	updated.LXUserSourceProxy = next
	if err := config.SaveTyped(s.manager, "music.json", updated, func(value config.MusicConfig) error { return value.Validate() }); err != nil {
		s.mu.Unlock()
		return config.LXUserSourceProxyConfig{}, fmt.Errorf("save LX source proxy settings: %w", err)
	}
	s.value = updated
	s.mu.Unlock()
	s.lxSessionMu.Lock()
	session := s.lxSession
	s.lxSessionMu.Unlock()
	if session != nil {
		if err := session.SetProxy(next); err != nil { return next, fmt.Errorf("proxy settings saved but active LX source update failed: %w", err) }
	}
	return next, nil
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
	s.lxLifecycleMu.Lock()
	defer s.lxLifecycleMu.Unlock()
	s.cancelPlaybackWatcher()
	s.closeLXUserSourceSession()
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
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := s.engine.Snapshot(ctx)
			if err != nil { continue }
			if snapshot.DurationMS > 0 {
				threshold := snapshot.DurationMS - 1000
				if threshold < snapshot.DurationMS/2 { threshold = snapshot.DurationMS/2 }
				if snapshot.PositionMS >= threshold { sawEndPosition = true }
			}
			if snapshot.State == music.PlaybackError {
				message := strings.TrimSpace(snapshot.Error)
				if message == "" { message = "mpv 加载音频失败" }
				s.setLastError(errors.New(message))
				currentItem := s.queue.Snapshot().Current
				s.queue.FinishCurrent()
				if currentItem != nil && currentItem.RequestedBy == hostQueueMarker {
					_ = s.advanceHostPlaylist()
				} else if len(s.queue.Snapshot().Items) > 0 {
					_ = s.PlayNext()
				} else {
					_ = s.resumeHostPlaylist()
				}
				return
			}
			if snapshot.State != music.PlaybackStopped { continue }
			s.mu.RLock()
			current := s.playGeneration == generation
			s.mu.RUnlock()
			if !current { return }
			if sawEndPosition {
				currentItem := s.queue.Snapshot().Current
				if currentItem != nil { s.pushHistory(*currentItem) }
				s.queue.FinishCurrent()
				if currentItem != nil && currentItem.RequestedBy == hostQueueMarker {
					_ = s.advanceHostPlaylist()
				} else if len(s.queue.Snapshot().Items) == 0 {
					if !s.resumeHostPlaylist() { _ = s.PlayNext() }
				} else {
					_ = s.PlayNext()
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

func cloneMusicConfig(value config.MusicConfig) config.MusicConfig { return value }
func cloneBoolMap(value map[string]bool) map[string]bool {
	copy := make(map[string]bool, len(value))
	for id, enabled := range value { copy[id] = enabled }
	return copy
}

func (s *MusicService) lxUserSourceStore() (*music.LXUserSourceStore, error) {
	if s == nil { return nil, errors.New("音乐服务不可用") }
	s.lxStoreOnce.Do(func() {
		configDir, err := os.UserConfigDir()
		if err != nil { s.lxStoreErr = fmt.Errorf("获取用户配置目录失败：%w", err); return }
		s.lxStore, s.lxStoreErr = music.NewLXUserSourceStore(filepath.Join(configDir, "My-LiveHub", "music-sources"))
	})
	return s.lxStore, s.lxStoreErr
}

// GetLXUserSources lists imported LX-compatible source scripts. Scripts are
// executed only when selected through SetActiveLXUserSource.
func (s *MusicService) GetLXUserSources() ([]music.LXUserSourceMetadata, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return nil, err }
	return store.List()
}

func (s *MusicService) ImportLXSource(script string) (music.LXUserSourceMetadata, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXUserSourceMetadata{}, err }
	return store.ImportScript(script, "")
}

func (s *MusicService) ImportLXSourceURL(sourceURL string) (music.LXUserSourceMetadata, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXUserSourceMetadata{}, err }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return store.ImportURL(ctx, sourceURL)
}

func (s *MusicService) DeleteLXUserSource(id string) error {
	if s == nil { return errors.New("音乐服务不可用") }
	s.lxLifecycleMu.Lock()
	defer s.lxLifecycleMu.Unlock()
	store, err := s.lxUserSourceStore()
	if err != nil { return err }
	activeID, err := store.GetActiveID()
	if err != nil { return err }
	s.lxSessionMu.Lock()
	sessionActive := s.lxSessionID == id
	s.lxSessionMu.Unlock()
	wasActive := activeID == id || sessionActive
	if wasActive {
		if err := s.setActiveLXUserSourceLocked(""); err != nil { return err }
	}
	if err := store.Delete(id); err != nil {
		if wasActive { _ = s.setActiveLXUserSourceLocked(id) }
		return err
	}
	return nil
}

func (s *MusicService) UpdateLXUserSource(id string) (music.LXUserSourceMetadata, error) {
	if s == nil { return music.LXUserSourceMetadata{}, errors.New("音乐服务不可用") }
	s.lxLifecycleMu.Lock()
	defer s.lxLifecycleMu.Unlock()
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXUserSourceMetadata{}, err }
	activeID, err := store.GetActiveID()
	if err != nil { return music.LXUserSourceMetadata{}, err }
	s.lxSessionMu.Lock()
	sessionActive := s.lxSessionID == id
	s.lxSessionMu.Unlock()
	wasActive := activeID == id || sessionActive
	if wasActive {
		if err := s.setActiveLXUserSourceLocked(""); err != nil { return music.LXUserSourceMetadata{}, err }
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	metadata, err := store.Update(ctx, id)
	if err != nil {
		if wasActive { _ = s.setActiveLXUserSourceLocked(id) }
		return music.LXUserSourceMetadata{}, err
	}
	if wasActive {
		if err := s.setActiveLXUserSourceLocked(metadata.ID); err != nil {
			return metadata, fmt.Errorf("音源已更新，但重新启用失败：%w", err)
		}
	}
	return metadata, nil
}

type LXSearchSourceOption struct {
	ID string `json:"id"`
	Name string `json:"name"`
	Enabled bool `json:"enabled"`
}

func (s *MusicService) lxSearchSourceOptions(result music.LXSourceRuntimeResult, enabled []string) []LXSearchSourceOption {
	if s == nil || s.searcher == nil { return []LXSearchSourceOption{} }
	declared := make(map[string]bool)
	for _, item := range result.DeclaredSources {
		if containsLXAction(item.Actions, "musicUrl") { declared[item.Key] = true }
	}
	names := make(map[string]string)
	for _, item := range s.searcher.Sources() { names[item.ID] = item.Name }
	selected := make(map[string]bool, len(enabled))
	for _, key := range enabled { selected[key] = true }
	options := make([]LXSearchSourceOption, 0)
	seen := make(map[string]bool)
	appendOption := func(id string) {
		name, supported := names[id]
		if !supported || !declared[id] || seen[id] { return }
		options = append(options, LXSearchSourceOption{ID:id, Name:name, Enabled:selected[id]})
		seen[id] = true
	}
	// Enabled options follow the saved priority; disabled options follow the
	// built-in provider order so they have a predictable position when enabled.
	for _, id := range enabled { appendOption(id) }
	for _, item := range s.searcher.Sources() { appendOption(item.ID) }
	return options
}

func (s *MusicService) GetLXSearchSourceOptions(id string) ([]LXSearchSourceOption, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return nil, err }
	metadata, err := store.Get(id)
	if err != nil { return nil, err }
	if metadata.Validation == nil || !metadata.Validation.Initialized { return []LXSearchSourceOption{}, nil }
	enabled := metadata.EnabledSearchSources
	if enabled == nil {
		for _, option := range s.lxSearchSourceOptions(*metadata.Validation, nil) { enabled = append(enabled, option.ID) }
	}
	return s.lxSearchSourceOptions(*metadata.Validation, enabled), nil
}

func (s *MusicService) SetLXSearchSources(id string, enabled []string) error {
	store, err := s.lxUserSourceStore()
	if err != nil { return err }
	metadata, err := store.Get(id)
	if err != nil { return err }
	if metadata.Validation == nil || !metadata.Validation.Initialized { return errors.New("请先验证当前音源脚本") }
	options := s.lxSearchSourceOptions(*metadata.Validation, nil)
	allowed := make([]string, 0, len(options))
	for _, option := range options { allowed = append(allowed, option.ID) }
	return store.SetEnabledSearchSources(id, allowed, enabled)
}

func (s *MusicService) ValidateLXUserSource(id string) (music.LXSourceRuntimeResult, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	metadata, err := store.Get(id)
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	// A successful validation is bound to the stored script version and is
	// reused after app restarts. Updating/reimporting the script clears it.
	if metadata.Validation != nil && metadata.ValidatedAt != nil && metadata.Validation.Initialized {
		if metadata.EnabledSearchSources == nil {
			defaults := make([]string, 0)
			for _, option := range s.lxSearchSourceOptions(*metadata.Validation, nil) { defaults = append(defaults, option.ID) }
			if err := store.SetEnabledSearchSources(id, defaults, defaults); err != nil { return music.LXSourceRuntimeResult{}, err }
		}
		return *metadata.Validation, nil
	}
	script, err := store.ReadScript(id)
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	result, err := music.InspectLXUserSource(ctx, script)
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	if err := store.SetValidation(id, result); err != nil {
		return music.LXSourceRuntimeResult{}, fmt.Errorf("音源验证通过，但保存验证状态失败：%w", err)
	}
	defaults := make([]string, 0)
	for _, option := range s.lxSearchSourceOptions(result, nil) { defaults = append(defaults, option.ID) }
	if err := store.SetEnabledSearchSources(id, defaults, defaults); err != nil { return music.LXSourceRuntimeResult{}, fmt.Errorf("音源验证通过，但保存默认搜索来源失败：%w", err) }
	return result, nil
}


type LXSourcePlatformTest struct {
	Platform string `json:"platform"`
	Name string `json:"name"`
	Status string `json:"status"`
	Message string `json:"message"`
	Song string `json:"song,omitempty"`
}

// TestLXUserSource checks each declared platform independently: native search,
// the script's musicUrl resolver, and a real ranged audio response.
func (s *MusicService) TestLXUserSource(id string) ([]LXSourcePlatformTest, error) {
	if s == nil { return nil, errors.New("音乐服务不可用") }
	validation, err := s.ValidateLXUserSource(id)
	if err != nil { return nil, err }
	searchNames := map[string]string{"kw":"酷我音乐","kg":"酷狗音乐","tx":"QQ 音乐","wy":"网易云音乐","mg":"咪咕音乐"}
	declared := make(map[string]music.LXDeclaredSource, len(validation.DeclaredSources))
	for _, item := range validation.DeclaredSources { declared[item.Key] = item }
	platforms := []string{"tx", "wy", "kg", "kw", "mg"}
	results := make([]LXSourcePlatformTest, 0, len(platforms))
	for _, platform := range platforms {
		item, exists := declared[platform]
		result := LXSourcePlatformTest{Platform:platform, Name:searchNames[platform], Status:"unsupported", Message:"脚本未声明 musicUrl 动作"}
		if result.Name == "" { result.Name = platform }
		if !exists || !containsLXAction(item.Actions, "musicUrl") {
			results = append(results, result)
			continue
		}
		result.Status = "failed"
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		// Use the same provider search implementation as the music search UI.
		// Test exactly the first result for the plain keyword "稻香"; do not
		// silently skip it in favor of a later result.
		page, searchErr := s.searcher.SearchProvider(ctx, platform, "稻香", 1)
		if searchErr != nil || len(page.Tracks) == 0 {
			if searchErr != nil { result.Message = "搜索失败：" + searchErr.Error() } else { result.Message = "搜索成功，但没有找到测试歌曲" }
			cancel()
			results = append(results, result)
			continue
		}
		track := page.Tracks[0]
		// Match the UI's default "自动选择最佳音质" behavior. An explicit
		// SelectedQuality is honored by chooseLXTrackQuality; when absent it
		// selects the best quality supported by both the track and source.
		selected, qualityErr := chooseLXTrackQuality(track, item)
		if qualityErr != nil {
			result.Song = track.Title + " - " + track.Artist
			result.Message = "首条搜索结果没有可用的默认音质：" + qualityErr.Error()
			cancel()
			results = append(results, result)
			continue
		}
		result.Song = track.Title + " - " + track.Artist
		payload, payloadErr := buildLXMusicURLRequest(track, selected)
		if payloadErr != nil {
			result.Message = "构造解析请求失败：" + payloadErr.Error()
			cancel()
			results = append(results, result)
			continue
		}
		resolved, resolveErr := s.ResolveLXUserSource(id, payload)
		if resolveErr != nil {
			result.Message = "播放地址解析失败：" + resolveErr.Error()
			cancel()
			results = append(results, result)
			continue
		}
		playURL, parseErr := parseLXMusicURLResponse(resolved, platform, selected.Type)
		if parseErr != nil {
			result.Message = "播放地址无效：" + parseErr.Error()
			cancel()
			results = append(results, result)
			continue
		}
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, playURL, nil)
		if requestErr == nil { request.Header.Set("Range", "bytes=0-1023") }
		var response *http.Response
		if requestErr == nil {
			// Use the same configured proxy as the active LX runtime. The old
			// test used a bare http.Client, so sources that require the LX proxy
			// were incorrectly reported as unavailable.
			proxyConfig := s.GetLXUserSourceProxy()
			transport := http.DefaultTransport.(*http.Transport).Clone()
			if proxyConfig.Enabled && strings.TrimSpace(proxyConfig.Host) != "" && strings.TrimSpace(proxyConfig.Port) != "" {
				transport.Proxy = http.ProxyURL(&url.URL{Scheme: "http", Host: net.JoinHostPort(strings.TrimSpace(proxyConfig.Host), strings.TrimSpace(proxyConfig.Port))})
			} else {
				transport.Proxy = http.ProxyFromEnvironment
			}
			client := &http.Client{Timeout:12*time.Second, Transport:transport}
			response, requestErr = client.Do(request)
			transport.CloseIdleConnections()
		}
		if requestErr != nil {
			result.Message = "播放地址请求失败：" + requestErr.Error()
		} else {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 1024))
			_ = response.Body.Close()
			if readErr != nil { result.Message = "读取音频数据失败：" + readErr.Error()
			} else if response.StatusCode < 200 || response.StatusCode >= 300 { result.Message = fmt.Sprintf("音频服务器返回 HTTP %d", response.StatusCode)
			} else if len(body) == 0 { result.Message = "播放地址可访问，但没有返回音频数据"
			} else { result.Status = "passed"; result.Message = fmt.Sprintf("解析成功，已取得 %d 字节音频数据", len(body)) }
		}
		cancel()
		results = append(results, result)
	}
	return results, nil
}

// closeLXUserSourceSession releases the active custom source when it is
// replaced, deleted, or the desktop application shuts down.
func (s *MusicService) closeLXUserSourceSession() {
	s.lxSessionMu.Lock()
	session := s.lxSession
	s.lxSession, s.lxSessionID = nil, ""
	s.lxSessionMu.Unlock()
	if session != nil { _ = session.Close() }
}

// ResolveLXUserSource dispatches a request to the currently selected LX source.
// The same source is initialized only once and retains its JS global state;
// switching to another source closes the previous runtime, matching LX setApi().
// SetActiveLXUserSource mirrors LX userApi.setApi(): close the previous
// runtime first, persist the selected ID, then initialize the new script.
func (s *MusicService) SetActiveLXUserSource(id string) error {
	if s == nil { return errors.New("音乐服务不可用") }
	s.lxLifecycleMu.Lock()
	defer s.lxLifecycleMu.Unlock()
	return s.setActiveLXUserSourceLocked(strings.TrimSpace(id))
}

// setActiveLXUserSourceLocked starts and validates the replacement before
// persisting it or closing the current source. A failed activation therefore
// leaves the previously working source selected and running.
func (s *MusicService) setActiveLXUserSourceLocked(id string) error {
	store, err := s.lxUserSourceStore()
	if err != nil { return err }
	var next *music.LXUserSourceProcess
	if id != "" {
		script, err := store.ReadScript(id)
		if err != nil { return err }
		ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
		defer cancel()
		next, err = music.StartLXUserSourceProcess(ctx, script, s.GetLXUserSourceProxy())
		if err != nil { return err }
	}
	if err := store.SetActiveID(id); err != nil {
		if next != nil { _ = next.Close() }
		return err
	}
	s.lxSessionMu.Lock()
	previous := s.lxSession
	s.lxSession, s.lxSessionID = next, id
	s.lxSessionMu.Unlock()
	if previous != nil { _ = previous.Close() }
	if next != nil { go s.watchLXUserSourceUpdates(id, next) }
	return nil
}

func (s *MusicService) GetActiveLXUserSourceID() (string, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return "", err }
	return store.GetActiveID()
}

func (s *MusicService) SetLXUserSourceAllowUpdateAlert(id string, enabled bool) error {
	store, err := s.lxUserSourceStore()
	if err != nil { return err }
	return store.SetAllowShowUpdateAlert(id, enabled)
}

func (s *MusicService) GetLXUserSourceUpdates() []music.LXUserSourceUpdate {
	if s == nil { return []music.LXUserSourceUpdate{} }
	s.lxUpdateMu.Lock()
	updates := append([]music.LXUserSourceUpdate(nil), s.lxUpdates...)
	s.lxUpdates = s.lxUpdates[:0]
	s.lxUpdateMu.Unlock()
	return updates
}

func (s *MusicService) watchLXUserSourceUpdates(id string, session *music.LXUserSourceProcess) {
	for update := range session.Updates() {
		s.lxSessionMu.Lock()
		active := s.lxSession == session && s.lxSessionID == id
		s.lxSessionMu.Unlock()
		if !active { continue }
		store, err := s.lxUserSourceStore()
		if err != nil { continue }
		metadata, err := store.Get(id)
		if err != nil || !metadata.AllowShowUpdateAlert { continue }
		update.SourceID, update.Name, update.Description = id, metadata.Name, metadata.Description
		s.lxUpdateMu.Lock()
		if len(s.lxUpdates) >= 20 {
			copy(s.lxUpdates, s.lxUpdates[1:])
			s.lxUpdates = s.lxUpdates[:len(s.lxUpdates)-1]
		}
		s.lxUpdates = append(s.lxUpdates, update)
		s.lxUpdateMu.Unlock()
	}
}

func (s *MusicService) ResolveLXUserSource(id string, payload json.RawMessage) (json.RawMessage, error) {
	if s == nil { return nil, errors.New("音乐服务不可用") }
	s.lxSessionMu.Lock()
	active := s.lxSession != nil && s.lxSessionID == id && s.lxSession.IsRunning()
	s.lxSessionMu.Unlock()
	if !active {
		if err := s.SetActiveLXUserSource(id); err != nil { return nil, err }
	}
	s.lxSessionMu.Lock()
	session := s.lxSession
	activeID := s.lxSessionID
	s.lxSessionMu.Unlock()
	if session == nil || activeID != id { return nil, errors.New("LX 当前启用的音源已切换") }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return session.Request(ctx, payload)
}

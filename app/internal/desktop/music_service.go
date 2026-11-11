package desktop

import (
	"context"
	"encoding/json"
			"errors"
	"fmt"
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
}

func NewMusicService(manager *config.Manager, value config.MusicConfig, buffers ...*events.Buffer) *MusicService {
	sources := music.NewSourceRegistry()
	return NewMusicServiceWithDependencies(manager, value, sources, music.NewMpvEngine(""), buffers...)
}

func NewMusicServiceWithDependencies(manager *config.Manager, value config.MusicConfig, sources *music.SourceRegistry, engine music.PlaybackEngine, buffers ...*events.Buffer) *MusicService {
	if sources == nil { sources = music.NewSourceRegistry() }
	if engine == nil { engine = music.NewMpvEngine("") }
	var buffer *events.Buffer
	if len(buffers) > 0 { buffer = buffers[0] }
	return &MusicService{
		manager: manager, buffer: buffer, value: cloneMusicConfig(value),
		sources: sources, engine: engine, queue: music.NewQueue(value.QueueCapacity),
		searcher: music.NewLXSearchClient(nil),
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
	enabled := s.value.Enabled && s.value.CommandEnabled && s.started
	requests := s.requests
	prefix := s.value.CommandPrefix
	s.mu.RUnlock()
	if !enabled || requests == nil { return }
	if _, ok := parseSongRequest(event.Content, prefix); !ok { return }
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

func (s *MusicService) processSongRequest(ctx context.Context, event events.Event) {
	s.mu.RLock()
	enabled := s.value.Enabled && s.value.CommandEnabled && s.started
	prefix := s.value.CommandPrefix
	s.mu.RUnlock()
	if !enabled { return }
	keyword, ok := parseSongRequest(event.Content, prefix)
	if !ok { return }
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
	if err := config.SaveTyped(s.manager, "music.json", value, func(candidate config.MusicConfig) error { return candidate.Validate() }); err != nil { _ = s.queue.Resize(oldCapacity); return err }
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

func (s *MusicService) search(parent context.Context, keyword string) (MusicSearchResponse, error) {
	return s.searchPage(parent, keyword, 1)
}

func (s *MusicService) searchPage(parent context.Context, keyword string, page int) (MusicSearchResponse, error) {
	if s == nil || s.searcher == nil { return MusicSearchResponse{}, errors.New("洛雪搜索 SDK 未初始化") }
	if parent == nil { parent = context.Background() }
	ctx, cancel := context.WithTimeout(parent, 18*time.Second)
	defer cancel()
	result, sourceErrors, err := s.searcher.Search(ctx, keyword, page)
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
	state := hostStateFor(s)
	state.mu.Lock()
	current := s.queue.Snapshot().Current
	preempt := current != nil && current.RequestedBy == hostQueueMarker
	if preempt {
		snapshot, err := s.engine.Snapshot(context.Background())
		if err != nil { state.mu.Unlock(); return music.QueueItem{}, fmt.Errorf("保存主播歌曲播放进度失败：%w", err) }
		state.resumeItem = current
		state.resumePosition = snapshot.PositionMS
		state.resumePending = true
		if err := s.engine.Pause(context.Background()); err != nil {
			state.resumePending = false
			state.resumeItem = nil
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
	if preempt {
		if err := s.PlayNext(); err != nil { return item, err }
	}
	return item, nil
}

func (s *MusicService) GetQueue() music.QueueSnapshot {
	if s == nil || s.queue == nil { return music.QueueSnapshot{Items:[]music.QueueItem{}} }
	return s.queue.Snapshot()
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
	if err := s.engine.Load(ctx, music.PlaybackResource{URL: playbackURL}, track); err != nil {
		return fmt.Errorf("mpv 加载歌曲失败：%w", err)
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
	return s.engine.SetVolume(context.Background(), volume)
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

func (s *MusicService) ValidateLXUserSource(id string) (music.LXSourceRuntimeResult, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	script, err := store.ReadScript(id)
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	return music.InspectLXUserSource(ctx, script)
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

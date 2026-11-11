package desktop

import (
	"context"
	"encoding/json"
			"errors"
	"fmt"
	"os"
	"path/filepath"
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
	lxSessionMu sync.Mutex
	lxSession *music.LXUserSourceProcess
	lxSessionID string
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
	}
}

func (s *MusicService) start(ctx context.Context) error {
	if s == nil || ctx == nil { return errors.New("music service and startup context are required") }
	if s.buffer == nil { return errors.New("music event buffer is required") }
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
	if _, err := s.search(ctx, keyword); err != nil {
		s.setLastError(fmt.Errorf("搜索弹幕点歌失败：%w", err))
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

func (s *MusicService) GetSources() []music.SourceSnapshot { return []music.SourceSnapshot{} }

func (s *MusicService) Search(keyword string) (MusicSearchResponse, error) {
	return s.SearchPage(keyword, 1)
}

func (s *MusicService) SearchPage(keyword string, page int) (MusicSearchResponse, error) {
	return s.searchPage(context.Background(), keyword, page)
}

func (s *MusicService) search(parent context.Context, keyword string) (MusicSearchResponse, error) {
	return s.searchPage(parent, keyword, 1)
}

func (s *MusicService) searchPage(parent context.Context, keyword string, page int) (MusicSearchResponse, error) { return MusicSearchResponse{}, errors.New("洛雪 SDK 搜索尚未接入") }

func (s *MusicService) Enqueue(track music.Track, requestedBy string) (music.QueueItem, error) {
	if s == nil || s.queue == nil { return music.QueueItem{}, errors.New("music queue is unavailable") }
	s.mu.RLock(); enabled := s.value.Enabled; s.mu.RUnlock()
	if !enabled { return music.QueueItem{}, errors.New("music module is disabled") }
	return s.queue.Enqueue(track, requestedBy)
}

func (s *MusicService) GetQueue() music.QueueSnapshot {
	if s == nil || s.queue == nil { return music.QueueSnapshot{Items:[]music.QueueItem{}} }
	return s.queue.Snapshot()
}

func (s *MusicService) PlayNext() error { return s.playNext(context.Background()) }

func (s *MusicService) playNext(parent context.Context) error { return errors.New("洛雪 SDK 播放解析尚未接入") }

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
	if current := s.queue.Snapshot().Current; current != nil { s.pushHistory(*current) }
	_ = s.engine.Stop(context.Background())
	s.queue.FinishCurrent()
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
				if currentItem := s.queue.Snapshot().Current; currentItem != nil { s.pushHistory(*currentItem) }
				s.queue.FinishCurrent()
				_ = s.PlayNext()
				return
			}
			if time.Since(startedAt) >= 20*time.Second {
				if ctx.Err() != nil { return }
				s.setLastError(errors.New("播放器未能开始播放；洛雪 SDK 播放解析尚未接入"))
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
	store, err := s.lxUserSourceStore()
	if err != nil { return err }
	s.lxSessionMu.Lock()
	if s.lxSessionID == id {
		session := s.lxSession
		s.lxSession, s.lxSessionID = nil, ""
		if session != nil { _ = session.Close() }
	}
	s.lxSessionMu.Unlock()
	return store.Delete(id)
}

func (s *MusicService) UpdateLXUserSource(id string) (music.LXUserSourceMetadata, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXUserSourceMetadata{}, err }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	metadata, err := store.Update(ctx, id)
	if err != nil { return music.LXUserSourceMetadata{}, err }
	s.lxSessionMu.Lock()
	if s.lxSessionID == id {
		session := s.lxSession
		s.lxSession, s.lxSessionID = nil, ""
		if session != nil { _ = session.Close() }
	}
	s.lxSessionMu.Unlock()
	return metadata, nil
}

func (s *MusicService) ValidateLXUserSource(id string) (music.LXSourceRuntimeResult, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	script, err := store.ReadScript(id)
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
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
// SetActiveLXUserSource mirrors LX userApi.setApi(): only one custom source
// script is active at a time. Passing an empty ID closes the active source.
func (s *MusicService) SetActiveLXUserSource(id string) error {
	if s == nil { return errors.New("音乐服务不可用") }
	if strings.TrimSpace(id) == "" {
		s.closeLXUserSourceSession()
		return nil
	}
	s.lxSessionMu.Lock()
	defer s.lxSessionMu.Unlock()
	if s.lxSession != nil && s.lxSessionID == id { return nil }
	store, err := s.lxUserSourceStore()
	if err != nil { return err }
	script, err := store.ReadScript(id)
	if err != nil { return err }
	if s.lxSession != nil { _ = s.lxSession.Close() }
	s.lxSession, s.lxSessionID = nil, ""
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	session, err := music.StartLXUserSourceProcess(ctx, script)
	if err != nil { return err }
	s.lxSession, s.lxSessionID = session, id
	return nil
}

func (s *MusicService) ResolveLXUserSource(id string, payload json.RawMessage) (json.RawMessage, error) {
	if s == nil { return nil, errors.New("音乐服务不可用") }
	s.lxSessionMu.Lock()
	active := s.lxSession != nil && s.lxSessionID == id
	s.lxSessionMu.Unlock()
	if !active {
		if err := s.SetActiveLXUserSource(id); err != nil { return nil, err }
	}
	s.lxSessionMu.Lock()
	session := s.lxSession
	s.lxSessionMu.Unlock()
	if session == nil { return nil, errors.New("LX 音源运行实例不可用") }

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return session.Request(ctx, payload)
}

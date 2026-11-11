package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"strconv"
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
}

func NewMusicService(manager *config.Manager, value config.MusicConfig, buffers ...*events.Buffer) *MusicService {
	sources := music.NewSourceRegistry()
	_ = sources.Register(music.NewNetEaseSource(nil, ""))
	_ = sources.Register(music.NewLXQQMusicSource(nil, ""))
	_ = sources.Register(music.NewKugouSource(nil, ""))
	_ = sources.Register(music.NewKuwoSource(nil, ""))
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
	response, err := s.search(ctx, keyword)
	if err != nil {
		s.setLastError(fmt.Errorf("搜索弹幕点歌失败：%w", err))
		return
	}
	if len(response.Tracks) == 0 {
		if len(response.Errors) > 0 { s.setLastError(errors.New("所有已启用音源均搜索失败")) }
		return
	}
	var enqueueErr error
	enqueued := false
	for _, track := range response.Tracks {
		source, ok := s.sources.Get(track.SourceID)
		if !ok || (!source.Capabilities().Resolve && !s.hasLXUserSources()) {
			continue
		}
		if _, err := s.Enqueue(track, event.UserName); err == nil {
			enqueued = true
			break
		} else {
			enqueueErr = err
		}
	}
	if !enqueued {
		if enqueueErr == nil {
			enqueueErr = errors.New("搜索结果中没有已实现播放资源解析的音源")
		}
		s.setLastError(fmt.Errorf("加入弹幕点歌队列失败：%w", enqueueErr))
		return
	}
	if s.queue.Snapshot().Current == nil {
		if err := s.playNext(ctx); err != nil { s.setLastError(err) }
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
	value.LegacyDefaultPlayer, value.LegacyPlayers = "", nil
	if err := value.Validate(); err != nil { return err }
	availableEnabled := false
	for _, source := range s.sources.List(value.Sources) {
		availableEnabled = availableEnabled || (source.Available && source.Enabled)
	}
	if !availableEnabled { return errors.New("at least one implemented music source must remain enabled") }
	oldCapacity := s.queue.Snapshot().Capacity
	if err := s.queue.Resize(value.QueueCapacity); err != nil { return err }
	if err := config.SaveTyped(s.manager, "music.json", value, func(candidate config.MusicConfig) error {
		return candidate.Validate()
	}); err != nil {
		_ = s.queue.Resize(oldCapacity)
		return err
	}
	s.mu.Lock()
	wasEnabled := s.value.Enabled
	s.value = cloneMusicConfig(value)
	s.mu.Unlock()
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
	if s == nil { return []music.SourceSnapshot{} }
	s.mu.RLock()
	enabled := cloneBoolMap(s.value.Sources)
	s.mu.RUnlock()
	return s.sources.List(enabled)
}

func (s *MusicService) Search(keyword string) (MusicSearchResponse, error) {
	return s.SearchPage(keyword, 1)
}

func (s *MusicService) SearchPage(keyword string, page int) (MusicSearchResponse, error) {
	return s.searchPage(context.Background(), keyword, page)
}

func (s *MusicService) search(parent context.Context, keyword string) (MusicSearchResponse, error) {
	return s.searchPage(parent, keyword, 1)
}

func (s *MusicService) searchPage(parent context.Context, keyword string, page int) (MusicSearchResponse, error) {
	if s == nil || s.sources == nil { return MusicSearchResponse{}, errors.New("music source registry is unavailable") }
	s.mu.RLock()
	enabled, limit := cloneBoolMap(s.value.Sources), s.value.SearchLimit
	s.mu.RUnlock()
	if page < 1 { page = 1 }
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	tracks, failures, total, maxPage, err := s.sources.SearchPage(ctx, keyword, enabled, limit, page)
	if err != nil { return MusicSearchResponse{}, err }
	return MusicSearchResponse{
		Tracks: tracks, Errors: failures, Page: page, PageSize: limit,
		Total: total, MaxPage: maxPage,
	}, nil
}

func (s *MusicService) Enqueue(track music.Track, requestedBy string) (music.QueueItem, error) {
	if s == nil || s.queue == nil { return music.QueueItem{}, errors.New("music queue is unavailable") }
	s.mu.RLock()
	moduleEnabled := s.value.Enabled
	enabled := s.value.Sources[track.SourceID]
	s.mu.RUnlock()
	if !moduleEnabled { return music.QueueItem{}, errors.New("music module is disabled") }
	if !enabled { return music.QueueItem{}, fmt.Errorf("music source %q is disabled", track.SourceID) }
	_, ok := s.sources.Get(track.SourceID)
	if !ok { return music.QueueItem{}, fmt.Errorf("music source %q is not available", track.SourceID) }
	// Queue insertion is independent from playback URL resolution. Keep search
	// results in the queue even when the selected source needs an LX resolver;
	// PlayNext reports resolution errors and preserves the current item.
	return s.queue.Enqueue(track, requestedBy)
}

func (s *MusicService) GetQueue() music.QueueSnapshot {
	if s == nil || s.queue == nil { return music.QueueSnapshot{Items:[]music.QueueItem{}} }
	return s.queue.Snapshot()
}

func (s *MusicService) PlayNext() error { return s.playNext(context.Background()) }

func (s *MusicService) playNext(parent context.Context) error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	queueSnapshot := s.queue.Snapshot()
	var item music.QueueItem
	if queueSnapshot.Current != nil {
		status, statusErr := s.engine.Snapshot(ctx)
		if statusErr == nil && (status.State == music.PlaybackPlaying || status.State == music.PlaybackPaused || status.State == music.PlaybackLoading) {
			return errors.New("a music track is already playing")
		}
		item = *queueSnapshot.Current
	} else {
		var ok bool
		item, ok = s.queue.TakeNext()
		if !ok { return errors.New("music queue is empty") }
	}
	s.mu.RLock()
	enabled := s.value.Sources[item.Track.SourceID]
	s.mu.RUnlock()
	if !enabled { return fmt.Errorf("music source %q is disabled; the current queue item was kept for retry", item.Track.SourceID) }
	source, ok := s.sources.Get(item.Track.SourceID)
	if !ok { return fmt.Errorf("music source %q is not available; the current queue item was kept for retry", item.Track.SourceID) }
	if validator, ok := s.engine.(music.PlaybackEngineValidator); ok {
		if err := validator.Validate(); err != nil {
			return fmt.Errorf("music playback runtime validation failed: %w", err)
		}
	}
	resource, selectedTrack, err := s.resolveWithFallback(ctx, item.Track, source)
	if err != nil { return fmt.Errorf("resolve %q: %w; the current queue item was kept for retry", item.Track.Title, err) }
	if selectedTrack.SourceID != item.Track.SourceID {
		item.Track = selectedTrack
		if err := s.queue.SetCurrent(item); err != nil { return err }
	}
	if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok {
		if err := lifecycle.Start(ctx); err != nil { return err }
	}
	if err := s.engine.Load(ctx, resource, selectedTrack); err != nil {
		return fmt.Errorf("play %q: %w; the current queue item was kept for retry", selectedTrack.Title, err)
	}
	s.startPlaybackWatcher()
	return nil
}

func (s *MusicService) resolveWithFallback(ctx context.Context, track music.Track, preferred music.Source) (music.PlaybackResource, music.Track, error) {
	var lxErr error
	if s.hasLXUserSources() {
		if resource, err := s.resolveLXTrack(ctx, track); err == nil {
			return resource, track, nil
		} else {
			lxErr = err
		}
	}
	resource, originalErr := preferred.Resolve(ctx, track)
	if originalErr == nil { return resource, track, nil }
	if lxErr != nil { originalErr = errors.Join(originalErr, lxErr) }
	resource, candidate, fallbackErr := s.resolveAlternate(ctx, track)
	if fallbackErr != nil { return music.PlaybackResource{}, track, errors.Join(originalErr, fallbackErr) }
	return resource, candidate, nil
}

func (s *MusicService) resolveAlternate(ctx context.Context, track music.Track) (music.PlaybackResource, music.Track, error) {
	s.mu.RLock()
	enabled := cloneBoolMap(s.value.Sources)
	limit := s.value.SearchLimit
	s.mu.RUnlock()
	keyword := strings.TrimSpace(track.Title + " " + track.Artist)
	candidates, _, searchErr := s.sources.Search(ctx, keyword, enabled, limit)
	if searchErr != nil { return music.PlaybackResource{}, track, searchErr }
	var resolveErrors []error
	for _, candidate := range candidates {
		if candidate.SourceID == track.SourceID || !sameTrack(track, candidate) { continue }
		fallback, ok := s.sources.Get(candidate.SourceID)
		if !ok { continue }
		resource, err := fallback.Resolve(ctx, candidate)
		if err == nil { return resource, candidate, nil }
		resolveErrors = append(resolveErrors, fmt.Errorf("%s: %w", fallback.Name(), err))
	}
	if len(resolveErrors) > 0 { return music.PlaybackResource{}, track, errors.Join(resolveErrors...) }
	return music.PlaybackResource{}, track, errors.New("no matching track found on another enabled source")
}

func sameTrack(left, right music.Track) bool {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		var builder strings.Builder
		for _, char := range value {
			if unicode.IsLetter(char) || unicode.IsNumber(char) { builder.WriteRune(char) }
		}
		return builder.String()
	}
	if normalize(left.Title) == "" || normalize(left.Title) != normalize(right.Title) { return false }
	leftArtist, rightArtist := normalize(left.Artist), normalize(right.Artist)
	if leftArtist != "" && rightArtist != "" && !strings.Contains(leftArtist, rightArtist) && !strings.Contains(rightArtist, leftArtist) { return false }
	if left.DurationMS > 0 && right.DurationMS > 0 {
		difference := left.DurationMS - right.DurationMS
		if difference < 0 { difference = -difference }
		if difference > 10000 { return false }
	}
	return true
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
	if s == nil || s.engine == nil { return nil }
	s.cancelPlaybackWatcher()
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
				if currentItem := s.queue.Snapshot().Current; currentItem != nil {
					fallbackCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					resource, candidate, fallbackErr := s.resolveAlternate(fallbackCtx, currentItem.Track)
					if fallbackErr == nil {
						currentItem.Track = candidate
						if setErr := s.queue.SetCurrent(*currentItem); setErr == nil {
							if loadErr := s.engine.Load(fallbackCtx, resource, candidate); loadErr == nil {
								cancel()
							s.startPlaybackWatcher()
								return
							} else {
								fallbackErr = loadErr
							}
						} else {
							fallbackErr = setErr
						}
					}
					cancel()
					if fallbackErr != nil { message = strings.TrimSpace(message + "；其他音源重试失败：" + fallbackErr.Error()) }
				}
				if message == "" { message = "mpv 加载音频失败；请检查音源返回的播放地址及网络访问" }
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
				if currentItem := s.queue.Snapshot().Current; currentItem != nil {
					fallbackCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
					resource, candidate, fallbackErr := s.resolveAlternate(fallbackCtx, currentItem.Track)
					if fallbackErr == nil {
						currentItem.Track = candidate
						if setErr := s.queue.SetCurrent(*currentItem); setErr == nil {
							if loadErr := s.engine.Load(fallbackCtx, resource, candidate); loadErr == nil {
								cancel()
							s.startPlaybackWatcher()
								return
							}
						}
					}
					cancel()
				}
				if ctx.Err() != nil { return }
				s.setLastError(errors.New("音源未能开始播放；已尝试其他音源，当前歌曲仍保留，可重试或切歌"))
				return
			}
		}
	}
}

func cloneMusicConfig(value config.MusicConfig) config.MusicConfig {
	copy := value
	copy.Sources = cloneBoolMap(value.Sources)
	copy.LegacyPlayers = nil
	copy.LegacyDefaultPlayer = ""
	return copy
}
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

// GetLXUserSources lists imported LX-compatible source scripts. Importing does
// not execute a script; execution is enabled only after the isolated runtime is
// integrated and the source passes its runtime handshake.
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
	return store.Delete(id)
}

func (s *MusicService) UpdateLXUserSource(id string) (music.LXUserSourceMetadata, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXUserSourceMetadata{}, err }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return store.Update(ctx, id)
}

func (s *MusicService) ValidateLXUserSource(id string) (music.LXSourceRuntimeResult, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	script, err := store.ReadScript(id)
	if err != nil { return music.LXSourceRuntimeResult{}, err }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return music.InspectLXUserSource(ctx, script)
}


func (s *MusicService) hasLXUserSources() bool {
	store, err := s.lxUserSourceStore()
	if err != nil { return false }
	sources, err := store.List()
	return err == nil && len(sources) > 0
}

func (s *MusicService) resolveLXTrack(ctx context.Context, track music.Track) (music.PlaybackResource, error) {
	store, err := s.lxUserSourceStore()
	if err != nil { return music.PlaybackResource{}, err }
	sources, err := store.List()
	if err != nil { return music.PlaybackResource{}, err }
	// Search adapters carry their own LX source key. No platform switch lives
	// in the playback service; custom search adapters can set their own key.
	sourceKey := strings.TrimSpace(track.LXSourceKey)
	if sourceKey == "" { sourceKey = strings.TrimSpace(track.SourceID) }
	musicInfo := buildLXMusicInfo(track)
	musicInfo["source"] = sourceKey
	var failures []error
	for _, source := range sources {
		if err := ctx.Err(); err != nil { return music.PlaybackResource{}, err }
		script, readErr := store.ReadScript(source.ID)
		if readErr != nil { failures = append(failures, fmt.Errorf("%s: %w", source.Name, readErr)); continue }
		inspection, inspectErr := music.InspectLXUserSource(ctx, script)
		if inspectErr != nil { failures = append(failures, fmt.Errorf("%s: %w", source.Name, inspectErr)); continue }
		var declared *music.LXDeclaredSource
		for i := range inspection.DeclaredSources {
			if inspection.DeclaredSources[i].Key == sourceKey { declared = &inspection.DeclaredSources[i]; break }
		}
		if declared == nil || !containsString(declared.Actions, "musicUrl") { continue }
		quality := selectLXQuality(declared.Qualitys, sourceKey == "local", track)
		payload, marshalErr := json.Marshal(map[string]any{
			"source": sourceKey, "action": "musicUrl",
			"info": map[string]any{"type": quality, "musicInfo": musicInfo},
		})
		if marshalErr != nil { failures = append(failures, marshalErr); continue }
		result, resolveErr := music.ResolveLXUserSource(ctx, script, payload)
		if resolveErr != nil { failures = append(failures, fmt.Errorf("%s/%s: %w", source.Name, sourceKey, resolveErr)); continue }
		var value any
		if err := json.Unmarshal(result, &value); err != nil { failures = append(failures, fmt.Errorf("%s: 解析结果格式无效：%w", source.Name, err)); continue }
		resource := music.PlaybackResource{}
		switch item := value.(type) {
		case string: resource.URL = item
		case map[string]any:
			resource.URL, _ = item["url"].(string)
			if headers, ok := item["headers"].(map[string]any); ok {
				resource.Headers = make(map[string]string, len(headers))
				for key, header := range headers { resource.Headers[key] = fmt.Sprint(header) }
			}
		default: failures = append(failures, fmt.Errorf("%s: 音源没有返回 URL", source.Name)); continue
		}
		if err := music.ValidateLXPlaybackURL(resource.URL); err != nil { failures = append(failures, fmt.Errorf("%s: %w", source.Name, err)); continue }
		return resource, nil
	}
	if len(failures) == 0 { return music.PlaybackResource{}, fmt.Errorf("没有已导入音源声明 LX 来源 %q 并支持 musicUrl", sourceKey) }
	return music.PlaybackResource{}, fmt.Errorf("没有已导入洛雪音源解析来源 %q：%w", sourceKey, errors.Join(failures...))
}

func containsString(values []string, target string) bool {
	for _, value := range values { if value == target { return true } }
	return false
}

func buildLXMusicInfo(track music.Track) map[string]any {
	seconds := track.DurationMS / 1000
	info := map[string]any{
		"id": track.ID, "mid": track.ID, "songmid": track.ID,
		"hash": track.ID, "rid": track.ID, "MUSICRID": track.ID,
		"name": track.Title, "songname": track.Title, "title": track.Title,
		"artist": track.Artist, "singer": track.Artist, "album": track.Album,
		"interval": formatLXInterval(seconds), "duration": seconds, "source": strings.TrimSpace(track.LXSourceKey),
		"albumName": track.Album, "img": track.CoverURL, "typeUrl": map[string]any{},
	}
	for key, value := range track.SourceData {
		if strings.TrimSpace(key) != "" { info[key] = value }
	}
	if strings.TrimSpace(track.LXSourceKey) == "tx" {
		info["source"] = "tx"
		info["songmid"] = track.ID
		info["songId"] = strings.TrimSpace(track.SourceData["id"])
		info["strMediaMid"] = strings.TrimSpace(track.SourceData["media_mid"])
		info["albumMid"] = strings.TrimSpace(track.SourceData["albummid"])
		info["albumId"] = strings.TrimSpace(track.SourceData["albumid"])
		info["albumName"] = track.Album
		info["img"] = track.CoverURL
		types, available := buildLXQQQualities(track.SourceData)
		info["types"], info["_types"] = types, available
	}
	return info
}

func formatLXInterval(seconds int64) string {
	if seconds <= 0 { return "" }
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func buildLXQQQualities(data map[string]string) ([]any, map[string]any) {
	type qualityField struct { key, name string }
	fields := []qualityField{
		{"size_128mp3", "128k"},
		{"size_320mp3", "320k"},
		{"size_flac", "flac"},
		{"size_hires", "flac24bit"},
	}
	types := make([]any, 0, len(fields))
	available := make(map[string]any, len(fields))
	for _, field := range fields {
		size, err := strconv.ParseInt(strings.TrimSpace(data[field.key]), 10, 64)
		if err != nil || size <= 0 { continue }
		sizeLabel := fmt.Sprintf("%.2fM", float64(size)/(1024*1024))
		types = append(types, map[string]any{"type": field.name, "size": sizeLabel})
		available[field.name] = map[string]any{"size": sizeLabel}
	}
	return types, available
}

func selectLXQuality(qualities []string, local bool, track music.Track) any {
	if local { return nil }
	// QQ search results expose per-track file sizes. LX Music checks
	// meta._qualitys before asking a source for a URL; do the same here so
	// we never request a quality that this particular song does not have.
	if track.LXSourceKey == "tx" {
		_, available := buildLXQQQualities(track.SourceData)
		if len(available) == 0 { return nil }
		for _, preferred := range []string{"320k", "flac24bit", "flac", "128k"} {
			if _, exists := available[preferred]; !exists { continue }
			if containsString(qualities, preferred) { return preferred }
		}
		return nil
	}
	for _, preferred := range []string{"320k", "flac", "flac24bit", "128k"} {
		if containsString(qualities, preferred) { return preferred }
	}
	if len(qualities) > 0 { return qualities[0] }
	return nil
}

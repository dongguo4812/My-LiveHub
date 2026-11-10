package desktop

import (
	"context"
	"errors"
	"fmt"
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
}

func NewMusicService(manager *config.Manager, value config.MusicConfig, buffers ...*events.Buffer) *MusicService {
	sources := music.NewSourceRegistry()
	_ = sources.Register(music.NewNetEaseSource(nil, ""))
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
	prefix := s.value.CommandPrefix
	s.mu.RUnlock()
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
	if _, err := s.Enqueue(response.Tracks[0], event.UserName); err != nil {
		s.setLastError(fmt.Errorf("加入弹幕点歌队列失败：%w", err))
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
	if err := config.SaveTyped(s.manager, "music.json", value, func(candidate config.MusicConfig) error {
		return candidate.Validate()
	}); err != nil { return err }
	s.mu.Lock()
	s.value = cloneMusicConfig(value)
	s.mu.Unlock()
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
	return s.search(context.Background(), keyword)
}

func (s *MusicService) search(parent context.Context, keyword string) (MusicSearchResponse, error) {
	if s == nil || s.sources == nil { return MusicSearchResponse{}, errors.New("music source registry is unavailable") }
	s.mu.RLock()
	enabled, limit := cloneBoolMap(s.value.Sources), s.value.SearchLimit
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(parent, 25*time.Second)
	defer cancel()
	tracks, failures, err := s.sources.Search(ctx, keyword, enabled, limit)
	if err != nil { return MusicSearchResponse{}, err }
	return MusicSearchResponse{Tracks:tracks, Errors:failures}, nil
}

func (s *MusicService) Enqueue(track music.Track, requestedBy string) (music.QueueItem, error) {
	if s == nil || s.queue == nil { return music.QueueItem{}, errors.New("music queue is unavailable") }
	s.mu.RLock()
	enabled := s.value.Sources[track.SourceID]
	s.mu.RUnlock()
	if !enabled { return music.QueueItem{}, fmt.Errorf("music source %q is disabled", track.SourceID) }
	if _, ok := s.sources.Get(track.SourceID); !ok { return music.QueueItem{}, fmt.Errorf("music source %q is not available", track.SourceID) }
	return s.queue.Enqueue(track, requestedBy)
}

func (s *MusicService) GetQueue() music.QueueSnapshot {
	if s == nil || s.queue == nil { return music.QueueSnapshot{Items:[]music.QueueItem{}} }
	return s.queue.Snapshot()
}

func (s *MusicService) PlayNext() error { return s.playNext(context.Background()) }

func (s *MusicService) playNext(parent context.Context) error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	item, ok := s.queue.TakeNext()
	if !ok { return errors.New("music queue is empty") }
	s.mu.RLock()
	enabled := s.value.Sources[item.Track.SourceID]
	s.mu.RUnlock()
	if !enabled { s.queue.FinishCurrent(); return fmt.Errorf("music source %q is disabled", item.Track.SourceID) }
	source, ok := s.sources.Get(item.Track.SourceID)
	if !ok { s.queue.FinishCurrent(); return fmt.Errorf("music source %q is not available", item.Track.SourceID) }
	ctx, cancel := context.WithTimeout(parent, 30*time.Second)
	defer cancel()
	resource, selectedTrack, err := s.resolveWithFallback(ctx, item.Track, source)
	if err != nil { s.queue.FinishCurrent(); return fmt.Errorf("resolve %q: %w", item.Track.Title, err) }
	if selectedTrack.SourceID != item.Track.SourceID {
		item.Track = selectedTrack
		if err := s.queue.SetCurrent(item); err != nil { s.queue.FinishCurrent(); return err }
	}
	if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok {
		if err := lifecycle.Start(ctx); err != nil { s.queue.FinishCurrent(); return err }
	}
	if err := s.engine.Load(ctx, resource, selectedTrack); err != nil {
		s.queue.FinishCurrent()
		return fmt.Errorf("play %q: %w", selectedTrack.Title, err)
	}
	s.startPlaybackWatcher()
	return nil
}

func (s *MusicService) resolveWithFallback(ctx context.Context, track music.Track, preferred music.Source) (music.PlaybackResource, music.Track, error) {
	resource, originalErr := preferred.Resolve(ctx, track)
	if originalErr == nil { return resource, track, nil }

	s.mu.RLock()
	enabled := cloneBoolMap(s.value.Sources)
	limit := s.value.SearchLimit
	s.mu.RUnlock()
	keyword := strings.TrimSpace(track.Title + " " + track.Artist)
	candidates, _, searchErr := s.sources.Search(ctx, keyword, enabled, limit)
	if searchErr != nil { return music.PlaybackResource{}, track, errors.Join(originalErr, searchErr) }
	for _, candidate := range candidates {
		if candidate.SourceID == track.SourceID || !sameTrack(track, candidate) { continue }
		fallback, ok := s.sources.Get(candidate.SourceID)
		if !ok { continue }
		resource, err := fallback.Resolve(ctx, candidate)
		if err == nil { return resource, candidate, nil }
	}
	return music.PlaybackResource{}, track, originalErr
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
	_ = s.engine.Stop(context.Background())
	s.queue.FinishCurrent()
	return s.PlayNext()
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
	sawEndPosition := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := s.engine.Snapshot(ctx)
			if err != nil { continue }
			if snapshot.DurationMS > 0 && snapshot.PositionMS >= snapshot.DurationMS-1000 {
				sawEndPosition = true
			}
			if !sawEndPosition || snapshot.State != music.PlaybackStopped { continue }
			s.mu.RLock()
			current := s.playGeneration == generation
			s.mu.RUnlock()
			if !current { return }
			s.queue.FinishCurrent()
			_ = s.PlayNext()
			return
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

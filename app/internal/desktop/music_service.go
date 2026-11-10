package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

type MusicSearchResponse struct {
	Tracks []music.Track `json:"tracks"`
	Errors map[string]string `json:"errors"`
}

type MusicService struct {
	manager *config.Manager
	mu sync.RWMutex
	value config.MusicConfig
	sources *music.SourceRegistry
	engine *music.MpvEngine
	queue *music.Queue
}

func NewMusicService(manager *config.Manager, value config.MusicConfig) *MusicService {
	sources := music.NewSourceRegistry()
	_ = sources.Register(music.NewNetEaseSource(nil, ""))
	return &MusicService{
		manager: manager,
		value: cloneMusicConfig(value),
		sources: sources,
		engine: music.NewMpvEngine(""),
		queue: music.NewQueue(value.QueueCapacity),
	}
}

func (s *MusicService) GetConfig() config.MusicConfig {
	if s == nil { return config.MusicConfig{} }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMusicConfig(s.value)
}

func (s *MusicService) SaveConfig(value config.MusicConfig) error {
	if s == nil || s.manager == nil { return errors.New("music configuration service is unavailable") }
	value.LegacyDefaultPlayer = ""
	value.LegacyPlayers = nil
	if err := value.Validate(); err != nil { return err }
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
	if s == nil || s.sources == nil { return MusicSearchResponse{}, errors.New("music source registry is unavailable") }
	s.mu.RLock()
	enabled := cloneBoolMap(s.value.Sources)
	limit := s.value.SearchLimit
	s.mu.RUnlock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	tracks, failures, err := s.sources.Search(ctx, keyword, enabled, limit)
	if err != nil { return MusicSearchResponse{}, err }
	return MusicSearchResponse{Tracks:tracks, Errors:failures}, nil
}

func (s *MusicService) Enqueue(track music.Track, requestedBy string) (music.QueueItem, error) {
	if s == nil || s.queue == nil { return music.QueueItem{}, errors.New("music queue is unavailable") }
	if _, ok := s.sources.Get(track.SourceID); !ok { return music.QueueItem{}, fmt.Errorf("music source %q is not available", track.SourceID) }
	return s.queue.Enqueue(track, requestedBy)
}

func (s *MusicService) GetQueue() music.QueueSnapshot {
	if s == nil || s.queue == nil { return music.QueueSnapshot{Items:[]music.QueueItem{}} }
	return s.queue.Snapshot()
}

func (s *MusicService) PlayNext() error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	item, ok := s.queue.TakeNext()
	if !ok { return errors.New("music queue is empty") }
	source, ok := s.sources.Get(item.Track.SourceID)
	if !ok { s.queue.FinishCurrent(); return fmt.Errorf("music source %q is not available", item.Track.SourceID) }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := s.engine.Start(ctx); err != nil { s.queue.FinishCurrent(); return err }
	resource, err := source.Resolve(ctx, item.Track)
	if err != nil { s.queue.FinishCurrent(); return fmt.Errorf("resolve %q from %s: %w", item.Track.Title, source.Name(), err) }
	if err := s.engine.Load(ctx, resource, item.Track); err != nil {
		s.queue.FinishCurrent()
		return fmt.Errorf("play %q: %w", item.Track.Title, err)
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
	return s.engine.SetVolume(context.Background(), volume)
}
func (s *MusicService) Next() error {
	if s == nil || s.engine == nil || s.queue == nil { return errors.New("music playback service is unavailable") }
	_ = s.engine.Stop(context.Background())
	return s.PlayNext()
}
func (s *MusicService) Stop() error {
	if s == nil || s.engine == nil || s.queue == nil { return nil }
	err := s.engine.Stop(context.Background())
	s.queue.FinishCurrent()
	return err
}
func (s *MusicService) GetPlaybackStatus() (music.PlaybackSnapshot, error) {
	if s == nil || s.engine == nil { return music.PlaybackSnapshot{}, music.ErrPlaybackEngineUnavailable }
	return s.engine.Snapshot(context.Background())
}
func (s *MusicService) Shutdown() error {
	if s == nil || s.engine == nil { return nil }
	return s.engine.Close()
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

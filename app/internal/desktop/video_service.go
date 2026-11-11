package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

var videoTrackSequence atomic.Uint64

// VideoService owns the dedicated borderless mpv video process. Music remains
// on its own audio-only process and is resumed only when it was playing before
// the MV started.
type VideoService struct {
	mu sync.Mutex
	engine music.PlaybackEngine
	music *MusicService
	active bool
	resumeMusic bool
	volume int
	watchCancel context.CancelFunc
	generation uint64
}

func NewVideoService(musicService *MusicService, engine music.PlaybackEngine) *VideoService {
	if engine == nil { engine = music.NewMpvVideoEngine("") }
	return &VideoService{music:musicService, engine:engine, volume:80}
}

func (s *VideoService) Play(resourceURL, title string) error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	resourceURL = strings.TrimSpace(resourceURL)
	title = strings.TrimSpace(title)
	parsed, err := url.Parse(resourceURL)
	if err != nil || parsed.Scheme == "" || (parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "file") {
		return errors.New("MV 地址必须是 http、https 或 file URL")
	}
	if title == "" { title = "MV" }

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active {
		s.cancelWatcherLocked()
		if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok {
			if err := lifecycle.Close(); err != nil { return fmt.Errorf("close previous MV window: %w", err) }
		}
		s.active = false
	} else {
		snapshot, snapshotErr := s.musicSnapshot()
		if snapshotErr != nil { return fmt.Errorf("读取音乐播放状态失败：%w", snapshotErr) }
		s.resumeMusic = snapshot.State == music.PlaybackPlaying
		if s.resumeMusic {
			if err := s.music.Pause(); err != nil {
				s.resumeMusic = false
				return fmt.Errorf("MV 开始前暂停音乐失败：%w", err)
			}
		}
	}

	restoreOnFailure := func(cause error) error {
		if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok { _ = lifecycle.Close() }
		s.active = false
		s.restoreMusicLocked()
		return cause
	}
	if validator, ok := s.engine.(music.PlaybackEngineValidator); ok {
		if err := validator.Validate(); err != nil { return restoreOnFailure(fmt.Errorf("MV 播放运行时验证失败：%w", err)) }
	}
	if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok {
		if err := lifecycle.Start(context.Background()); err != nil { return restoreOnFailure(fmt.Errorf("启动 MV 视频窗口失败：%w", err)) }
	}
	track := music.Track{
		ID:fmt.Sprintf("mv-%d", videoTrackSequence.Add(1)),
		SourceID:"direct_video",
		Title:title,
	}
	if err := s.engine.Load(context.Background(), music.PlaybackResource{URL:resourceURL}, track); err != nil {
		return restoreOnFailure(fmt.Errorf("加载 MV 失败：%w", err))
	}
	s.active = true
	s.startWatcherLocked()
	return nil
}

func (s *VideoService) musicSnapshot() (music.PlaybackSnapshot, error) {
	if s.music == nil { return music.PlaybackSnapshot{State:music.PlaybackIdle}, nil }
	return s.music.GetPlaybackStatus()
}

func (s *VideoService) Pause() error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	return s.engine.Pause(context.Background())
}
func (s *VideoService) Resume() error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	return s.engine.Resume(context.Background())
}
func (s *VideoService) Seek(positionMS int64) error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	return s.engine.Seek(context.Background(), positionMS)
}
func (s *VideoService) SetVolume(volume int) error {
	if s == nil || s.engine == nil { return music.ErrPlaybackEngineUnavailable }
	if volume < 0 || volume > 100 { return errors.New("volume must be between 0 and 100") }
	if err := s.engine.SetVolume(context.Background(), volume); err != nil { return err }
	s.mu.Lock()
	s.volume = volume
	s.mu.Unlock()
	return nil
}
func (s *VideoService) GetPlaybackStatus() (music.PlaybackSnapshot, error) {
	if s == nil || s.engine == nil { return music.PlaybackSnapshot{}, music.ErrPlaybackEngineUnavailable }
	return s.engine.Snapshot(context.Background())
}
func (s *VideoService) Stop() error {
	if s == nil { return nil }
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishLocked()
}
func (s *VideoService) finishLocked() error {
	s.cancelWatcherLocked()
	var err error
	if lifecycle, ok := s.engine.(music.PlaybackEngineLifecycle); ok {
		err = lifecycle.Close() // Closing the process also closes the video-only window.
	} else if s.engine != nil {
		err = s.engine.Stop(context.Background())
	}
	s.active = false
	s.restoreMusicLocked()
	return err
}
func (s *VideoService) restoreMusicLocked() {
	shouldResume := s.resumeMusic
	s.resumeMusic = false
	if shouldResume && s.music != nil {
		if err := s.music.Resume(); err != nil {
			// The caller can inspect the music service state and retry manually.
		}
	}
}
func (s *VideoService) startWatcherLocked() {
	ctx, cancel := context.WithCancel(context.Background())
	s.watchCancel = cancel
	s.generation++
	generation := s.generation
	go s.watch(ctx, generation)
}
func (s *VideoService) watch(ctx context.Context, generation uint64) {
	ticker := time.NewTicker(400 * time.Millisecond)
	defer ticker.Stop()
	consecutiveErrors := 0
	for {
		select {
		case <-ctx.Done(): return
		case <-ticker.C:
			snapshot, err := s.engine.Snapshot(ctx)
			if err != nil {
				consecutiveErrors++
				if consecutiveErrors < 5 { continue }
			} else {
				consecutiveErrors = 0
				if snapshot.State != music.PlaybackStopped && snapshot.State != music.PlaybackIdle && snapshot.State != music.PlaybackError { continue }
			}
			s.mu.Lock()
			if s.generation == generation && s.active { _ = s.finishLocked() }
			s.mu.Unlock()
			return
		}
	}
}
func (s *VideoService) cancelWatcherLocked() {
	if s.watchCancel != nil {
		s.watchCancel()
		s.watchCancel = nil
	}
	s.generation++
}
func (s *VideoService) Shutdown() error {
	if s == nil { return nil }
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.finishLocked()
}

package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

// ResolvePlaybackURL is the public MusicService facade over the unified
// source orchestration layer. Track normalization and native metadata mapping
// live in the music package so search, playback and tests share one contract.
func (s *MusicService) ResolvePlaybackURL(track music.Track, quality string) (string, error) {
	if s == nil { return "", errors.New("music service is unavailable") }
	s.mu.RLock()
	runtime := s.scriptRuntime
	searcher := s.searcher
	s.mu.RUnlock()
	if runtime == nil { return "", errors.New("production LX JavaScript runtime is not enabled in this build") }
	ctx, cancel := context.WithTimeout(context.Background(), 105*time.Second)
	defer cancel()
	if err := s.waitForLXRuntime(ctx); err != nil { return "", err }
	return music.NewUnifiedSourceService(searcher, runtime).ResolvePlaybackURL(ctx, track, quality)
}

package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

// ResolvePlaybackResource returns the structured, validated media resource for
// a track. The cache is shared with the production queue path.
func (s *MusicService) ResolvePlaybackResource(track music.Track, quality string) (music.PlaybackResource, error) {
	if s == nil { return music.PlaybackResource{}, errors.New("music service is unavailable") }
	s.mu.RLock()
	runtime, searcher, cache := s.scriptRuntime, s.searcher, s.playbackResourceCache
	s.mu.RUnlock()
	if runtime == nil { return music.PlaybackResource{}, errors.New("production LX JavaScript runtime is not enabled in this build") }
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return music.NewUnifiedSourceService(searcher, runtime).ResolvePlaybackResource(ctx, track, quality, cache)
}

// RefreshPlaybackResource invalidates the failed signed URL and performs one
// bounded re-resolution pass. Playback adapters must enforce their own single
// retry budget and report the final media failure to MusicService.
func (s *MusicService) RefreshPlaybackResource(track music.Track, failed music.PlaybackResource) (music.PlaybackResource, error) {
	if s == nil { return music.PlaybackResource{}, errors.New("music service is unavailable") }
	s.mu.RLock()
	runtime, searcher, cache := s.scriptRuntime, s.searcher, s.playbackResourceCache
	s.mu.RUnlock()
	if runtime == nil { return music.PlaybackResource{}, errors.New("production LX JavaScript runtime is not enabled in this build") }
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return music.NewUnifiedSourceService(searcher, runtime).RefreshPlaybackResource(ctx, track, failed, cache)
}

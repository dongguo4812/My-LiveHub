package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"errors"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

// ResolvePlaybackResource returns the structured, validated media resource for
// a track. The cache is shared with the production queue path.
func (s *MusicService) ResolvePlaybackResource(track music.Track, quality string) (music.PlaybackResource, error) {
	if s == nil { return music.PlaybackResource{}, errors.New("music service is unavailable") }
	s.mu.RLock()
	runtime, cache := s.scriptRuntime, s.playbackResourceCache
	s.mu.RUnlock()
	if runtime == nil { return music.PlaybackResource{}, errors.New("production LX JavaScript runtime is not enabled in this build") }
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return music.NewUnifiedSourceService(scriptAwareSourceSearcher{service: s}, runtime).ResolvePlaybackResource(ctx, track, quality, cache)
}

// RefreshPlaybackResource invalidates the failed signed URL and performs one
// bounded re-resolution pass. Playback adapters must enforce their own single
// retry budget and report the final media failure to MusicService.
func (s *MusicService) RefreshPlaybackResource(track music.Track, failed music.PlaybackResource) (music.PlaybackResource, error) {
	if s == nil { return music.PlaybackResource{}, errors.New("music service is unavailable") }
	s.mu.RLock()
	runtime, cache := s.scriptRuntime, s.playbackResourceCache
	s.mu.RUnlock()
	if runtime == nil { return music.PlaybackResource{}, errors.New("production LX JavaScript runtime is not enabled in this build") }
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	return music.NewUnifiedSourceService(scriptAwareSourceSearcher{service: s}, runtime).RefreshPlaybackResource(ctx, track, failed, cache)
}


// HandlePlaybackResourceFailure is the single-refresh callback for a failed
// media load. Repeated error events for the same failed signed URL reuse the
// same refreshed resource instead of repeatedly invalidating and resolving it.
func (s *MusicService) HandlePlaybackResourceFailure(track music.Track, failed music.PlaybackResource) (music.PlaybackResource, error) {
	if s == nil { return music.PlaybackResource{}, errors.New("music service is unavailable") }
	sourceID := firstNonEmpty(track.LXSourceKey, track.SourceID)
	if sourceID == "" || track.ID == "" || failed.URL == "" || failed.SourceID != sourceID ||
		failed.TrackID != track.ID || strings.TrimSpace(failed.Quality) == "" {
		return music.PlaybackResource{}, errors.New("failed playback resource does not match the requested track")
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{sourceID, track.ID, failed.Quality, failed.URL}, "\x00")))
	key := hex.EncodeToString(sum[:])

	s.playbackRefreshMu.Lock()
	defer s.playbackRefreshMu.Unlock()
	now := time.Now()
	if previous, ok := s.playbackRefreshResults[key]; ok {
		validUntil := previous.ResolvedAt.Add(2 * time.Minute)
		if !previous.ExpiresAt.IsZero() && previous.ExpiresAt.Before(validUntil) { validUntil = previous.ExpiresAt }
		if now.Before(validUntil) { return previous, nil }
		delete(s.playbackRefreshResults, key)
	}
	refreshed, err := s.RefreshPlaybackResource(track, failed)
	if err != nil { return music.PlaybackResource{}, fmt.Errorf("refresh failed playback resource: %w", err) }
	if s.playbackRefreshResults == nil { s.playbackRefreshResults = make(map[string]music.PlaybackResource) }
	if len(s.playbackRefreshResults) >= 512 { s.playbackRefreshResults = make(map[string]music.PlaybackResource) }
	s.playbackRefreshResults[key] = refreshed
	return refreshed, nil
}

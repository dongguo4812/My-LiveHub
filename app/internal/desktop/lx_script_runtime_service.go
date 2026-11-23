package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/music"
)

// ResolvePlaybackURL runs the active LX source script through the owned
// QuickJS runtime. The external 洛雪 URI playback path remains unchanged;
// callers that need the concrete CDN URL can use this production resolver.
func (s *MusicService) ResolvePlaybackURL(ctx context.Context, track music.Track, quality string) (string, error) {
	if s == nil {
		return "", errors.New("music service is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	s.mu.RLock()
	runtime := s.scriptRuntime
	s.mu.RUnlock()
	if runtime == nil {
		return "", errors.New("production LX JavaScript runtime is not enabled in this build")
	}
	source := firstNonEmpty(track.LXSourceKey, track.SourceID)
	if strings.TrimSpace(source) == "" {
		return "", errors.New("track has no LX source identifier")
	}
	songID := firstNonEmpty(
		track.SourceData["songmid"],
		track.SourceData["song_id"],
		track.SourceData["hash"],
		track.SourceData["id"],
	)
	if songID == "" {
		songID = strings.TrimPrefix(track.ID, source+"_")
	}
	if songID == "" {
		return "", errors.New("track has no native source song identifier")
	}
	types := make([]map[string]any, 0, len(track.Qualities))
	for _, item := range track.Qualities {
		entry := map[string]any{"type": item.Type, "size": item.Size}
		for key, value := range item.SourceData {
			entry[key] = value
		}
		types = append(types, entry)
	}
	if quality == "" && len(track.Qualities) > 0 {
		quality = track.Qualities[0].Type
	}
	if quality == "" {
		quality = "320k"
	}
	info := map[string]any{
		"musicInfo": map[string]any{
			"songmid": songID,
			"name": track.Title,
			"singer": track.Artist,
			"albumName": track.Album,
			"albumId": firstNonEmpty(track.SourceData["album_id"], track.SourceData["albumId"]),
			"hash": track.SourceData["hash"],
			"strMediaMid": firstNonEmpty(track.SourceData["media_mid"], track.SourceData["strMediaMid"]),
			"albumMid": firstNonEmpty(track.SourceData["album_mid"], track.SourceData["albumMid"]),
			"copyrightId": firstNonEmpty(track.SourceData["copyright_id"], track.SourceData["copyrightId"]),
			"lrcUrl": firstNonEmpty(track.SourceData["lrc_url"], track.SourceData["lrcUrl"]),
			"types": types,
		},
		"type": quality,
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	resolvedURL, err := runtime.ResolvePlaybackURL(ctx, source, info)
	if err != nil {
		return "", fmt.Errorf("resolve %s track %q: %w", source, track.Title, err)
	}
	parsed, parseErr := url.Parse(strings.TrimSpace(resolvedURL))
	if parseErr != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", fmt.Errorf("LX source returned an invalid HTTP(S) playback URL")
	}
	return resolvedURL, nil
}

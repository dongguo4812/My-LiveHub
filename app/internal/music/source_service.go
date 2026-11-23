package music

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

type SourceSearcher interface {
	Sources() []SourceSnapshot
	SearchSources(context.Context, string, int, []string) (SearchPage, map[string]string, error)
	SearchProvider(context.Context, string, string, int) (SearchPage, error)
}

type PlaybackURLResolver interface {
	ResolvePlaybackURL(context.Context, string, any) (string, error)
}

// UnifiedSourceService is the single orchestration boundary for normalized
// search results and native playback URL resolution. Provider-specific IDs
// and metadata remain on Track so no caller needs platform-specific structs.
type UnifiedSourceService struct {
	searcher SourceSearcher
	resolver PlaybackURLResolver
}

func NewUnifiedSourceService(searcher SourceSearcher, resolver PlaybackURLResolver) *UnifiedSourceService {
	return &UnifiedSourceService{searcher: searcher, resolver: resolver}
}

func (s *UnifiedSourceService) Sources() []SourceSnapshot {
	if s == nil || s.searcher == nil { return []SourceSnapshot{} }
	return s.searcher.Sources()
}

func (s *UnifiedSourceService) Search(ctx context.Context, keyword string, page int, sourceIDs []string) (SearchPage, map[string]string, error) {
	if s == nil || s.searcher == nil { return SearchPage{}, nil, errors.New("music source search is unavailable") }
	return s.searcher.SearchSources(ctx, keyword, page, sourceIDs)
}

func (s *UnifiedSourceService) SearchSource(ctx context.Context, source, keyword string, page int) (SearchPage, error) {
	if s == nil || s.searcher == nil { return SearchPage{}, errors.New("music source search is unavailable") }
	return s.searcher.SearchProvider(ctx, source, keyword, page)
}

func (s *UnifiedSourceService) ResolvePlaybackURL(ctx context.Context, track Track, qualityType string) (string, error) {
	if s == nil || s.resolver == nil { return "", errors.New("music playback URL resolver is unavailable") }
	if ctx == nil { ctx = context.Background() }
	source := strings.TrimSpace(firstTrackValue(track.LXSourceKey, track.SourceID))
	if source == "" { return "", errors.New("track has no source identifier") }
	songID := firstTrackValue(
		track.SourceData["songmid"], track.SourceData["song_id"],
		track.SourceData["hash"], track.SourceData["id"],
	)
	if songID == "" { songID = strings.TrimPrefix(track.ID, source+"_") }
	if songID == "" || songID == track.ID { return "", errors.New("track has no native source song identifier") }

	types := make([]map[string]any, 0, len(track.Qualities))
	for _, item := range track.Qualities {
		if strings.TrimSpace(item.Type) == "" { continue }
		entry := map[string]any{"type": item.Type, "size": item.Size}
		for key, value := range item.SourceData { entry[key] = value }
		types = append(types, entry)
	}
	if qualityType == "" && len(track.Qualities) > 0 { qualityType = track.Qualities[0].Type }
	if qualityType == "" { qualityType = "320k" }
	info := map[string]any{
		"musicInfo": map[string]any{
			"songmid": songID, "name": track.Title, "singer": track.Artist,
			"albumName": track.Album,
			"albumId": firstTrackValue(track.SourceData["album_id"], track.SourceData["albumId"]),
			"hash": track.SourceData["hash"],
			"strMediaMid": firstTrackValue(track.SourceData["media_mid"], track.SourceData["strMediaMid"]),
			"albumMid": firstTrackValue(track.SourceData["album_mid"], track.SourceData["albumMid"]),
			"copyrightId": firstTrackValue(track.SourceData["copyright_id"], track.SourceData["copyrightId"]),
			"lrcUrl": firstTrackValue(track.SourceData["lrc_url"], track.SourceData["lrcUrl"]),
			"types": types,
		},
		"type": qualityType,
	}
	resolved, err := s.resolver.ResolvePlaybackURL(ctx, source, info)
	if err != nil { return "", fmt.Errorf("resolve %s track %q: %w", source, track.Title, err) }
	resolved = strings.TrimSpace(resolved)
	parsed, err := url.Parse(resolved)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("source returned an invalid HTTP(S) playback URL")
	}
	return resolved, nil
}

func firstTrackValue(values ...string) string {
	for _, value := range values { if strings.TrimSpace(value) != "" { return strings.TrimSpace(value) } }
	return ""
}

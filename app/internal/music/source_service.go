package music

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"strconv"
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
	if s == nil || s.searcher == nil {
		return []SourceSnapshot{}
	}
	return s.searcher.Sources()
}

func (s *UnifiedSourceService) Search(ctx context.Context, keyword string, page int, sourceIDs []string) (SearchPage, map[string]string, error) {
	if s == nil || s.searcher == nil {
		return SearchPage{}, nil, errors.New("music source search is unavailable")
	}
	return s.searcher.SearchSources(ctx, keyword, page, sourceIDs)
}

func (s *UnifiedSourceService) SearchSource(ctx context.Context, source, keyword string, page int) (SearchPage, error) {
	if s == nil || s.searcher == nil {
		return SearchPage{}, errors.New("music source search is unavailable")
	}
	return s.searcher.SearchProvider(ctx, source, keyword, page)
}

func (s *UnifiedSourceService) ResolvePlaybackURL(ctx context.Context, track Track, qualityType string) (string, error) {
	if s == nil || s.resolver == nil { return "", errors.New("music playback URL resolver is unavailable") }
	if ctx == nil { ctx = context.Background() }
	source := strings.TrimSpace(firstTrackValue(track.LXSourceKey, track.SourceID))
	if source == "" { return "", errors.New("track has no source identifier") }
	songID := firstTrackValue(track.SourceData["songmid"], track.SourceData["song_id"], track.SourceData["hash"], track.SourceData["id"])
	if songID == "" {
		if source == "kg" {
			songID = strings.TrimSpace(track.ID)
			if separator := strings.LastIndex(songID, "_"); separator >= 0 { songID = songID[separator+1:] }
		} else {
			songID = strings.TrimPrefix(track.ID, source+"_")
			if songID == track.ID { songID = "" }
		}
	}
	if songID == "" { return "", errors.New("track has no native source song identifier") }

	// LX's custom-source musicUrl boundary receives toOldMusicInfo's flattened
	// object, not the renderer's nested MusicInfo model. Keep both the ordered
	// types array and the keyed _types index; scripts use either representation.
	types := make([]map[string]any, 0, len(track.Qualities))
	_types := make(map[string]any, len(track.Qualities))
	for _, item := range track.Qualities {
		kind := strings.TrimSpace(item.Type)
		if kind == "" { continue }
		entry := map[string]any{"type": kind, "size": item.Size}
		indexed := map[string]any{"size": item.Size}
		for key, value := range item.SourceData { entry[key] = value; indexed[key] = value }
		types = append(types, entry)
		_types[kind] = indexed
	}
	if qualityType == "" { qualityType = strings.TrimSpace(track.SelectedQuality) }
	if qualityType == "" && len(types) > 0 { qualityType, _ = types[0]["type"].(string) }
	if qualityType == "" { qualityType = "320k" }

	interval := ""
	if track.DurationMS > 0 {
		seconds := track.DurationMS / 1000
		interval = fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
	}
	info := map[string]any{
		"songmid": songID,
		"name": track.Title,
		"singer": track.Artist,
		"albumName": track.Album,
		"albumId": firstTrackValue(track.SourceData["album_id"], track.SourceData["albumId"]),
		"hash": track.SourceData["hash"],
		"strMediaMid": firstTrackValue(track.SourceData["media_mid"], track.SourceData["strMediaMid"]),
		"albumMid": firstTrackValue(track.SourceData["album_mid"], track.SourceData["albumMid"]),
		"copyrightId": firstTrackValue(track.SourceData["copyright_id"], track.SourceData["copyrightId"]),
		"lrcUrl": firstTrackValue(track.SourceData["lrc_url"], track.SourceData["lrcUrl"]),
		"interval": interval,
		"img": track.CoverURL,
		"types": types,
		"_types": _types,
	}
	if value := track.SourceData["song_id"]; value != "" {
		if numeric, err := strconv.ParseInt(value, 10, 64); err == nil { info["songId"] = numeric } else { info["songId"] = value }
	}
	resolved, err := s.resolver.ResolvePlaybackURL(ctx, source, map[string]any{"musicInfo": info, "type": qualityType})
	if err != nil { return "", fmt.Errorf("resolve %s track %q: %w", source, track.Title, err) }
	resolved = strings.TrimSpace(resolved)
	parsed, err := url.Parse(resolved)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", errors.New("source returned an invalid HTTP(S) playback URL")
	}
	return resolved, nil
}

func firstTrackValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

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
	if s == nil || s.resolver == nil {
		return "", errors.New("music playback URL resolver is unavailable")
	}
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

	// LX musicUrl receives the desktop MusicInfo shape:
	// { musicInfo: { id, name, singer, source, interval, meta: { songId,
	// albumName, qualitys, _qualitys, ... } }, type }. The previous flattened
	// songmid/types object was not the native LX contract and could make scripts
	// request the wrong catalog item or report a misleading copyright error.
	qualitys := make([]map[string]any, 0, len(track.Qualities))
	_qualitys := make(map[string]any, len(track.Qualities))
	for _, item := range track.Qualities {
		kind := strings.TrimSpace(item.Type)
		if kind == "" { continue }
		entry := map[string]any{"type": kind, "size": item.Size}
		for key, value := range item.SourceData { entry[key] = value }
		qualitys = append(qualitys, entry)
		indexed := map[string]any{"size": item.Size}
		for key, value := range item.SourceData { indexed[key] = value }
		_qualitys[kind] = indexed
	}
	if qualityType == "" { qualityType = strings.TrimSpace(track.SelectedQuality) }
	if qualityType == "" && len(qualitys) > 0 { qualityType, _ = qualitys[0]["type"].(string) }
	if qualityType == "" { qualityType = "320k" }

	interval := ""
	if track.DurationMS > 0 {
		seconds := track.DurationMS / 1000
		interval = fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
	}
	meta := map[string]any{
		"songId": songID,
		"albumName": track.Album,
		"picUrl": track.CoverURL,
		"qualitys": qualitys,
		"_qualitys": _qualitys,
	}
	if value := firstTrackValue(track.SourceData["album_id"], track.SourceData["albumId"]); value != "" { meta["albumId"] = value }
	switch source {
	case "kg":
		if value := track.SourceData["hash"]; value != "" { meta["hash"] = value }
		if value := firstTrackValue(track.SourceData["album_audio_id"], track.SourceData["albumAudioId"]); value != "" { meta["albumAudioId"] = value }
	case "tx":
		if value := firstTrackValue(track.SourceData["media_mid"], track.SourceData["strMediaMid"]); value != "" { meta["strMediaMid"] = value }
		if value := track.SourceData["song_id"]; value != "" { meta["id"] = value }
		if value := firstTrackValue(track.SourceData["album_mid"], track.SourceData["albumMid"]); value != "" { meta["albumMid"] = value }
	case "mg":
		if value := firstTrackValue(track.SourceData["copyright_id"], track.SourceData["copyrightId"]); value != "" { meta["copyrightId"] = value }
		if value := firstTrackValue(track.SourceData["lrc_url"], track.SourceData["lrcUrl"]); value != "" { meta["lrcUrl"] = value }
		if value := firstTrackValue(track.SourceData["mrc_url"], track.SourceData["mrcUrl"]); value != "" { meta["mrcUrl"] = value }
		if value := firstTrackValue(track.SourceData["trc_url"], track.SourceData["trcUrl"]); value != "" { meta["trcUrl"] = value }
	}
	musicInfo := map[string]any{
		"id": track.ID, "name": track.Title, "singer": track.Artist,
		"source": source, "interval": interval, "meta": meta,
	}
	info := map[string]any{"musicInfo": musicInfo, "type": qualityType}
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
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

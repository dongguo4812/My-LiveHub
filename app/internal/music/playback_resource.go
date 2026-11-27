package music

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

// PlaybackResource is the validated media request contract passed from the
// source resolver to a playback engine. Script API request headers are not
// implicitly copied here; only media headers explicitly supplied by a source
// adapter may be populated.
type PlaybackResource struct {
	URL          string            `json:"url"`
	SourceID     string            `json:"source_id"`
	TrackID      string            `json:"track_id"`
	Quality      string            `json:"quality"`
	Headers      map[string]string `json:"headers,omitempty"`
	MIMEType     string            `json:"mime_type,omitempty"`
	Codec        string            `json:"codec,omitempty"`
	ExpiresAt    time.Time         `json:"expires_at,omitempty"`
	Refreshable  bool              `json:"refreshable"`
	RequiresProxy bool             `json:"requires_proxy"`
	ResolvedAt   time.Time         `json:"resolved_at"`
}

// PlaybackResourceCache is a bounded, concurrency-safe in-memory cache.
// Entries with a source expiry are never served past that expiry.
type PlaybackResourceCache struct {
	mu sync.Mutex
	entries map[string]playbackResourceEntry
	ttl time.Duration
	maxEntries int
	now func() time.Time
}

type playbackResourceEntry struct {
	resource PlaybackResource
	expiresAt time.Time
}

func NewPlaybackResourceCache(ttl time.Duration, maxEntries int) *PlaybackResourceCache {
	if ttl <= 0 { ttl = 2 * time.Minute }
	if maxEntries <= 0 { maxEntries = 512 }
	return &PlaybackResourceCache{entries: make(map[string]playbackResourceEntry), ttl: ttl, maxEntries: maxEntries, now: time.Now}
}

func playbackResourceCacheKey(track Track, quality string) string {
	return strings.Join([]string{firstTrackValue(track.LXSourceKey, track.SourceID), track.ID, quality}, "\x00")
}

func (c *PlaybackResourceCache) Get(track Track, quality string) (PlaybackResource, bool) {
	if c == nil { return PlaybackResource{}, false }
	key := playbackResourceCacheKey(track, quality)
	now := c.now()
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok { return PlaybackResource{}, false }
	if !now.Before(entry.expiresAt) || (!entry.resource.ExpiresAt.IsZero() && !now.Before(entry.resource.ExpiresAt)) {
		delete(c.entries, key)
		return PlaybackResource{}, false
	}
	return clonePlaybackResource(entry.resource), true
}

func (c *PlaybackResourceCache) Put(track Track, quality string, resource PlaybackResource) {
	if c == nil { return }
	if err := validatePlaybackResource(resource); err != nil { return }
	if resource.SourceID != firstTrackValue(track.LXSourceKey, track.SourceID) ||
		resource.TrackID != track.ID || resource.Quality != strings.TrimSpace(quality) {
		return
	}
	now := c.now()
	resource.ResolvedAt = now
	expires := now.Add(c.ttl)
	if !resource.ExpiresAt.IsZero() && resource.ExpiresAt.Before(expires) { expires = resource.ExpiresAt }
	if !expires.After(now) { return }
	key := playbackResourceCacheKey(track, quality)
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists && len(c.entries) >= c.maxEntries {
		// Deterministically evict the entry with the earliest expiry.
		oldestKey := ""
		var oldest time.Time
		for candidate, entry := range c.entries {
			if oldestKey == "" || entry.expiresAt.Before(oldest) || (entry.expiresAt.Equal(oldest) && candidate < oldestKey) { oldestKey, oldest = candidate, entry.expiresAt }
		}
		delete(c.entries, oldestKey)
	}
	c.entries[key] = playbackResourceEntry{resource: clonePlaybackResource(resource), expiresAt: expires}
}

func (c *PlaybackResourceCache) Invalidate(track Track, quality string) {
	if c == nil { return }
	c.mu.Lock()
	delete(c.entries, playbackResourceCacheKey(track, quality))
	c.mu.Unlock()
}

func (c *PlaybackResourceCache) Clear() {
	if c == nil { return }
	c.mu.Lock()
	c.entries = make(map[string]playbackResourceEntry)
	c.mu.Unlock()
}

func clonePlaybackResource(resource PlaybackResource) PlaybackResource {
	if resource.Headers != nil {
		headers := make(map[string]string, len(resource.Headers))
		for key, value := range resource.Headers { headers[key] = value }
		resource.Headers = headers
	}
	return resource
}

func validatePlaybackResource(resource PlaybackResource) error {
	parsed, err := url.Parse(strings.TrimSpace(resource.URL))
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("playback resource must contain a valid credential-free HTTP(S) URL without a fragment")
	}
	if strings.TrimSpace(resource.SourceID) == "" || strings.TrimSpace(resource.TrackID) == "" {
		return errors.New("playback resource source and track identifiers are required")
	}
	if len(resource.Headers) > 100 {
		return errors.New("playback resource contains too many media headers")
	}
	headerBytes := 0
	for key, value := range resource.Headers {
		if strings.TrimSpace(key) == "" || strings.ContainsAny(key+value, "\r\n") {
			return fmt.Errorf("playback resource contains an invalid media header")
		}
		for _, r := range key {
			if !isHTTPTokenRune(r) { return errors.New("playback resource contains an invalid media header name") }
		}
		for _, r := range value {
			if (r < 0x20 && r != '\t') || r == 0x7f { return errors.New("playback resource contains an invalid media header value") }
		}
		headerBytes += len(key) + len(value)
		if headerBytes > 64<<10 { return errors.New("playback resource media headers exceed 64 KiB") }
	}
	return nil
}

// playbackQualityRank follows Xinghai's mapQuality fallback order rather than
// assuming that every source-specific label is a simple bitrate.
func playbackQualityRank(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "jymaster": return 100
	case "sky": return 95
	case "jyeffect": return 90
	case "hires", "hi-res": return 85
	case "flac24bit": return 80
	case "spatial": return 75
	case "master", "atmos", "atmos_plus": return 70
	case "flac": return 60
	case "320k": return 50
	case "192k": return 40
	case "128k": return 30
	default: return 0
	}
}
func BestAvailablePlaybackQuality(qualities []TrackQuality) string {
	best, bestRank := "", -1
	for _, item := range qualities {
		kind := strings.TrimSpace(item.Type)
		if kind == "" { continue }
		if rank := playbackQualityRank(kind); rank > bestRank { best, bestRank = kind, rank }
	}
	return best
}

// detectPlaybackURLQuality recognizes only explicit provider URL encodings. It
// deliberately returns unknown for opaque URLs rather than guessing from a label.
func detectPlaybackURLQuality(source, rawURL string) string {
	if strings.TrimSpace(source) != "mg" { return "" }
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil { return "" }
	path, err := url.PathUnescape(parsed.EscapedPath())
	if err != nil { path = parsed.Path }
	path = strings.ToUpper(path)
	switch {
	case strings.Contains(path, "MP3_128_"): return "128k"
	case strings.Contains(path, "MP3_192_"): return "192k"
	case strings.Contains(path, "MP3_320_"): return "320k"
	case strings.Contains(path, "FLAC"): return "flac"
	default: return ""
	}
}

// ResolvePlaybackResource resolves quality candidates in deterministic order.
// Explicitly selected quality is attempted first; automatic candidates are ranked
// by known fidelity tier, with stable source order for equal or unknown labels.
func (s *UnifiedSourceService) ResolvePlaybackResource(ctx context.Context, track Track, requestedQuality string, cache *PlaybackResourceCache) (PlaybackResource, error) {
	if s == nil || s.resolver == nil { return PlaybackResource{}, errors.New("music playback URL resolver is unavailable") }
	if ctx == nil { ctx = context.Background() }
	qualities := make([]string, 0, len(track.Qualities)+1)
	seen := make(map[string]bool)
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value != "" && !seen[value] { seen[value] = true; qualities = append(qualities, value) }
	}
	// Explicit user choices always win. Only automatic candidates are ranked.
	add(requestedQuality)
	add(track.SelectedQuality)
	automatic := append([]TrackQuality(nil), track.Qualities...)
	sort.SliceStable(automatic, func(i, j int) bool {
		return playbackQualityRank(automatic[i].Type) > playbackQualityRank(automatic[j].Type)
	})
	for _, item := range automatic { add(item.Type) }
	if len(qualities) == 0 { add("320k") }

	failures := make([]string, 0, len(qualities))
	for _, quality := range qualities {
		if err := ctx.Err(); err != nil { return PlaybackResource{}, err }
		if cached, ok := cache.Get(track, quality); ok { return cached, nil }
		resolved, err := s.ResolvePlaybackURL(ctx, track, quality)
		if err != nil {
			cache.Invalidate(track, quality)
			failures = append(failures, quality+": "+err.Error())
			continue
		}
		if actual := detectPlaybackURLQuality(firstTrackValue(track.LXSourceKey, track.SourceID), resolved); actual != "" &&
			playbackQualityRank(actual) < playbackQualityRank(quality) {
			failures = append(failures, fmt.Sprintf("%s: media URL identifies %s, not the requested quality", quality, actual))
			continue
		}
		resource := PlaybackResource{
			URL: resolved, SourceID: firstTrackValue(track.LXSourceKey, track.SourceID),
			TrackID: track.ID, Quality: quality, Refreshable: true, ResolvedAt: time.Now(),
		}
		if err := validatePlaybackResource(resource); err != nil {
			failures = append(failures, quality+": "+err.Error())
			continue
		}
		cache.Put(track, quality, resource)
		return resource, nil
	}
	return PlaybackResource{}, fmt.Errorf("all playback qualities failed: %s", strings.Join(failures, "; "))
}

// RefreshPlaybackResource invalidates the failed URL before performing one
// fresh resolution pass. Callers own the retry budget; this method never loops.
func (s *UnifiedSourceService) RefreshPlaybackResource(ctx context.Context, track Track, failed PlaybackResource, cache *PlaybackResourceCache) (PlaybackResource, error) {
	if cache != nil { cache.Invalidate(track, failed.Quality) }
	return s.ResolvePlaybackResource(ctx, track, failed.Quality, cache)
}

func isHTTPTokenRune(r rune) bool {
	if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' { return true }
	switch r {
	case '!', '#', '$', '%', '&', 39, '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	default:
		return false
	}
}

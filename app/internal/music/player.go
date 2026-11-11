package music

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

type SourceCapabilities struct {
	Search  bool `json:"search"`
	Resolve bool `json:"resolve"`
}

type Track struct {
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	Title      string `json:"title"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	CoverURL   string `json:"cover_url,omitempty"`
}

type PlaybackResource struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	MIMEType string           `json:"mime_type,omitempty"`
}

type Source interface {
	ID() string
	Name() string
	Capabilities() SourceCapabilities
	Search(context.Context, string, int) ([]Track, error)
	Resolve(context.Context, Track) (PlaybackResource, error)
}

type SourceSearchPage struct {
	Tracks []Track
	Total int64
	MaxPage int
	PageSize int
}

// PagedSource mirrors the per-source pagination contract used by LX Music.
// Search remains available for older adapters while paged adapters implement
// this optional interface.
type PagedSource interface {
	SearchPage(context.Context, string, int, int) (SourceSearchPage, error)
}

type SourceSnapshot struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Capabilities SourceCapabilities `json:"capabilities"`
	Enabled      bool               `json:"enabled"`
	Available    bool               `json:"available"`
	Error        string             `json:"error,omitempty"`
}

type SourceRegistry struct {
	mu      sync.RWMutex
	sources map[string]Source
}

func NewSourceRegistry() *SourceRegistry {
	return &SourceRegistry{sources: make(map[string]Source)}
}

func (r *SourceRegistry) Register(source Source) error {
	if r == nil {
		return errors.New("music source registry is required")
	}
	if source == nil {
		return errors.New("music source is required")
	}
	id := strings.TrimSpace(source.ID())
	if id == "" || strings.TrimSpace(source.Name()) == "" {
		return errors.New("music source ID and name are required")
	}
	capabilities := source.Capabilities()
	if !capabilities.Search {
		return fmt.Errorf("music source %q must support search", id)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.sources[id]; exists {
		return fmt.Errorf("music source %q is already registered", id)
	}
	r.sources[id] = source
	return nil
}

func (r *SourceRegistry) Get(id string) (Source, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	source, ok := r.sources[strings.TrimSpace(id)]
	return source, ok
}

func (r *SourceRegistry) List(enabled map[string]bool) []SourceSnapshot {
	if r == nil {
		return []SourceSnapshot{}
	}
	r.mu.RLock()
	sources := make([]Source, 0, len(r.sources))
	for _, source := range r.sources {
		sources = append(sources, source)
	}
	r.mu.RUnlock()

	result := make([]SourceSnapshot, 0, len(sources))
	for _, source := range sources {
		result = append(result, SourceSnapshot{
			ID: source.ID(), Name: source.Name(),
			Capabilities: source.Capabilities(),
			Enabled: enabled[source.ID()], Available: true,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

// Search queries all registered and enabled sources concurrently. Results are
// grouped deterministically by source ID; one failing source does not suppress
// successful results from other sources.
func (r *SourceRegistry) Search(ctx context.Context, keyword string, enabled map[string]bool, limit int) ([]Track, map[string]string, error) {
	if r == nil {
		return nil, nil, errors.New("music source registry is unavailable")
	}
	keyword, err := normalizeSearchKeyword(keyword)
	if err != nil {
		return nil, nil, err
	}
	limit = normalizeSearchLimit(limit)

	r.mu.RLock()
	selected := make([]Source, 0, len(r.sources))
	for id, source := range r.sources {
		if enabled[id] {
			selected = append(selected, source)
		}
	}
	r.mu.RUnlock()
	sort.Slice(selected, func(i, j int) bool { return selected[i].ID() < selected[j].ID() })
	if len(selected) == 0 {
		return nil, nil, errors.New("no enabled and available music sources")
	}

	type sourceResult struct {
		id string
		tracks []Track
		err error
	}
	results := make(chan sourceResult, len(selected))
	var wg sync.WaitGroup
	for _, source := range selected {
		source := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := sourceResult{id: source.ID()}
			defer func() {
				if recovered := recover(); recovered != nil {
					result.err = fmt.Errorf("music source panic: %v", recovered)
				}
				results <- result
			}()
			result.tracks, result.err = source.Search(ctx, keyword, limit)
		}()
	}
	wg.Wait()
	close(results)

	resultByID := make(map[string]sourceResult, len(selected))
	for result := range results { resultByID[result.id] = result }
	tracks := make([]Track, 0, len(selected)*limit)
	sourceErrors := make(map[string]string)
	for _, source := range selected {
		result := resultByID[source.ID()]
		if result.err != nil {
			sourceErrors[result.id] = result.err.Error()
			continue
		}
		if len(result.tracks) > limit { result.tracks = result.tracks[:limit] }
		for _, track := range result.tracks {
			if track.SourceID == "" { track.SourceID = result.id }
			if track.SourceID != result.id {
				sourceErrors[result.id] = "source returned a track owned by another source"
				continue
			}
			if err := validateTrack(track); err != nil {
				sourceErrors[result.id] = err.Error()
				continue
			}
			tracks = append(tracks, track)
		}
	}
	return tracks, sourceErrors, nil
}

func validateTrack(track Track) error {
	if strings.TrimSpace(track.ID) == "" {
		return errors.New("track ID is required")
	}
	if strings.TrimSpace(track.SourceID) == "" {
		return errors.New("track source ID is required")
	}
	if strings.TrimSpace(track.Title) == "" {
		return errors.New("track title is required")
	}
	if track.DurationMS < 0 {
		return errors.New("track duration cannot be negative")
	}
	return nil
}

func normalizeSearchLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	if limit > 50 {
		return 50
	}
	return limit
}

func normalizeSearchKeyword(keyword string) (string, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return "", errors.New("search keyword is required")
	}
	return keyword, nil
}

type aggregatedSourcePage struct {
	id string
	page SourceSearchPage
	err error
}

// SearchPage requests the same page from every enabled source concurrently.
// The aggregate total and page count intentionally use the maximum reported
// by an individual source, matching LX Music's "all" search behavior rather
// than adding totals from different catalogs.
func (r *SourceRegistry) SearchPage(ctx context.Context, keyword string, enabled map[string]bool, limit, page int) ([]Track, map[string]string, int64, int, error) {
	if r == nil {
		return nil, nil, 0, 0, errors.New("music source registry is unavailable")
	}
	keyword, err := normalizeSearchKeyword(keyword)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	limit = normalizeSearchLimit(limit)
	if page < 1 {
		page = 1
	}

	r.mu.RLock()
	selected := make([]Source, 0, len(r.sources))
	for id, source := range r.sources {
		if enabled[id] && source.Capabilities().Search {
			selected = append(selected, source)
		}
	}
	r.mu.RUnlock()
	sort.SliceStable(selected, func(i, j int) bool {
		left := selected[i].Capabilities().Resolve
		right := selected[j].Capabilities().Resolve
		if left != right {
			return left
		}
		return selected[i].ID() < selected[j].ID()
	})
	if len(selected) == 0 {
		return nil, nil, 0, 0, errors.New("no enabled and available music sources")
	}

	results := make(chan aggregatedSourcePage, len(selected))
	var wg sync.WaitGroup
	for _, source := range selected {
		source := source
		wg.Add(1)
		go func() {
			defer wg.Done()
			result := aggregatedSourcePage{id: source.ID(), page: SourceSearchPage{PageSize: limit, MaxPage: 1}}
			defer func() {
				if recovered := recover(); recovered != nil {
					result.err = fmt.Errorf("music source panic: %v", recovered)
				}
				results <- result
			}()
			if paged, ok := source.(PagedSource); ok {
				result.page, result.err = paged.SearchPage(ctx, keyword, page, limit)
				return
			}
			if page > 1 {
				return
			}
			result.page.Tracks, result.err = source.Search(ctx, keyword, limit)
			result.page.Total = int64(len(result.page.Tracks))
			result.page.MaxPage = 1
			result.page.PageSize = limit
		}()
	}
	wg.Wait()
	close(results)

	byID := make(map[string]aggregatedSourcePage, len(selected))
	for result := range results {
		byID[result.id] = result
	}
	tracks := make([]Track, 0, len(selected)*limit)
	failures := make(map[string]string)
	var total int64
	maxPage := 1
	for _, source := range selected {
		result := byID[source.ID()]
		if result.err != nil {
			failures[result.id] = result.err.Error()
			continue
		}
		if result.page.Total > total {
			total = result.page.Total
		}
		if result.page.MaxPage > maxPage {
			maxPage = result.page.MaxPage
		}
		for _, track := range result.page.Tracks {
			if track.SourceID == "" {
				track.SourceID = result.id
			}
			if track.SourceID != result.id {
				failures[result.id] = "source returned a track owned by another source"
				continue
			}
			if err := validateTrack(track); err != nil {
				failures[result.id] = err.Error()
				continue
			}
			tracks = append(tracks, track)
		}
	}
	tracks = rankAndDeduplicateTracks(tracks, keyword)
	return tracks, failures, total, maxPage, nil
}

func rankAndDeduplicateTracks(tracks []Track, keyword string) []Track {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		var b strings.Builder
		for _, r := range value {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 0x4e00 && r <= 0x9fff {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	query := normalize(keyword)
	type ranked struct {
		track Track
		score int
		index int
	}
	items := make([]ranked, 0, len(tracks))
	seen := make(map[string]struct{}, len(tracks))
	for index, track := range tracks {
		title := normalize(track.Title)
		artist := normalize(track.Artist)
		key := title + "|" + artist
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		target := normalize(track.Title + " " + track.Artist)
		score := stringSimilarity(query, target)
		items = append(items, ranked{track: track, score: score, index: index})
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].score != items[j].score {
			return items[i].score > items[j].score
		}
		return items[i].index < items[j].index
	})
	result := make([]Track, 0, len(items))
	for _, item := range items {
		result = append(result, item.track)
	}
	return result
}

func stringSimilarity(left, right string) int {
	a := []rune(left)
	b := []rune(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	previous := make([]int, len(b)+1)
	for i := range previous {
		previous[i] = i
	}
	for i, char := range a {
		diagonal := previous[0]
		previous[0] = i + 1
		for j, other := range b {
			cost := 1
			if char == other {
				cost = 0
			}
			value := previous[j+1] + 1
			if previous[j]+1 < value {
				value = previous[j] + 1
			}
			if diagonal+cost < value {
				value = diagonal + cost
			}
			diagonal = previous[j+1]
			previous[j+1] = value
		}
	}
	similarity := 1 - float64(previous[len(b)])/float64(len(b))
	return int(similarity * 10000)
}

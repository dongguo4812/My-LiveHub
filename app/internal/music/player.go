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
	if !capabilities.Search || !capabilities.Resolve {
		return fmt.Errorf("music source %q must support search and playback resource resolution", id)
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

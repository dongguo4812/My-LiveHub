package music

import ("errors"; "fmt"; "sort"; "strings"; "sync")

type SourceCapabilities struct {
	Search  bool `json:"search"`
	Resolve bool `json:"resolve"`
}

type Track struct {
	Qualities []TrackQuality `json:"qualities,omitempty"`
	SelectedQuality string `json:"selected_quality,omitempty"`
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	// LXSourceKey is the source identifier used by the 洛雪 playback protocol.
	LXSourceKey string `json:"lx_source_key,omitempty"`
	// SourceData preserves provider-native identifiers and fields required by
	// the 洛雪 playback protocol (for example QQ songmid or Kugou FileHash).
	SourceData map[string]string `json:"source_data,omitempty"`
	Title      string `json:"title"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	CoverURL   string `json:"cover_url,omitempty"`
}

type Source interface { ID() string; Name() string; Capabilities() SourceCapabilities }

type SourceSnapshot struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Capabilities SourceCapabilities `json:"capabilities"`
	Actions      []string           `json:"actions,omitempty"`
	Qualitys     []string           `json:"qualitys,omitempty"`
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


// validateTrack checks the minimum identity and display fields required by
// the queue and playback engines. Platform-specific metadata remains optional.
func validateTrack(track Track) error {
	if strings.TrimSpace(track.ID) == "" {
		return errors.New("music track ID is required")
	}
	if strings.TrimSpace(track.SourceID) == "" {
		return errors.New("music track source ID is required")
	}
	if strings.TrimSpace(track.Title) == "" {
		return errors.New("music track title is required")
	}
	if track.DurationMS < 0 {
		return errors.New("music track duration cannot be negative")
	}
	return nil
}

package music

import ("errors"; "strings")

type SourceCapabilities struct {
	Search  bool `json:"search"`
	Resolve bool `json:"resolve"`
}

type Track struct {
	Qualities []TrackQuality `json:"qualities,omitempty"`
	SelectedQuality string `json:"selected_quality,omitempty"`
	ResolvedQuality string `json:"resolved_quality,omitempty"`
	ID         string `json:"id"`
	SourceID   string `json:"source_id"`
	// LXSourceKey identifies the LX-JS source namespace used for script actions.
	LXSourceKey string `json:"lx_source_key,omitempty"`
	// SourceData preserves native provider identifiers used by search, playback
	// URL resolution, and provider-specific lyric adapters.
	SourceData map[string]string `json:"source_data,omitempty"`
	Title      string `json:"title"`
	Artist     string `json:"artist,omitempty"`
	Album      string `json:"album,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
	CoverURL   string `json:"cover_url,omitempty"`
	// PlaybackResource is process-local and intentionally excluded from JSON.
	PlaybackResource *PlaybackResource `json:"-"`
}

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

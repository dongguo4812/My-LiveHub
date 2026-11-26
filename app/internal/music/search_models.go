package music

// LX platform identifiers and normalized search result models are shared by
// the source orchestration layer and script-backed search implementation.
// They intentionally live outside search_lx.go so the legacy native HTTP
// provider implementation can be removed independently.

const (
	LXSearchKW = "kw"
	LXSearchKG = "kg"
	LXSearchTX = "tx"
	LXSearchWY = "wy"
	LXSearchMG = "mg"
)

type TrackQuality struct {
	Type string            `json:"type"`
	Size string            `json:"size,omitempty"`
	SourceData map[string]string `json:"source_data,omitempty"`
}

type SearchPage struct {
	Tracks []Track `json:"tracks"`
	Total int64 `json:"total"`
	Page int `json:"page"`
	PageSize int `json:"page_size"`
	MaxPage int `json:"max_page"`
	Source string `json:"source"`
}

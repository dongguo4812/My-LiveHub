package music

import "encoding/json"

type LXDeclaredSource struct {
	Key string `json:"key"`
	Name string `json:"name,omitempty"`
	Type string `json:"type,omitempty"`
	Actions []string `json:"actions,omitempty"`
	Qualitys []string `json:"qualitys,omitempty"`
}

const LXUserSourceRuntimeVersion = "lx-electron-2.12.6"

type LXSourceRuntimeResult struct {
	Runtime string `json:"runtime,omitempty"`
	Initialized bool `json:"initialized"`
	Message string `json:"message,omitempty"`
	Platforms []string `json:"platforms"`
	DeclaredSources []LXDeclaredSource `json:"declared_sources"`
	Sources json.RawMessage `json:"sources"`
	Handlers []string `json:"handlers"`
}

type LXUserSourceUpdate struct {
	Log string `json:"log"`
	UpdateURL string `json:"update_url,omitempty"`
	SourceID string `json:"source_id,omitempty"`
	Name string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}

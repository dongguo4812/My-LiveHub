package voice

import "context"

type Audio struct {
	Data   []byte
	Format string
}

type VoiceOption struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Locale string `json:"locale"`
	Gender string `json:"gender"`
}

type SynthesisOptions struct {
	Voice  string
	Rate   string
	Volume string
	Pitch  string
}

// Engine is the provider-neutral contract. The voice module depends on this
// interface rather than on a particular TTS implementation.
type Engine interface {
	ID() string
	ListVoices(context.Context, string) ([]VoiceOption, error)
	Synthesize(context.Context, string, SynthesisOptions) (Audio, error)
}

package voice

import (
	"bytes"
	"context"
	"errors"
	"strings"

	edgetts "github.com/leopold7/edge-tts-go"
)

type EdgeEngine struct{}

func NewEdgeEngine() *EdgeEngine { return &EdgeEngine{} }

func (e *EdgeEngine) ID() string { return "edge" }

func (e *EdgeEngine) ListVoices(ctx context.Context, locale string) ([]VoiceOption, error) {
	voices, err := edgetts.ListVoices(ctx, "")
	if err != nil {
		return nil, err
	}
	locale = strings.TrimSpace(locale)
	result := make([]VoiceOption, 0, len(voices))
	for _, item := range voices {
		if locale != "" && !strings.EqualFold(item.Locale, locale) && !strings.HasPrefix(strings.ToLower(item.Locale), strings.ToLower(locale)+"-") {
			continue
		}
		result = append(result, VoiceOption{
			ID: item.ShortName, Name: item.FriendlyName, Locale: item.Locale, Gender: item.Gender,
		})
	}
	return result, nil
}

func (e *EdgeEngine) Synthesize(ctx context.Context, text string, options SynthesisOptions) (Audio, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Audio{}, errors.New("speech text is empty")
	}
	cfg := edgetts.DefaultConfig()
	if strings.TrimSpace(options.Voice) != "" {
		cfg.Voice = options.Voice
	}
	if options.Rate != "" {
		cfg.Rate = options.Rate
	}
	if options.Volume != "" {
		cfg.Volume = options.Volume
	}
	if options.Pitch != "" {
		cfg.Pitch = options.Pitch
	}
	communicate, err := edgetts.NewCommunicate(text, cfg)
	if err != nil {
		return Audio{}, err
	}
	var output bytes.Buffer
	err = communicate.Stream(ctx, func(chunk edgetts.TTSChunk) error {
		if chunk.Type == edgetts.ChunkTypeAudio {
			_, writeErr := output.Write(chunk.Data)
			return writeErr
		}
		return nil
	})
	if err != nil {
		return Audio{}, err
	}
	if output.Len() == 0 {
		return Audio{}, edgetts.ErrNoAudioReceived
	}
	return Audio{Data: output.Bytes(), Format: "audio/mpeg"}, nil
}

package voice

import (
	"context"
	"errors"
)

// Module adapts the voice service to the core module lifecycle without
// exposing lifecycle methods as frontend service bindings.
type Module struct {
	service *VoiceService
}

func NewModule(service *VoiceService) *Module {
	return &Module{service: service}
}

func (m *Module) Start(ctx context.Context) error {
	if m == nil || m.service == nil {
		return errors.New("voice service is unavailable")
	}
	return m.service.start(ctx)
}

func (m *Module) Stop(ctx context.Context) error {
	if m == nil || m.service == nil {
		return nil
	}
	return m.service.stop(ctx)
}

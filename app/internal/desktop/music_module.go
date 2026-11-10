package desktop

import (
	"context"
	"errors"
)

type MusicModule struct {
	service *MusicService
}

func NewMusicModule(service *MusicService) *MusicModule {
	return &MusicModule{service:service}
}

func (m *MusicModule) Start(ctx context.Context) error {
	if m == nil || m.service == nil { return errors.New("music service is unavailable") }
	return m.service.start(ctx)
}

func (m *MusicModule) Stop(ctx context.Context) error {
	if m == nil || m.service == nil { return nil }
	return m.service.stop(ctx)
}

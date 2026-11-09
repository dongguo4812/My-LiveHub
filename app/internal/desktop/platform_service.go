package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
)

type PlatformService struct {
	manager *platform.Manager
	events *bilibili.EventBuffer
}

func NewPlatformService(manager *platform.Manager, events *bilibili.EventBuffer) *PlatformService {
	return &PlatformService{manager:manager, events:events}
}

func (s *PlatformService) GetPlatforms() []platform.ConnectionSnapshot {
	if s == nil || s.manager == nil { return nil }
	return s.manager.Snapshot()
}

func (s *PlatformService) GetBilibiliEvents() []bilibili.Event {
	if s == nil || s.events == nil { return nil }
	return s.events.Snapshot()
}

func (s *PlatformService) ConnectPlatform(platformID string) error {
	if s == nil || s.manager == nil { return errors.New("platform connection manager is unavailable") }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.Connect(ctx, platformID)
}

func (s *PlatformService) DisconnectPlatform(platformID string) error {
	if s == nil || s.manager == nil { return errors.New("platform connection manager is unavailable") }
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.Disconnect(ctx, platformID)
}

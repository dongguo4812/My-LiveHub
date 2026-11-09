package desktop

import (
	"context"
	"errors"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
)

// PlatformService exposes platform connection state and user-requested
// connection operations to the desktop frontend.
type PlatformService struct {
	manager *platform.Manager
}

func NewPlatformService(manager *platform.Manager) *PlatformService {
	return &PlatformService{manager: manager}
}

func (s *PlatformService) GetPlatforms() []platform.ConnectionSnapshot {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Snapshot()
}

func (s *PlatformService) ConnectPlatform(platformID string) error {
	if s == nil || s.manager == nil {
		return errors.New("platform connection manager is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.Connect(ctx, platformID)
}

func (s *PlatformService) DisconnectPlatform(platformID string) error {
	if s == nil || s.manager == nil {
		return errors.New("platform connection manager is unavailable")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.manager.Disconnect(ctx, platformID)
}

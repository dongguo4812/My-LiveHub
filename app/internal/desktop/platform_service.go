package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
	"github.com/wailsapp/wails/v3/pkg/application"
)

type PlatformService struct {
	manager    *platform.Manager
	events     *bilibili.EventBuffer
	app        *application.App
	lightTheme bool
}

func NewPlatformService(manager *platform.Manager, events *bilibili.EventBuffer) *PlatformService {
	return &PlatformService{manager: manager, events: events}
}

// SetApplication is called after the Wails application has been created.
func (s *PlatformService) SetApplication(app *application.App, lightTheme bool) {
	if s == nil {
		return
	}
	s.app = app
	s.lightTheme = lightTheme
}

func (s *PlatformService) GetPlatforms() []platform.ConnectionSnapshot {
	if s == nil || s.manager == nil {
		return nil
	}
	return s.manager.Snapshot()
}

func (s *PlatformService) GetBilibiliEvents() []bilibili.Event {
	if s == nil || s.events == nil {
		return nil
	}
	return s.events.Snapshot()
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

// OpenSimulatorWindow opens one independent simulator window per platform.
func (s *PlatformService) OpenSimulatorWindow(platformID string) error {
	if s == nil || s.app == nil {
		return errors.New("simulator window service is unavailable")
	}
	if platformID != "bilibili" {
		return fmt.Errorf("暂不支持 %s 平台的事件模拟", platformID)
	}
	name := "simulator-" + platformID
	if existing, ok := s.app.Window.GetByName(name); ok {
		existing.Show()
		existing.Focus()
		return nil
	}
	background := application.NewRGBA(18, 20, 27, 255)
	if s.lightTheme {
		background = application.NewRGBA(242, 243, 245, 255)
	}
	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: name, Title: "B 站互动模拟器", Width: 1080, Height: 780,
		MinWidth: 900, MinHeight: 640, BackgroundColour: background,
		URL: "/#/simulator/bilibili",
	})
	window.Center()
	window.Show()
	return nil
}

// SimulateBilibiliEvent injects a local test event into the same event buffer
// used by the Bilibili adapter. It never calls Bilibili's network APIs.
func (s *PlatformService) SimulateBilibiliEvent(
	eventType string,
	roomID int,
	userID int,
	userName string,
	content string,
	giftName string,
	giftCount int,
	unitPrice float64,
	amount float64,
	duration int,
	guardLevel int,
	months int,
) (bilibili.Event, error) {
	if s == nil || s.events == nil {
		return bilibili.Event{}, errors.New("Bilibili event service is unavailable")
	}
	return s.events.AddSimulationEvent(bilibili.SimulationEventInput{
		Type: eventType, RoomID: roomID, UserID: userID, UserName: userName,
		Content: content, GiftName: giftName, GiftCount: giftCount,
		UnitPrice: unitPrice, Amount: amount, Duration: duration,
		GuardLevel: guardLevel, Months: months,
	})
}

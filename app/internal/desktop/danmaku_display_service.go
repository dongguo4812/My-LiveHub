package desktop

import (
	"errors"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/danmaku"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
)

type DanmakuDisplayService struct {
	manager   *config.Manager
	events    *eventstream.Buffer
	processor *danmaku.Processor
	mu        sync.RWMutex
	current   config.DanmakuDisplayConfig
	app       *application.App
	window    *application.WebviewWindow
	pinned    bool
}

func NewDanmakuDisplayService(manager *config.Manager, current config.DanmakuDisplayConfig, buffer *eventstream.Buffer) *DanmakuDisplayService {
	return &DanmakuDisplayService{manager: manager, current: current, events: buffer, processor: danmaku.NewProcessor()}
}

func (s *DanmakuDisplayService) GetSettings() config.DanmakuDisplayConfig {
	if s == nil {
		return config.DefaultDanmakuDisplayConfig()
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func (s *DanmakuDisplayService) UpdateSettings(next config.DanmakuDisplayConfig) (config.DanmakuDisplayConfig, error) {
	if s == nil || s.manager == nil {
		return config.DanmakuDisplayConfig{}, errors.New("弹幕显示设置服务不可用")
	}
	if err := next.Validate(); err != nil {
		return config.DanmakuDisplayConfig{}, err
	}
	if err := config.SaveTyped(s.manager, "danmaku_display.json", next, func(value config.DanmakuDisplayConfig) error {
		return value.Validate()
	}); err != nil {
		return config.DanmakuDisplayConfig{}, err
	}
	s.mu.Lock()
	s.current = next
	s.mu.Unlock()
	return next, nil
}

// GetDisplayEvents returns a processed snapshot while leaving the shared event history untouched.
func (s *DanmakuDisplayService) GetDisplayEvents() []danmaku.DisplayEvent {
	if s == nil || s.events == nil || s.processor == nil {
		return []danmaku.DisplayEvent{}
	}
	s.mu.RLock()
	settings := s.current
	s.mu.RUnlock()
	return s.processor.Process(s.events.Snapshot(), settings, time.Now())
}


// DanmakuWindowState describes the independent overlay window.
type DanmakuWindowState struct {
	IsOpen   bool `json:"is_open"`
	IsPinned bool `json:"is_pinned"`
}

// AttachDanmakuDisplayApplication supplies the Wails application after startup.
func AttachDanmakuDisplayApplication(s *DanmakuDisplayService, app *application.App) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

// OpenWindow creates or focuses the transparent, frameless danmaku overlay.
func (s *DanmakuDisplayService) OpenWindow() error {
	if s == nil {
		return errors.New("弹幕展示窗口服务不可用")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.app == nil {
		return errors.New("桌面窗口服务尚未初始化")
	}
	if s.window != nil {
		s.window.Show()
		s.window.Focus()
		return nil
	}
	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "danmaku-overlay",
		Title: "",
		Width: 1000,
		Height: 560,
		MinWidth: 420,
		MinHeight: 180,
		Frameless: true,
		DisableResize: false,
		BackgroundType: application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		// Treat the overlay as a floating tool window so the Windows desktop switcher
		// does not handle it like a regular taskbar application window.
		Windows: application.WindowsWindow{
			HiddenOnTaskbar: true,
			DisableFramelessWindowDecorations: true,
		},
		URL: "/#/danmaku/overlay",
	})
	s.window = window
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		s.mu.Lock()
		if s.window == window {
			s.window = nil
			s.pinned = false
		}
		s.mu.Unlock()
	})
	window.Center()
	window.Show()
	return nil
}

// CloseWindow closes only the overlay; it does not affect platform connections.
func (s *DanmakuDisplayService) CloseWindow() {
	if s == nil {
		return
	}
	s.mu.Lock()
	window := s.window
	s.window = nil
	s.pinned = false
	s.mu.Unlock()
	if window != nil {
		window.SetIgnoreMouseEvents(false)
		window.SetAlwaysOnTop(false)
		window.Close()
	}
}

// SetWindowPinned toggles topmost and click-through together.
func (s *DanmakuDisplayService) SetWindowPinned(pinned bool) error {
	if s == nil {
		return errors.New("弹幕展示窗口服务不可用")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.window == nil {
		return errors.New("请先打开弹幕展示窗口")
	}
	if pinned {
		s.window.SetAlwaysOnTop(true)
		s.window.SetIgnoreMouseEvents(true)
	} else {
		s.window.SetIgnoreMouseEvents(false)
		s.window.SetAlwaysOnTop(false)
	}
	s.pinned = pinned
	return nil
}

// GetWindowState reports whether the overlay is open and pinned.
func (s *DanmakuDisplayService) GetWindowState() DanmakuWindowState {
	if s == nil {
		return DanmakuWindowState{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return DanmakuWindowState{IsOpen: s.window != nil, IsPinned: s.window != nil && s.pinned}
}

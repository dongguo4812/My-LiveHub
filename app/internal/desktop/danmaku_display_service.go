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
	windowCfg config.DanmakuWindowConfig
	app       *application.App
	window    *application.WebviewWindow
	pinned    bool
	saveTimer *time.Timer
}

func NewDanmakuDisplayService(manager *config.Manager, current config.DanmakuDisplayConfig, windowCfg config.DanmakuWindowConfig, buffer *eventstream.Buffer) *DanmakuDisplayService {
	return &DanmakuDisplayService{
		manager: manager, current: current, windowCfg: windowCfg,
		events: buffer, processor: danmaku.NewProcessor(),
	}
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

type DanmakuWindowState struct {
	IsOpen   bool `json:"is_open"`
	IsPinned bool `json:"is_pinned"`
}

func AttachDanmakuDisplayApplication(s *DanmakuDisplayService, app *application.App) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

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

	cfg := s.windowCfg
	options := application.WebviewWindowOptions{
		Name: "danmaku-overlay",
		Title: "",
		Width: cfg.Width,
		Height: cfg.Height,
		MinWidth: 420,
		MinHeight: 180,
		Frameless: true,
		DisableResize: false,
		BackgroundType: application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL: "/#/danmaku/overlay",
		Windows: application.WindowsWindow{
			// Keep the overlay truly frameless; the page supplies its own
			// interactive resize affordance when the window is not pinned.
			DisableFramelessWindowDecorations: true,
			HiddenOnTaskbar: true,
		},
	}
	if cfg.PositionSaved {
		options.InitialPosition = application.WindowXY
		options.X = cfg.X
		options.Y = cfg.Y
	}
	window := s.app.Window.NewWithOptions(options)
	s.window = window
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		s.persistWindowGeometry(window)
		s.mu.Lock()
		if s.window == window {
			s.window = nil
			s.pinned = false
			if s.saveTimer != nil {
				s.saveTimer.Stop()
				s.saveTimer = nil
			}
		}
		s.mu.Unlock()
	})
	window.OnWindowEvent(events.Common.WindowDidMove, func(_ *application.WindowEvent) {
		s.scheduleWindowGeometrySave(window)
	})
	window.OnWindowEvent(events.Common.WindowDidResize, func(_ *application.WindowEvent) {
		s.scheduleWindowGeometrySave(window)
	})
	// Windows "Show desktop" can minimise or hide ordinary top-level windows.
	// A pinned overlay is a tool overlay, so immediately restore its visibility.
	restorePinned := func(_ *application.WindowEvent) {
		s.mu.RLock()
		shouldRestore := s.window == window && s.pinned
		s.mu.RUnlock()
		if shouldRestore {
			go func() {
				window.UnMinimise()
				window.Show()
				window.SetAlwaysOnTop(true)
				window.SetIgnoreMouseEvents(true)
			}()
		}
	}
	window.OnWindowEvent(events.Common.WindowMinimise, restorePinned)
	window.OnWindowEvent(events.Common.WindowHide, restorePinned)

	if !cfg.PositionSaved {
		window.Center()
	}
	window.Show()
	return nil
}

func (s *DanmakuDisplayService) scheduleWindowGeometrySave(window *application.WebviewWindow) {
	if s == nil || window == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.window != window {
		return
	}
	if s.saveTimer != nil {
		s.saveTimer.Stop()
	}
	s.saveTimer = time.AfterFunc(350*time.Millisecond, func() {
		s.persistWindowGeometry(window)
	})
}

func (s *DanmakuDisplayService) persistWindowGeometry(window *application.WebviewWindow) {
	if s == nil || window == nil || s.manager == nil {
		return
	}
	x, y := window.Position()
	width, height := window.Size()
	if width < 420 || height < 180 {
		return
	}
	s.mu.Lock()
	if s.window != window {
		s.mu.Unlock()
		return
	}
	next := s.windowCfg
	next.X, next.Y = x, y
	next.Width, next.Height = width, height
	next.PositionSaved = true
	s.mu.Unlock()

	if err := config.SaveTyped(s.manager, "danmaku_window.json", next, func(value config.DanmakuWindowConfig) error {
		return value.Validate()
	}); err != nil {
		return
	}
	s.mu.Lock()
	if s.window == window {
		s.windowCfg = next
	}
	s.mu.Unlock()
}

func (s *DanmakuDisplayService) CloseWindow() {
	if s == nil {
		return
	}
	s.mu.Lock()
	window := s.window
	s.window = nil
	s.pinned = false
	if s.saveTimer != nil {
		s.saveTimer.Stop()
		s.saveTimer = nil
	}
	s.mu.Unlock()
	if window != nil {
		s.persistWindowGeometryForClose(window)
		window.SetIgnoreMouseEvents(false)
		window.SetAlwaysOnTop(false)
		window.Close()
	}
}

// persistWindowGeometryForClose saves the final frame after CloseWindow has detached
// the window from the service's active-window pointer.
func (s *DanmakuDisplayService) persistWindowGeometryForClose(window *application.WebviewWindow) {
	if s == nil || window == nil || s.manager == nil {
		return
	}
	x, y := window.Position()
	width, height := window.Size()
	if width < 420 || height < 180 {
		return
	}
	s.mu.Lock()
	next := s.windowCfg
	next.X, next.Y, next.Width, next.Height = x, y, width, height
	next.PositionSaved = true
	s.mu.Unlock()
	if err := config.SaveTyped(s.manager, "danmaku_window.json", next, func(value config.DanmakuWindowConfig) error {
		return value.Validate()
	}); err == nil {
		s.mu.Lock()
		s.windowCfg = next
		s.mu.Unlock()
	}
}

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

func (s *DanmakuDisplayService) GetWindowState() DanmakuWindowState {
	if s == nil {
		return DanmakuWindowState{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return DanmakuWindowState{IsOpen: s.window != nil, IsPinned: s.window != nil && s.pinned}
}

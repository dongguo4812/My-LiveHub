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
	"github.com/dongguo4812/My-LiveHub/app/internal/media/imagecache"
)

type DanmakuDisplayService struct {
	manager   *config.Manager
	events    *eventstream.Buffer
	processor *danmaku.Processor
	imageCache *imagecache.Manager
	mu        sync.RWMutex
	current   config.DanmakuDisplayConfig
	windowCfg config.DanmakuWindowConfig
	app       *application.App
	window    *application.WebviewWindow
	pinned    bool
	saveTimer *time.Timer
}

func NewDanmakuDisplayService(manager *config.Manager, current config.DanmakuDisplayConfig, windowCfg config.DanmakuWindowConfig, buffer *eventstream.Buffer, imageCache *imagecache.Manager) *DanmakuDisplayService {
	return &DanmakuDisplayService{
		manager: manager, current: current, windowCfg: windowCfg,
		events: buffer, processor: danmaku.NewProcessor(), imageCache: imageCache,
	}
}

func (s *DanmakuDisplayService) GetSettings() config.DanmakuDisplayConfig {
	if s == nil { return config.DefaultDanmakuDisplayConfig() }
	s.mu.RLock(); defer s.mu.RUnlock()
	return s.current
}

func (s *DanmakuDisplayService) UpdateSettings(next config.DanmakuDisplayConfig) (config.DanmakuDisplayConfig, error) {
	if s == nil || s.manager == nil { return config.DanmakuDisplayConfig{}, errors.New("弹幕显示设置服务不可用") }
	if err := next.Validate(); err != nil { return config.DanmakuDisplayConfig{}, err }
	if err := config.SaveTyped(s.manager, "danmaku_display.json", next, func(value config.DanmakuDisplayConfig) error { return value.Validate() }); err != nil { return config.DanmakuDisplayConfig{}, err }
	s.mu.Lock(); s.current = next; s.mu.Unlock()
	return next, nil
}

// GetDisplayEvents returns a processed snapshot. Cache downloads are scheduled without blocking this call.
func (s *DanmakuDisplayService) GetDisplayEvents() []danmaku.DisplayEvent {
	if s == nil || s.events == nil || s.processor == nil { return []danmaku.DisplayEvent{} }
	s.mu.RLock(); settings := s.current; s.mu.RUnlock()
	result := s.processor.Process(s.events.Snapshot(), settings, time.Now())
	if s.imageCache == nil { return result }
	for i := range result {
		giftImageURL := result[i].Event.GiftImageURL
		if result[i].Event.Type == "gift" && giftImageURL != "" {
			if localURL := s.imageCache.CachedURLFor(result[i].Event.PlatformID,giftImageURL); localURL != "" {
				result[i].Event.GiftImageCacheURL = localURL
			} else {
				s.imageCache.PrefetchFor(result[i].Event.PlatformID,giftImageURL)
			}
		}
		giftAnimationURL := result[i].Event.GiftAnimationURL
		if result[i].Event.Type == "gift" && giftAnimationURL != "" {
			if localURL := s.imageCache.CachedURLFor(result[i].Event.PlatformID,giftAnimationURL); localURL != "" {
				result[i].Event.GiftAnimationCacheURL = localURL
			} else {
				s.imageCache.PrefetchFor(result[i].Event.PlatformID,giftAnimationURL)
			}
		}
		for j := range result[i].Event.Emoticons {
			token := &result[i].Event.Emoticons[j]
			if localURL := s.imageCache.CachedURLForResource(result[i].Event.PlatformID, token.ResourceID, token.URL); localURL != "" {
				token.CacheURL = localURL
			} else {
				s.imageCache.PrefetchForResource(result[i].Event.PlatformID, token.ResourceID, token.URL)
			}
		}
	}
	return result
}

type DanmakuWindowState struct { IsOpen bool `json:"is_open"`; IsPinned bool `json:"is_pinned"` }

func AttachDanmakuDisplayApplication(s *DanmakuDisplayService, app *application.App) {
	if s == nil { return }; s.mu.Lock(); s.app = app; s.mu.Unlock()
}

func (s *DanmakuDisplayService) OpenWindow() error {
	if s == nil { return errors.New("弹幕展示窗口服务不可用") }
	s.mu.Lock(); defer s.mu.Unlock()
	if s.app == nil { return errors.New("桌面窗口服务尚未初始化") }
	if s.window != nil { s.window.Show(); s.window.Focus(); return nil }
	cfg := s.windowCfg
	cfg.OpenOnStartup = true
	if err := config.SaveTyped(s.manager, "danmaku_window.json", cfg, func(value config.DanmakuWindowConfig) error { return value.Validate() }); err != nil { return err }
	s.windowCfg = cfg
	options := application.WebviewWindowOptions{
		Name: "danmaku-overlay", Title: "", Width: cfg.Width, Height: cfg.Height,
		MinWidth: 420, MinHeight: 180, Frameless: true, DisableResize: false,
		BackgroundType: application.BackgroundTypeTransparent, BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL: "/#/danmaku/overlay",
		Windows: application.WindowsWindow{DisableFramelessWindowDecorations: true},
	}
	if cfg.PositionSaved { options.InitialPosition = application.WindowXY; options.X = cfg.X; options.Y = cfg.Y }
	s.pinned = cfg.Pinned
	window := s.app.Window.NewWithOptions(options)
	s.window = window
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		s.persistWindowGeometryAndOpenState(window, false)
		s.mu.Lock()
		if s.window == window { s.window = nil; s.pinned = false; if s.saveTimer != nil { s.saveTimer.Stop(); s.saveTimer = nil } }
		s.mu.Unlock()
	})
	window.OnWindowEvent(events.Common.WindowDidMove, func(_ *application.WindowEvent) { s.scheduleWindowGeometrySave(window) })
	window.OnWindowEvent(events.Common.WindowDidResize, func(_ *application.WindowEvent) { s.scheduleWindowGeometrySave(window) })
	restorePinned := func(_ *application.WindowEvent) {
		s.mu.RLock(); shouldRestore := s.window == window && s.pinned; s.mu.RUnlock()
		if shouldRestore { go func() { window.UnMinimise(); window.Show(); window.SetAlwaysOnTop(true); window.SetIgnoreMouseEvents(true) }() }
	}
	window.OnWindowEvent(events.Common.WindowMinimise, restorePinned)
	window.OnWindowEvent(events.Common.WindowHide, restorePinned)
	if !cfg.PositionSaved { window.Center() }
	window.Show()
	if cfg.Pinned { window.SetAlwaysOnTop(true); window.SetIgnoreMouseEvents(true) }
	return nil
}

func (s *DanmakuDisplayService) scheduleWindowGeometrySave(window *application.WebviewWindow) {
	if s == nil || window == nil { return }
	s.mu.Lock(); defer s.mu.Unlock()
	if s.window != window { return }
	if s.saveTimer != nil { s.saveTimer.Stop() }
	s.saveTimer = time.AfterFunc(350*time.Millisecond, func() { s.persistWindowGeometry(window) })
}
func (s *DanmakuDisplayService) persistWindowGeometry(window *application.WebviewWindow) { s.persistWindowGeometryAndOpenState(window, true) }
func (s *DanmakuDisplayService) persistWindowGeometryAndOpenState(window *application.WebviewWindow, openOnStartup bool) {
	if s == nil || window == nil || s.manager == nil { return }
	x, y := window.Position(); width, height := window.Size()
	if width < 420 || height < 180 { return }
	s.mu.Lock()
	if s.window != window { s.mu.Unlock(); return }
	next := s.windowCfg; next.X, next.Y, next.Width, next.Height = x, y, width, height; next.PositionSaved = true; next.OpenOnStartup = openOnStartup
	s.mu.Unlock()
	if err := config.SaveTyped(s.manager, "danmaku_window.json", next, func(value config.DanmakuWindowConfig) error { return value.Validate() }); err != nil { return }
	s.mu.Lock(); s.windowCfg = next; s.mu.Unlock()
}
func (s *DanmakuDisplayService) CloseWindow() { s.closeWindow(false) }
func (s *DanmakuDisplayService) CloseWindowForShutdown() { s.closeWindow(true) }
func (s *DanmakuDisplayService) closeWindow(preserveOpenPreference bool) {
	if s == nil { return }
	s.mu.Lock(); window := s.window; s.window = nil; s.pinned = false; openOnStartup := s.windowCfg.OpenOnStartup
	if !preserveOpenPreference { openOnStartup = false; s.windowCfg.OpenOnStartup = false }
	if s.saveTimer != nil { s.saveTimer.Stop(); s.saveTimer = nil }
	s.mu.Unlock()
	if window != nil {
		s.persistWindowGeometryForClose(window, openOnStartup); window.SetIgnoreMouseEvents(false); window.SetAlwaysOnTop(false); window.Close()
	} else if !preserveOpenPreference && s.manager != nil {
		s.mu.Lock(); next := s.windowCfg; s.mu.Unlock()
		_ = config.SaveTyped(s.manager, "danmaku_window.json", next, func(value config.DanmakuWindowConfig) error { return value.Validate() })
	}
}
func (s *DanmakuDisplayService) persistWindowGeometryForClose(window *application.WebviewWindow, openOnStartup bool) {
	if s == nil || window == nil || s.manager == nil { return }
	x, y := window.Position(); width, height := window.Size()
	if width < 420 || height < 180 { return }
	s.mu.Lock(); next := s.windowCfg; next.X, next.Y, next.Width, next.Height = x, y, width, height; next.PositionSaved = true; next.OpenOnStartup = openOnStartup; s.mu.Unlock()
	if err := config.SaveTyped(s.manager, "danmaku_window.json", next, func(value config.DanmakuWindowConfig) error { return value.Validate() }); err == nil { s.mu.Lock(); s.windowCfg = next; s.mu.Unlock() }
}
func (s *DanmakuDisplayService) SetWindowPinned(pinned bool) error {
	if s == nil { return errors.New("弹幕展示窗口服务不可用") }
	s.mu.Lock(); defer s.mu.Unlock()
	if s.window == nil { return errors.New("请先打开弹幕展示窗口") }
	if s.manager == nil { return errors.New("弹幕窗口配置服务不可用") }
	next := s.windowCfg; next.Pinned = pinned
	if err := config.SaveTyped(s.manager, "danmaku_window.json", next, func(value config.DanmakuWindowConfig) error { return value.Validate() }); err != nil { return err }
	if pinned { s.window.SetAlwaysOnTop(true); s.window.SetIgnoreMouseEvents(true) } else { s.window.SetIgnoreMouseEvents(false); s.window.SetAlwaysOnTop(false) }
	s.windowCfg = next; s.pinned = pinned; return nil
}
func (s *DanmakuDisplayService) GetWindowState() DanmakuWindowState {
	if s == nil { return DanmakuWindowState{} }
	s.mu.RLock(); defer s.mu.RUnlock()
	return DanmakuWindowState{IsOpen: s.window != nil, IsPinned: s.window != nil && s.pinned}
}

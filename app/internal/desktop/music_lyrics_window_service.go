package desktop

import (
	"errors"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

type MusicLyricsWindowState struct {
	IsOpen bool `json:"is_open"`
}

type MusicLyricsWindowService struct {
	music  *MusicService
	mu     sync.RWMutex
	app    *application.App
	window *application.WebviewWindow
}

func NewMusicLyricsWindowService(music *MusicService) *MusicLyricsWindowService {
	return &MusicLyricsWindowService{music: music}
}

func AttachMusicLyricsWindowApplication(s *MusicLyricsWindowService, app *application.App) {
	if s == nil { return }
	s.mu.Lock()
	s.app = app
	s.mu.Unlock()
}

func (s *MusicLyricsWindowService) OpenWindow() error {
	if s == nil { return errors.New("桌面歌词窗口服务不可用") }
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.app == nil { return errors.New("桌面窗口服务尚未初始化") }
	if s.window != nil {
		s.window.Show()
		s.window.Focus()
		return nil
	}
	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "music-lyrics-overlay",
		Title: "",
		Width: 820,
		Height: 190,
		MinWidth: 420,
		MinHeight: 130,
		Frameless: true,
		DisableResize: false,
		BackgroundType: application.BackgroundTypeTransparent,
		BackgroundColour: application.NewRGBA(0, 0, 0, 0),
		URL: "/#/music/lyrics-overlay",
		Windows: application.WindowsWindow{DisableFramelessWindowDecorations: true},
	})
	s.window = window
	window.OnWindowEvent(events.Common.WindowClosing, func(_ *application.WindowEvent) {
		s.mu.Lock()
		if s.window == window { s.window = nil }
		s.mu.Unlock()
	})
	window.Center()
	window.SetAlwaysOnTop(true)
	window.Show()
	return nil
}

func (s *MusicLyricsWindowService) CloseWindow() {
	if s == nil { return }
	s.mu.Lock()
	window := s.window
	s.window = nil
	s.mu.Unlock()
	if window != nil { window.Close() }
}

func (s *MusicLyricsWindowService) CloseWindowForShutdown() {
	s.CloseWindow()
}

func (s *MusicLyricsWindowService) GetWindowState() MusicLyricsWindowState {
	if s == nil { return MusicLyricsWindowState{} }
	s.mu.RLock()
	defer s.mu.RUnlock()
	return MusicLyricsWindowState{IsOpen: s.window != nil}
}

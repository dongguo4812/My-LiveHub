package main

import (
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

const secondaryWindowName = "secondary-window"

type WindowService struct {
	app *application.App

	mu sync.Mutex
	secondary *application.WebviewWindow
}

// OpenSecondaryWindow opens the validation window, reusing the existing
// instance when it is already open.
func (s *WindowService) OpenSecondaryWindow() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.secondary != nil {
		s.secondary.Show()
		s.secondary.Focus()
		return "已恢复已有子窗口"
	}

	window := s.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             secondaryWindowName,
		Title:            "My-LiveHub - 子窗口验证",
		Width:            720,
		Height:           480,
		MinWidth:         480,
		MinHeight:        320,
		BackgroundColour: application.NewRGB(18, 20, 30),
		URL:              "/?window=secondary",
	})

	window.OnWindowEvent(events.Common.WindowClosing, func(event *application.WindowEvent) {
		s.mu.Lock()
		if s.secondary == window {
			s.secondary = nil
		}
		s.mu.Unlock()
	})

	s.secondary = window
	window.Center()
	window.Show()

	return "子窗口已创建"
}

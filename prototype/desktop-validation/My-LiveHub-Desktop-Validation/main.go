package main

import (
	"context"
	"embed"
	"log"
	"sync/atomic"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/appicon.png
var trayIcon []byte

func init() {
	application.RegisterEvent[string]("time")
}

func main() {
	windowService := &WindowService{}

	app := application.New(application.Options{
		Name:        "My-LiveHub-Desktop-Validation",
		Description: "My-LiveHub desktop lifecycle validation",
		Services: []application.Service{
			application.NewService(&GreetService{}),
			application.NewService(windowService),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})
	windowService.app = app

	displayServer, err := startDisplayServer()
	if err != nil {
		log.Fatalf("start OBS display server on %s: %v", displayServerAddress, err)
	}

	// Stop background work and the standalone OBS HTTP server during shutdown.
	ctx, cancel := context.WithCancel(context.Background())
	app.OnShutdown(func() {
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := displayServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown OBS display server: %v", err)
		}
	})

	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main-window",
		Title:            "My-LiveHub 桌面验证",
		Width:            1000,
		Height:           680,
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

	// Closing the main window hides it instead of terminating the app.
	// The tray's Quit action explicitly bypasses this behaviour.
	var quitting atomic.Bool
	mainWindow.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		if quitting.Load() {
			return
		}
		mainWindow.Hide()
		event.Cancel()
	})

	tray := app.SystemTray.New()
	tray.SetIcon(trayIcon)
	tray.SetTooltip("My-LiveHub 桌面验证")
	tray.OnClick(func() {
		mainWindow.Show()
		mainWindow.Focus()
	})

	trayMenu := app.NewMenu()
	trayMenu.Add("打开主窗口").OnClick(func(ctx *application.Context) {
		mainWindow.Show()
		mainWindow.Focus()
	})
	trayMenu.AddSeparator()
	trayMenu.Add("退出程序").OnClick(func(ctx *application.Context) {
		quitting.Store(true)
		app.Quit()
	})
	tray.SetMenu(trayMenu)

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				app.Event.Emit("time", time.Now().Format(time.RFC1123))
			}
		}
	}()

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

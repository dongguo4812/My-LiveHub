package main

import (
	"embed"
	"context"
	"log"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

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
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	windowService.app = app

	// Stop background work as soon as the application begins shutting down.
	ctx, cancel := context.WithCancel(context.Background())
	app.OnShutdown(cancel)

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main-window",
		Title:            "My-LiveHub 桌面验证",
		Width:            1000,
		Height:           680,
		BackgroundColour: application.NewRGB(6, 7, 15),
		URL:              "/",
	})

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

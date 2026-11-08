package main

import (
	"embed"
	"log"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := application.New(application.Options{
		Name:        "My-LiveHub",
		Description: "多平台综合直播互动辅助平台",
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(assets),
		},
	})

	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main-window",
		Title:            "My-LiveHub",
		Width:            1440,
		Height:           900,
		MinWidth:         1100,
		MinHeight:        680,
		BackgroundColour: application.NewRGB(18, 20, 27),
		URL:              "/",
	})

	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

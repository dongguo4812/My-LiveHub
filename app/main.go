package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed config/default/*.json
var embeddedDefaultConfig embed.FS

func main() {
	environment := config.ResolveEnvironment(os.Args[1:])
	paths, err := config.ResolveRuntimePaths(environment)
	if err != nil {
		log.Fatalf("initialize configuration paths: %v", err)
	}

	var defaultConfigFS fs.FS
	if environment == config.EnvironmentDevelopment {
		defaultConfigFS = os.DirFS(paths.DefaultDir)
	} else {
		defaultConfigFS, err = fs.Sub(embeddedDefaultConfig, "config/default")
		if err != nil {
			log.Fatalf("open embedded default configuration: %v", err)
		}
	}

	configManager, err := config.NewManager(defaultConfigFS, paths.LocalDir)
	if err != nil {
		log.Fatalf("initialize configuration manager: %v", err)
	}
	if _, err := configManager.Load("app.json"); err != nil {
		log.Fatalf("load application configuration: %v", err)
	}

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

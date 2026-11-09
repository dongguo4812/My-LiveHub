package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/core"
	"github.com/dongguo4812/My-LiveHub/app/internal/desktop"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
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
	appConfig, err := config.LoadTyped[config.AppConfig](configManager, "app.json", func(value config.AppConfig) error {
		return value.Validate()
	})
	if err != nil {
		log.Fatalf("load application configuration: %v", err)
	}
	coreApp, err := core.New(configManager, appConfig)
	if err != nil {
		log.Fatalf("initialize core application: %v", err)
	}

	credentialStore, err := bilibili.NewDPAPICredentialStore()
	if err != nil {
		log.Fatalf("initialize Bilibili credential store: %v", err)
	}
	bilibiliAuth, err := bilibili.NewAuthManager(credentialStore, nil)
	if err != nil {
		log.Fatalf("initialize Bilibili authentication: %v", err)
	}
	bilibiliConfig, err := config.LoadTyped[config.BilibiliConfig](configManager, "bilibili.json", func(value config.BilibiliConfig) error {
		return value.Validate()
	})
	if err != nil {
		log.Fatalf("load Bilibili configuration: %v", err)
	}
	bilibiliEvents := eventstream.NewBuffer()
	bilibiliAdapter, err := bilibili.NewAdapter(bilibiliAuth, bilibiliConfig.RoomID, bilibiliEvents)
	if err != nil {
		log.Fatalf("initialize Bilibili adapter: %v", err)
	}

	platformManager := platform.NewManager()
	if err := platformManager.Register(bilibiliAdapter); err != nil {
		log.Fatalf("register Bilibili adapter: %v", err)
	}
	if err := coreApp.Modules().Register("platform-connections", platformManager); err != nil {
		log.Fatalf("register platform connection module: %v", err)
	}

	platformService := desktop.NewPlatformService(platformManager, bilibiliEvents)
	app := application.New(application.Options{
		Name:        "My-LiveHub",
		Description: "多平台综合直播互动辅助平台",
		Assets:      application.AssetOptions{Handler: application.AssetFileServerFS(assets)},
		Services: []application.Service{
			application.NewService(desktop.NewCoreService(coreApp)),
			application.NewService(platformService),
			application.NewService(desktop.NewBilibiliAuthService(bilibiliAuth)),
			application.NewService(desktop.NewBilibiliSettingsService(configManager, bilibiliConfig, bilibiliAdapter)),
			application.NewService(desktop.NewAppearanceService(configManager, appConfig)),
		},
	})
	desktop.AttachApplication(platformService, app, appConfig.Appearance.Theme == "light")

	background := application.NewRGBA(18, 20, 27, 255)
	if appConfig.Appearance.Theme == "light" {
		background = application.NewRGBA(245, 247, 251, 255)
	}
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main-window", Title: "My-LiveHub", Width: 1440, Height: 900,
		MinWidth: 1100, MinHeight: 680, BackgroundColour: background, URL: "/",
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

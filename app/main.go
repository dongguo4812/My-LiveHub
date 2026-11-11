package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/dongguo4812/My-LiveHub/app/internal/config"
	"github.com/dongguo4812/My-LiveHub/app/internal/core"
	"github.com/dongguo4812/My-LiveHub/app/internal/desktop"
	eventstream "github.com/dongguo4812/My-LiveHub/app/internal/events"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform"
	"github.com/dongguo4812/My-LiveHub/app/internal/platform/bilibili"
	"github.com/dongguo4812/My-LiveHub/app/internal/voice"
	"github.com/dongguo4812/My-LiveHub/app/internal/media/imagecache"
	"github.com/dongguo4812/My-LiveHub/app/internal/media/resource"
	"github.com/dongguo4812/My-LiveHub/app/internal/music"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
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
	danmakuDisplayConfig, err := config.LoadTyped[config.DanmakuDisplayConfig](configManager, "danmaku_display.json", func(value config.DanmakuDisplayConfig) error {
		return value.Validate()
	})
	if err != nil {
		log.Fatalf("load danmaku display configuration: %v", err)
	}
	danmakuWindowConfig, err := config.LoadTyped[config.DanmakuWindowConfig](configManager, "danmaku_window.json", func(value config.DanmakuWindowConfig) error {
		return value.Validate()
	})
	if err != nil {
		log.Fatalf("load danmaku window configuration: %v", err)
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
	musicConfig, err := config.LoadTyped[config.MusicConfig](configManager, "music.json", func(value config.MusicConfig) error {
		if value.ConfigVersion == 1 {
			return nil
		}
		return value.Validate()
	})
	if err != nil {
		log.Fatalf("load music configuration: %v", err)
	}
	if migratedMusicConfig, changed := config.MigrateMusicConfig(musicConfig); changed {
		if err := config.SaveTyped(configManager, "music.json", migratedMusicConfig, func(value config.MusicConfig) error {
			return value.Validate()
		}); err != nil {
			log.Fatalf("migrate music configuration: %v", err)
		}
		musicConfig = migratedMusicConfig
	}
	voiceConfig, err := config.LoadTyped[config.VoiceConfig](configManager, "voice.json", func(value config.VoiceConfig) error {
		return value.Validate()
	})
	if err != nil {
		log.Fatalf("load voice configuration: %v", err)
	}
	eventBus := eventstream.NewBus()
	bilibiliEvents := eventstream.NewBuffer(eventBus)
	musicService := desktop.NewMusicService(configManager, musicConfig, bilibiliEvents)
	videoService := desktop.NewVideoService(musicService, music.NewMpvVideoEngine(""))
	cacheDir := ""
	if environment == config.EnvironmentDevelopment {
		if workingDir, getwdErr := os.Getwd(); getwdErr == nil {
			cacheDir = filepath.Join(workingDir, "cache")
		}
	} else if userConfigDir, configDirErr := os.UserConfigDir(); configDirErr == nil {
		cacheDir = filepath.Join(userConfigDir, "My-LiveHub", "cache")
	}
	mediaCatalog, catalogErr := resource.NewPersistentCatalog(filepath.Join(cacheDir, "media-resources"))
	if catalogErr != nil {
		log.Printf("load local media resource catalog: %v", catalogErr)
	}
	if mediaCatalog == nil {
		mediaCatalog = resource.NewCatalog()
	}
	bilibiliAdapter, err := bilibili.NewAdapter(bilibiliAuth, bilibiliConfig.RoomID, bilibiliEvents)
	if err != nil {
		log.Fatalf("initialize Bilibili adapter: %v", err)
	}
	bilibiliAdapter.SetMediaCatalog(mediaCatalog)

	platformManager := platform.NewManager()
	if err := platformManager.Register(bilibiliAdapter); err != nil {
		log.Fatalf("register Bilibili adapter: %v", err)
	}
	if err := coreApp.Modules().Register("platform-connections", platformManager); err != nil {
		log.Fatalf("register platform connection module: %v", err)
	}

	platformService := desktop.NewPlatformService(platformManager, bilibiliEvents, mediaCatalog)
	var wailsApp *application.App
	voiceService, err := voice.NewService(bilibiliEvents, configManager, voiceConfig, func(name string, data any) error {
		if wailsApp == nil {
			return fmt.Errorf("Wails application is not ready")
		}
		window, ok := wailsApp.Window.GetByName("main-window")
		if !ok {
			return fmt.Errorf("main window is not ready")
		}
		window.EmitEvent(name, data)
		return nil
	})
	if err != nil {
		log.Fatalf("initialize voice service: %v", err)
	}
	if err := coreApp.Modules().Register("voice", voice.NewModule(voiceService)); err != nil {
		log.Fatalf("register voice module: %v", err)
	}
	if err := coreApp.Modules().Register("music", desktop.NewMusicModule(musicService)); err != nil {
		log.Fatalf("register music module: %v", err)
	}
	if err := coreApp.Modules().Register("video", desktop.NewVideoModule(videoService)); err != nil {
		log.Fatalf("register video module: %v", err)
	}
	assetHandler := application.AssetFileServerFS(assets)
	imageCache, cacheErr := imagecache.NewWithPolicies(filepath.Join(cacheDir, "media-images"), assetHandler, map[string]imagecache.URLPolicy{"bilibili":imagecache.BilibiliURLPolicy})
	if cacheErr != nil { log.Printf("initialize local media image cache: %v", cacheErr) } else { assetHandler = imageCache; platformService.SetMediaImageCache(imageCache); imageCache.RefreshStats() }
	danmakuDisplayService := desktop.NewDanmakuDisplayService(configManager, danmakuDisplayConfig, danmakuWindowConfig, bilibiliEvents, imageCache)
	app := application.New(application.Options{
		Name:        "My-LiveHub",
		Description: "多平台综合直播互动辅助平台",
		Assets:      application.AssetOptions{Handler: assetHandler},
		Services: []application.Service{
			application.NewService(desktop.NewCoreService(coreApp)),
			application.NewService(platformService),
			application.NewService(desktop.NewBilibiliAuthService(bilibiliAuth, platformManager)),
			application.NewService(desktop.NewBilibiliSettingsService(configManager, bilibiliConfig, bilibiliAdapter)),
			application.NewService(desktop.NewAppearanceService(configManager, appConfig)),
			application.NewService(danmakuDisplayService),
			application.NewService(voiceService),
			application.NewService(musicService),
			application.NewService(videoService),
		},
	})
	wailsApp = app
	desktop.AttachApplication(platformService, app, appConfig.Appearance.Theme == "light")
	desktop.AttachDanmakuDisplayApplication(danmakuDisplayService, app)
	app.OnShutdown(func() {
		danmakuDisplayService.CloseWindowForShutdown()
		if err := videoService.Shutdown(); err != nil {
			log.Printf("stop video playback engine: %v", err)
		}
		if err := musicService.Shutdown(); err != nil {
			log.Printf("stop music playback engine: %v", err)
		}
		if err := mediaCatalog.Close(); err != nil {
			log.Printf("flush local media resource catalog: %v", err)
		}
	})

	background := application.NewRGBA(18, 20, 27, 255)
	if appConfig.Appearance.Theme == "light" {
		background = application.NewRGBA(245, 247, 251, 255)
	}
	mainWindow := app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name: "main-window", Title: "My-LiveHub", Width: 1440, Height: 900,
		MinWidth: 1100, MinHeight: 680, BackgroundColour: background, URL: "/",
	})
	// Restore auxiliary windows only after the main WebView runtime is ready.
	// Creating the transparent overlay before app.Run() can initialise its
	// WebView before the native event loop and break transparent composition.
	mainWindow.OnWindowEvent(events.Common.WindowRuntimeReady, func(_ *application.WindowEvent) {
		if danmakuWindowConfig.OpenOnStartup {
			if err := danmakuDisplayService.OpenWindow(); err != nil {
				log.Printf("restore danmaku overlay window: %v", err)
			}
		}
	})
	// Keep the main window alive until the application-level quit is confirmed.
	// This ensures OnShutdown closes auxiliary windows before the process exits.
	mainWindow.RegisterHook(events.Common.WindowClosing, func(event *application.WindowEvent) {
		event.Cancel()
		go app.Quit()
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}

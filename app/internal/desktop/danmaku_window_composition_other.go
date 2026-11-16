//go:build !windows

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

func restoreDanmakuWindowComposition(_ *application.WebviewWindow, _ bool) bool {
	return true
}

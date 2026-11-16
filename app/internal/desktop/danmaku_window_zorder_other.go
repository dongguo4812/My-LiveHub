//go:build !windows

package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

func restoreNativeTopmost(window *application.WebviewWindow) bool {
	return false
}

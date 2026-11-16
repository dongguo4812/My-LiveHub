//go:build windows

package desktop

import (
	"golang.org/x/sys/windows"

	"github.com/wailsapp/wails/v3/pkg/application"
)

var setWindowPosProc = windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowPos")

const (
	hwndTopmost   = ^uintptr(0)
	swpNoSize     = 0x0001
	swpNoMove     = 0x0002
	swpNoActivate = 0x0010
)

func restoreNativeTopmost(window *application.WebviewWindow) bool {
	if window == nil {
		return false
	}
	hwnd := uintptr(window.NativeWindow())
	if hwnd == 0 {
		return false
	}
	if err := setWindowPosProc.Find(); err != nil {
		return false
	}
	result, _, _ := setWindowPosProc.Call(
		hwnd,
		hwndTopmost,
		0, 0, 0, 0,
		swpNoSize|swpNoMove|swpNoActivate,
	)
	return result != 0
}

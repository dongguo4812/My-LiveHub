//go:build windows

package desktop

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"golang.org/x/sys/windows"
)

var (
	getWindowLongPtrProc = windows.NewLazySystemDLL("user32.dll").NewProc("GetWindowLongPtrW")
	setWindowLongPtrProc = windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowLongPtrW")
	setWindowPosStyleProc = windows.NewLazySystemDLL("user32.dll").NewProc("SetWindowPos")
)

const (
	danmakuGWLExStyle              = ^uintptr(19) // -20
	danmakuWSExTransparent         = uintptr(0x00000020)
	danmakuWSExLayered             = uintptr(0x00080000)
	danmakuWSExNoRedirectionBitmap = uintptr(0x00200000)
	danmakuSWPNoSize                = uintptr(0x0001)
	danmakuSWPNoMove                = uintptr(0x0002)
	danmakuSWPNoZOrder              = uintptr(0x0004)
	danmakuSWPNoActivate            = uintptr(0x0010)
	danmakuSWPFrameChanged          = uintptr(0x0020)
)

// restoreDanmakuWindowComposition restores the extended-style combination
// Wails uses for each transparent-window input mode. Wails v3.1.0's
// SetIgnoreMouseEvents(false) only removes WS_EX_TRANSPARENT; it leaves
// WS_EX_LAYERED set and does not restore WS_EX_NOREDIRECTIONBITMAP.
func restoreDanmakuWindowComposition(window *application.WebviewWindow, ignoreMouse bool) bool {
	if window == nil {
		return false
	}
	hwnd := uintptr(window.NativeWindow())
	if hwnd == 0 {
		return false
	}
	if err := getWindowLongPtrProc.Find(); err != nil {
		return false
	}
	if err := setWindowLongPtrProc.Find(); err != nil {
		return false
	}
	if err := setWindowPosStyleProc.Find(); err != nil {
		return false
	}

	style, _, _ := getWindowLongPtrProc.Call(hwnd, danmakuGWLExStyle)
	exStyle := style
	if ignoreMouse {
		exStyle &^= danmakuWSExNoRedirectionBitmap
		exStyle |= danmakuWSExLayered | danmakuWSExTransparent
	} else {
		exStyle &^= danmakuWSExLayered | danmakuWSExTransparent
		exStyle |= danmakuWSExNoRedirectionBitmap
	}
	if exStyle != style {
		setWindowLongPtrProc.Call(hwnd, danmakuGWLExStyle, exStyle)
	}
	result, _, _ := setWindowPosStyleProc.Call(
		hwnd, 0, 0, 0, 0, 0,
		danmakuSWPNoSize|danmakuSWPNoMove|danmakuSWPNoZOrder|danmakuSWPNoActivate|danmakuSWPFrameChanged,
	)
	return result != 0
}

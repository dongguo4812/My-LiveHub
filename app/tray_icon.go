package main

import _ "embed"

// Use the same source artwork as the Windows executable icon for the system tray.
// Wails' tray API expects encoded image bytes; the PNG is the source image used
// to generate build/icon.ico during the Windows packaging workflow.
//
//go:embed build/icon.png
var trayIcon []byte

func createTrayIcon() []byte {
	return trayIcon
}

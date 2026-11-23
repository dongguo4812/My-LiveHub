package desktop

import "github.com/wailsapp/wails/v3/pkg/application"

// AttachHTMLAudioPlayback connects the MusicService command port to the main
// WebView. Kept as a package helper (not a bound service method) so the Wails
// binding generator never attempts to expose a Go callback parameter.
func AttachHTMLAudioPlayback(service *MusicService, app *application.App) {
	if service == nil { return }
	service.setHTMLAudioEventEmitter(func(name string, data any) {
		if app == nil { return }
		if window, ok := app.Window.GetByName("main-window"); ok {
			window.EmitEvent(name, data)
		}
	})
}

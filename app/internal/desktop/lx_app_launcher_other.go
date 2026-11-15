//go:build !windows

package desktop

// On macOS and Linux, the registered URI handler is the platform-native
// mechanism for opening the installed LX Music desktop application.
func launchLXApp() error {
	return launchLXURI("lxmusic://")
}

//go:build !windows

package desktop

import "fmt"

func readOwnedWebView2Processes(uint32) ([]WebView2ProcessSnapshot, error) {
	return []WebView2ProcessSnapshot{}, fmt.Errorf("WebView2 process inventory is supported on Windows only")
}

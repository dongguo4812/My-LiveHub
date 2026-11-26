//go:build !windows

package desktop

import (
 "fmt"
 "time"
)

func readOwnedWebView2AudioServices(ownerPID int, now time.Time) ([]AudioServiceProcessSnapshot, error) {
 return []AudioServiceProcessSnapshot{}, fmt.Errorf("WebView2 Audio Service process inventory is only supported on Windows")
}

//go:build !windows

package desktop

import "errors"

// CPU sampling currently uses the native Windows process and system APIs.
func readCPUSnapshot() (systemTotal, systemIdle, processTotal uint64, err error) {
	return 0, 0, 0, errors.New("CPU sampling is only implemented on Windows")
}

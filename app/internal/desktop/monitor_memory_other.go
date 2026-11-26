//go:build !windows

package desktop

import "errors"

func readProcessMemorySnapshot() (ProcessMemorySnapshot, error) {
	return ProcessMemorySnapshot{}, errors.New("process memory sampling is only implemented on Windows")
}

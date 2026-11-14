//go:build windows

package desktop

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var getSystemTimesProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetSystemTimes")

func filetimeValue(value windows.Filetime) uint64 {
	return uint64(value.HighDateTime)<<32 | uint64(value.LowDateTime)
}

// readCPUSnapshot returns Windows system times and this process' CPU time.
// All values use 100-nanosecond units, as provided by the Windows API.
func readCPUSnapshot() (systemTotal, systemIdle, processTotal uint64, err error) {
	var idle, kernel, user windows.Filetime
	result, _, callErr := getSystemTimesProc.Call(
		uintptr(unsafe.Pointer(&idle)),
		uintptr(unsafe.Pointer(&kernel)),
		uintptr(unsafe.Pointer(&user)),
	)
	if result == 0 {
		if callErr != syscall.Errno(0) {
			err = callErr
		} else {
			err = syscall.EINVAL
		}
		return
	}

	var creation, exit, processKernel, processUser windows.Filetime
	if err = windows.GetProcessTimes(windows.GetCurrentProcess(), &creation, &exit, &processKernel, &processUser); err != nil {
		return
	}
	systemIdle = filetimeValue(idle)
	systemTotal = filetimeValue(kernel) + filetimeValue(user)
	processTotal = filetimeValue(processKernel) + filetimeValue(processUser)
	return
}

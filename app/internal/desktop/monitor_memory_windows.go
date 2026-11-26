//go:build windows

package desktop

import (
	"errors"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	th32csSnapProcess = 0x00000002
	processQueryInformation = 0x0400
	processVMRead = 0x0010
)

type processEntry32 struct {
	Size              uint32
	Usage             uint32
	ProcessID         uint32
	DefaultHeapID     uintptr
	ModuleID          uint32
	Threads           uint32
	ParentProcessID   uint32
	PriorityClassBase int32
	Flags             uint32
	ExeFile           [windows.MAX_PATH]uint16
}

type processMemoryCountersEx struct {
	CB                         uint32
	PageFaultCount             uint32
	PeakWorkingSetSize         uintptr
	WorkingSetSize             uintptr
	QuotaPeakPagedPoolUsage    uintptr
	QuotaPagedPoolUsage        uintptr
	QuotaPeakNonPagedPoolUsage uintptr
	QuotaNonPagedPoolUsage     uintptr
	PagefileUsage              uintptr
	PeakPagefileUsage          uintptr
	PrivateUsage               uintptr
}

var (
	createToolhelp32SnapshotProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("CreateToolhelp32Snapshot")
	process32FirstWProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("Process32FirstW")
	process32NextWProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("Process32NextW")
	getProcessMemoryInfoProc = windows.NewLazySystemDLL("psapi.dll").NewProc("GetProcessMemoryInfo")
)

func readProcessMemorySnapshot() (ProcessMemorySnapshot, error) {
	currentPID := uint32(windows.GetCurrentProcessId())
	processes, err := snapshotProcesses()
	if err != nil {
		return ProcessMemorySnapshot{}, err
	}
	children := make(map[uint32][]uint32, len(processes))
	for _, process := range processes {
		children[process.ParentProcessID] = append(children[process.ParentProcessID], process.ProcessID)
	}
	owned := make(map[uint32]struct{})
	queue := append([]uint32(nil), children[currentPID]...)
	for len(queue) > 0 {
		pid := queue[0]
		queue = queue[1:]
		if _, exists := owned[pid]; exists {
			continue
		}
		owned[pid] = struct{}{}
		queue = append(queue, children[pid]...)
	}
	result := ProcessMemorySnapshot{ProcessID: currentPID}
	current, err := readOneProcessMemory(windows.CurrentProcess)
	if err != nil {
		return ProcessMemorySnapshot{}, err
	}
	result.ProcessWorkingSetBytes = current.WorkingSetBytes
	result.ProcessPrivateBytes = current.PrivateBytes
	for _, process := range processes {
		if _, ok := owned[process.ProcessID]; !ok {
			continue
		}
		if !strings.EqualFold(windows.UTF16ToString(process.ExeFile[:]), "msedgewebview2.exe") {
			continue
		}
		memory, memoryErr := readOneProcessMemory(windows.Handle(process.ProcessID))
		if memoryErr != nil {
			// A child can exit between process enumeration and OpenProcess.
			// Ignore only that per-process race; the next sample will reconcile it.
			continue
		}
		result.WebView2ProcessCount++
		result.WebView2WorkingSetBytes += memory.WorkingSetBytes
		result.WebView2PrivateBytes += memory.PrivateBytes
	}
	return result, nil
}

func snapshotProcesses() ([]processEntry32, error) {
	handle, _, callErr := createToolhelp32SnapshotProc.Call(th32csSnapProcess, 0)
	if windows.Handle(handle) == windows.InvalidHandle {
		if callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, errors.New("CreateToolhelp32Snapshot failed")
	}
	defer windows.CloseHandle(windows.Handle(handle))
	entry := processEntry32{Size: uint32(unsafe.Sizeof(processEntry32{}))}
	ok, _, firstErr := process32FirstWProc.Call(handle, uintptr(unsafe.Pointer(&entry)))
	if ok == 0 {
		if firstErr != syscall.Errno(0) {
			return nil, firstErr
		}
		return nil, errors.New("Process32FirstW failed")
	}
	var result []processEntry32
	for {
		result = append(result, entry)
		entry = processEntry32{Size: uint32(unsafe.Sizeof(processEntry32{}))}
		ok, _, nextErr := process32NextWProc.Call(handle, uintptr(unsafe.Pointer(&entry)))
		if ok == 0 {
			if nextErr != syscall.Errno(0) && nextErr != windows.ERROR_NO_MORE_FILES {
				return nil, nextErr
			}
			break
		}
	}
	return result, nil
}

type oneProcessMemory struct {
	WorkingSetBytes uint64
	PrivateBytes    uint64
}

func readOneProcessMemory(process windows.Handle) (oneProcessMemory, error) {
	var counters processMemoryCountersEx
	counters.CB = uint32(unsafe.Sizeof(counters))
	ok, _, callErr := getProcessMemoryInfoProc.Call(
		uintptr(process),
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.CB),
	)
	if ok == 0 {
		if callErr != syscall.Errno(0) {
			return oneProcessMemory{}, callErr
		}
		return oneProcessMemory{}, errors.New("GetProcessMemoryInfo failed")
	}
	return oneProcessMemory{
		WorkingSetBytes: uint64(counters.WorkingSetSize),
		PrivateBytes:    uint64(counters.PrivateUsage),
	}, nil
}

func readCurrentProcessMemory() (oneProcessMemory, error) {
	return readOneProcessMemory(windows.CurrentProcess)
}

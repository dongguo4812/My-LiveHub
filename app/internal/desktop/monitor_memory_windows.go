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
	processQueryLimitedInformation = 0x1000
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

type unicodeString struct {
	Length uint16
	MaximumLength uint16
	Buffer uintptr
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
	ntQueryInformationProcessProc = windows.NewLazySystemDLL("ntdll.dll").NewProc("NtQueryInformationProcess")
)

func readProcessMemorySnapshot() (ProcessMemorySnapshot, error) {
	currentPID := uint32(windows.GetCurrentProcessId())
	processes, err := snapshotProcesses()
	if err != nil {
		return ProcessMemorySnapshot{}, err
	}
	parents := make([]processParent, 0, len(processes))
	for _, process := range processes {
		parents = append(parents, processParent{PID: process.ProcessID, ParentPID: process.ParentProcessID})
	}
	owned := collectDescendantProcessIDs(currentPID, parents)
	result := ProcessMemorySnapshot{ProcessID: currentPID}
	current, err := readOneProcessMemory(windows.CurrentProcess())
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
		memory, memoryErr := readPIDProcessMemory(process.ProcessID)
		if memoryErr != nil {
			// A child can exit between process enumeration and OpenProcess.
			// Ignore only that per-process race; the next sample will reconcile it.
			continue
		}
		result.WebView2ProcessCount++
		result.WebView2WorkingSetBytes += memory.WorkingSetBytes
		result.WebView2PrivateBytes += memory.PrivateBytes
		commandLine, commandLineErr := readPIDProcessCommandLine(process.ProcessID)
		if commandLineErr != nil {
			result.WebView2UnclassifiedProcessCount++
		} else if isWebView2RendererCommandLine(commandLine) {
			result.WebView2RendererCount++
			result.WebView2RendererWorkingSetBytes += memory.WorkingSetBytes
			result.WebView2RendererPrivateBytes += memory.PrivateBytes
		}
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

func readPIDProcessMemory(pid uint32) (oneProcessMemory, error) {
	handle, err := windows.OpenProcess(processQueryInformation|processVMRead, false, pid)
	if err != nil {
		return oneProcessMemory{}, err
	}
	defer windows.CloseHandle(handle)
	return readOneProcessMemory(handle)
}

func readPIDProcessCommandLine(pid uint32) (string, error) {
	handle, err := windows.OpenProcess(processQueryLimitedInformation, false, pid)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	// ProcessCommandLineInformation is available on supported Windows 10+
	// systems and returns a UNICODE_STRING within the caller-provided buffer.
	const processCommandLineInformation = 60
	buffer := make([]byte, 64*1024)
	var returned uint32
	status, _, _ := ntQueryInformationProcessProc.Call(
		uintptr(handle),
		processCommandLineInformation,
		uintptr(unsafe.Pointer(&buffer[0])),
		uintptr(len(buffer)),
		uintptr(unsafe.Pointer(&returned)),
	)
	if int32(status) < 0 {
		return "", syscall.Errno(uint32(status))
	}
	if returned < uint32(unsafe.Sizeof(unicodeString{})) {
		return "", errors.New("NtQueryInformationProcess returned a truncated command line")
	}
	value := (*unicodeString)(unsafe.Pointer(&buffer[0]))
	base := uintptr(unsafe.Pointer(&buffer[0]))
	if value.Length == 0 || value.Buffer < base || value.Buffer+uintptr(value.Length) > base+uintptr(len(buffer)) {
		return "", errors.New("NtQueryInformationProcess returned an invalid command line buffer")
	}
	units := unsafe.Slice((*uint16)(unsafe.Pointer(value.Buffer)), int(value.Length/2))
	return windows.UTF16ToString(units), nil
}

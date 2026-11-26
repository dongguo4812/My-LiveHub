//go:build windows

package desktop

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func readOwnedWebView2Processes(ownerPID uint32) ([]WebView2ProcessSnapshot, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil { return nil, fmt.Errorf("create process snapshot: %w", err) }
	defer windows.CloseHandle(snapshot)

	entry := windows.ProcessEntry32{Size: uint32(windows.SizeofProcessEntry32)}
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, fmt.Errorf("read first process entry: %w", err)
	}
	entries := make([]processEntry, 0, 32)
	for {
		entries = append(entries, processEntry{
			pid: entry.ProcessID, parentPID: entry.ParentProcessID,
			executable: windows.UTF16ToString(entry.ExeFile[:]),
		})
		err = windows.Process32Next(snapshot, &entry)
		if err == windows.ERROR_NO_MORE_FILES { break }
		if err != nil { return nil, fmt.Errorf("read process entry: %w", err) }
	}
	return ownedWebView2Processes(ownerPID, entries), nil
}

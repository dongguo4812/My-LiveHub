package desktop

import (
	"path/filepath"
	"sort"
	"strings"
)

type processEntry struct {
	pid uint32
	parentPID uint32
	executable string
}

// ownedWebView2Processes attributes only WebView2 processes reachable from the
// application PID through parent-child links in the same process snapshot.
func ownedWebView2Processes(ownerPID uint32, entries []processEntry) []WebView2ProcessSnapshot {
	if ownerPID == 0 { return []WebView2ProcessSnapshot{} }
	byPID := make(map[uint32]processEntry, len(entries))
	for _, entry := range entries {
		if entry.pid != 0 { byPID[entry.pid] = entry }
	}
	owned := map[uint32]struct{}{ownerPID: {}}
	result := make([]WebView2ProcessSnapshot, 0)
	remaining := make(map[uint32]processEntry)
	for pid, entry := range byPID {
		if strings.EqualFold(filepath.Base(entry.executable), "msedgewebview2.exe") {
			remaining[pid] = entry
		}
	}
	for len(remaining) > 0 {
		progress := false
		for pid, entry := range remaining {
			if _, ok := owned[entry.parentPID]; !ok { continue }
			owned[pid] = struct{}{}
			result = append(result, WebView2ProcessSnapshot{
				PID: entry.pid, ParentPID: entry.parentPID, Executable: filepath.Base(entry.executable),
			})
			delete(remaining, pid)
			progress = true
		}
		if !progress { break }
	}
	sort.Slice(result, func(i, j int) bool { return result[i].PID < result[j].PID })
	return result
}

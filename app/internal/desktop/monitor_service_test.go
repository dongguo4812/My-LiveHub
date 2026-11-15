package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func writeMonitorTestLog(t *testing.T, path string, lines ...string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(joinMonitorTestLines(lines)), 0o600); err != nil {
		t.Fatalf("write log file: %v", err)
	}
}

func joinMonitorTestLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := ""
	for _, line := range lines {
		result += line + "\n"
	}
	return result
}

func TestGetLogsByDateReturnsNewestFirstAcrossRotations(t *testing.T) {
	dir := t.TempDir()
	writeMonitorTestLog(t, filepath.Join(dir, "my-livehub-2026-11-15.log.1"),
		"2026/11/15 08:00:00.000000 older-rotation-first",
		"2026/11/15 08:01:00.000000 older-rotation-second",
	)
	writeMonitorTestLog(t, filepath.Join(dir, "my-livehub-2026-11-15.log"),
		"2026/11/15 08:02:00.000000 active-first",
		"2026/11/15 08:03:00.000000 active-latest",
	)

	entries := NewMonitorService(dir).GetLogsByDate("2026-11-15")
	want := []string{"active-latest", "active-first", "older-rotation-second", "older-rotation-first"}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %#v", len(entries), len(want), entries)
	}
	for i, message := range want {
		if entries[i].Message != message {
			t.Errorf("entry %d = %q, want %q", i, entries[i].Message, message)
		}
	}
}

func TestGetRecentLogsReturnsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	writeMonitorTestLog(t, filepath.Join(dir, "my-livehub-2026-11-14.log"),
		"2026/11/14 23:58:00.000000 previous-day-latest",
	)
	writeMonitorTestLog(t, filepath.Join(dir, "my-livehub-2026-11-15.log.1"),
		"2026/11/15 08:00:00.000000 rotated-older",
	)
	writeMonitorTestLog(t, filepath.Join(dir, "my-livehub-2026-11-15.log"),
		"2026/11/15 08:01:00.000000 active-older",
		"2026/11/15 08:02:00.000000 active-latest",
	)

	entries := NewMonitorService(dir).GetRecentLogs(10)
	want := []string{"active-latest", "active-older", "rotated-older", "previous-day-latest"}
	if len(entries) != len(want) {
		t.Fatalf("got %d entries, want %d: %#v", len(entries), len(want), entries)
	}
	for i, message := range want {
		if entries[i].Message != message {
			t.Errorf("entry %d = %q, want %q", i, entries[i].Message, message)
		}
	}
}

func TestGetLogsByDateMergesExtensionLogsNewestFirst(t *testing.T) {
	mainDir := t.TempDir()
	extensionDir := t.TempDir()
	writeMonitorTestLog(t, filepath.Join(mainDir, "my-livehub-2026-11-15.log"),
		"2026/11/15 08:00:00.000000 main-log",
	)
	writeMonitorTestLog(t, filepath.Join(extensionDir, "2026-11-15.log"),
		"2026-11-15 08:05:00 [WARNING] [lol_hex_display] extension-log",
	)

	service := NewMonitorService(mainDir, extensionDir)
	dates := service.GetLogDates()
	if len(dates) != 1 || dates[0] != "2026-11-15" {
		t.Fatalf("dates = %#v, want one merged date", dates)
	}
	entries := service.GetLogsByDate("2026-11-15")
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %#v", len(entries), entries)
	}
	if entries[0].Message == "" || entries[0].Time != "2026-11-15 08:05:00" || entries[0].Level != "WARN" {
		t.Fatalf("newest extension entry = %#v", entries[0])
	}
	if entries[1].Message == "" || entries[1].Time != "2026/11/15 08:00:00.000000" {
		t.Fatalf("older main entry = %#v", entries[1])
	}
}

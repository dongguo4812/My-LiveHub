package desktop

import "testing"

func TestOwnedWebView2ProcessesFollowsOnlyApplicationProcessTree(t *testing.T) {
	entries := []processEntry{
		{pid: 100, parentPID: 1, executable: "app.exe"},
		{pid: 110, parentPID: 100, executable: "msedgewebview2.exe"},
		{pid: 111, parentPID: 110, executable: "msedgewebview2.exe"},
		{pid: 112, parentPID: 111, executable: "msedgewebview2.exe"},
		{pid: 200, parentPID: 1, executable: "other-app.exe"},
		{pid: 210, parentPID: 200, executable: "msedgewebview2.exe"},
		{pid: 211, parentPID: 210, executable: "msedgewebview2.exe"},
		{pid: 300, parentPID: 999, executable: "msedgewebview2.exe"},
		{pid: 400, parentPID: 100, executable: "not-webview.exe"},
	}
	got := ownedWebView2Processes(100, entries)
	if len(got) != 3 { t.Fatalf("got %d owned processes, want 3: %#v", len(got), got) }
	want := []uint32{110, 111, 112}
	for i, pid := range want {
		if got[i].PID != pid { t.Errorf("process %d PID = %d, want %d", i, got[i].PID, pid) }
	}
}

func TestOwnedWebView2ProcessesDoesNotAttributeByNameAlone(t *testing.T) {
	got := ownedWebView2Processes(100, []processEntry{
		{pid: 210, parentPID: 200, executable: "msedgewebview2.exe"},
		{pid: 211, parentPID: 210, executable: "msedgewebview2.exe"},
	})
	if len(got) != 0 { t.Fatalf("attributed unrelated WebView2 processes: %#v", got) }
}

func TestOwnedWebView2ProcessesReturnsEmptyForMissingOwner(t *testing.T) {
	got := ownedWebView2Processes(0, []processEntry{{pid: 110, parentPID: 0, executable: "msedgewebview2.exe"}})
	if len(got) != 0 { t.Fatalf("got processes without an owner PID: %#v", got) }
}

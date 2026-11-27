package desktop

import "testing"

func TestCollectDescendantProcessIDsExcludesOtherApplications(t *testing.T) {
	processes := []processParent{
		{PID: 200, ParentPID: 100}, // app-owned WebView2 browser
		{PID: 201, ParentPID: 200}, // app-owned WebView2 renderer
		{PID: 202, ParentPID: 201}, // nested app-owned utility
		{PID: 300, ParentPID: 999}, // another application's WebView2 browser
		{PID: 301, ParentPID: 300}, // another application's renderer
	}
	got := collectDescendantProcessIDs(100, processes)
	for _, pid := range []uint32{200, 201, 202} {
		if _, ok := got[pid]; !ok {
			t.Errorf("owned descendant PID %d was not included", pid)
		}
	}
	for _, pid := range []uint32{100, 300, 301, 999} {
		if _, ok := got[pid]; ok {
			t.Errorf("unrelated/root PID %d was incorrectly included", pid)
		}
	}
}

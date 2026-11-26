//go:build windows

package desktop

import (
	"testing"
)

func TestReadProcessMemorySnapshotMeasuresCurrentProcess(t *testing.T) {
	snapshot, err := readProcessMemorySnapshot()
	if err != nil {
		t.Fatalf("read process memory snapshot: %v", err)
	}
	if snapshot.ProcessID == 0 {
		t.Fatal("process ID is zero")
	}
	if snapshot.ProcessWorkingSetBytes == 0 {
		t.Fatal("current process working set is zero")
	}
	if snapshot.ProcessPrivateBytes == 0 {
		t.Fatal("current process private bytes is zero")
	}
	if snapshot.WebView2ProcessCount < 0 || snapshot.WebView2WorkingSetBytes < 0 {
		t.Fatalf("invalid WebView2 metrics: %#v", snapshot)
	}
}

func TestSystemMetricsCapturesMemoryBaseline(t *testing.T) {
	service := NewMonitorService(t.TempDir())
	first := service.GetSystemMetrics()
	if first.ProcessMemoryError != "" {
		t.Fatalf("first process memory sample failed: %s", first.ProcessMemoryError)
	}
	if !first.MemoryBaselineReady {
		t.Fatal("first successful sample did not establish a memory baseline")
	}
	if first.ProcessWorkingSetDeltaBytes != 0 || first.WebView2WorkingSetDeltaBytes != 0 {
		t.Fatalf("first sample deltas should be zero: %#v", first)
	}
	second := service.GetSystemMetrics()
	if second.ProcessMemoryError != "" || !second.MemoryBaselineReady {
		t.Fatalf("second process memory sample is not healthy: %#v", second)
	}
	if second.ProcessID != first.ProcessID {
		t.Fatalf("process ID changed between samples: %d -> %d", first.ProcessID, second.ProcessID)
	}
}

//go:build windows

package desktop

import (
	"testing"

	"golang.org/x/sys/windows"
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
	if first.MemorySampleCount != 1 || first.ProcessWorkingSetPeakBytes != first.ProcessWorkingSetBytes || first.ProcessPrivatePeakBytes != first.ProcessPrivateBytes {
		t.Fatalf("first sample did not initialize process peaks and sample count: %#v", first)
	}
	if first.ProcessWorkingSetDeltaBytes != 0 || first.ProcessPrivateDeltaBytes != 0 || first.WebView2WorkingSetDeltaBytes != 0 || first.WebView2PrivateDeltaBytes != 0 || first.WebView2RendererWorkingSetDeltaBytes != 0 || first.WebView2RendererPrivateDeltaBytes != 0 {
		t.Fatalf("first sample deltas should be zero: %#v", first)
	}
	second := service.GetSystemMetrics()
	if second.ProcessMemoryError != "" || !second.MemoryBaselineReady {
		t.Fatalf("second process memory sample is not healthy: %#v", second)
	}
	if second.ProcessID != first.ProcessID {
		t.Fatalf("process ID changed between samples: %d -> %d", first.ProcessID, second.ProcessID)
	}
	if second.MemorySampleCount != 2 || second.ProcessWorkingSetPeakBytes < second.ProcessWorkingSetBytes || second.ProcessPrivatePeakBytes < second.ProcessPrivateBytes || second.WebView2WorkingSetPeakBytes < second.WebView2WorkingSetBytes || second.WebView2RendererWorkingSetPeakBytes < second.WebView2RendererWorkingSetBytes {
		t.Fatalf("memory peaks/sample count were not retained: %#v", second)
	}
}

func TestReadProcessCommandLineForCurrentProcess(t *testing.T) {
	commandLine, err := readPIDProcessCommandLine(windows.GetCurrentProcessId())
	if err != nil {
		t.Fatalf("read current process command line: %v", err)
	}
	if commandLine == "" {
		t.Fatal("current process command line is empty")
	}
}

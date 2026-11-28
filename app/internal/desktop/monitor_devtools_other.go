//go:build !windows

package desktop

import (
	"fmt"
	"time"
)

type RendererMemoryTraceResult struct {
	Path       string `json:"path"`
	EventCount int    `json:"event_count"`
	DumpGUID   string `json:"dump_guid"`
	CapturedAt time.Time `json:"captured_at"`
}

func (s *MonitorService) SetDevToolsPort(port int) {}

func (s *MonitorService) CaptureWebView2MemoryTrace() (RendererMemoryTraceResult, error) {
	return RendererMemoryTraceResult{}, fmt.Errorf("Chromium MemoryInfra diagnostics are currently supported on Windows development builds only")
}

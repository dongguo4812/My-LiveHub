//go:build !windows

package desktop

import (
	"fmt"
	"time"
)

type RendererDOMTargetResult struct {
 TargetID string `json:"target_id"`
 Title string `json:"title"`
 URL string `json:"url"`
 Type string `json:"type"`
 Documents int `json:"documents"`
 Nodes int `json:"nodes"`
 JSEventListeners int `json:"js_event_listeners"`
 Error string `json:"error,omitempty"`
}
type RendererDOMDiagnosticsResult struct {
 Path string `json:"path"`
 TargetCount int `json:"target_count"`
 CapturedAt time.Time `json:"captured_at"`
 Targets []RendererDOMTargetResult `json:"targets"`
}

type RendererMemoryTraceResult struct {
	Path       string    `json:"path"`
	EventCount int       `json:"event_count"`
	DumpGUID   string    `json:"dump_guid"`
	CapturedAt time.Time `json:"captured_at"`
}

func (s *MonitorService) SetDevToolsPort(port int) {}

func (s *MonitorService) CaptureWebView2DOMDiagnostics() (RendererDOMDiagnosticsResult, error) {
 return RendererDOMDiagnosticsResult{}, fmt.Errorf("Chromium DOM diagnostics are currently supported on Windows development builds only")
}

func (s *MonitorService) CaptureWebView2MemoryTrace() (RendererMemoryTraceResult, error) {
	return RendererMemoryTraceResult{}, fmt.Errorf("Chromium MemoryInfra diagnostics are currently supported on Windows development builds only")
}

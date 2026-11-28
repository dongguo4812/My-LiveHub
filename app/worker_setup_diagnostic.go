package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/dongguo4812/My-LiveHub/app/internal/desktop"
)

var workerDiagnosticProcessStartedAt = time.Now()

// workerSetupDiagnosticHandler adds application context only to the Wails
// Worker request-cancellation setup error. It never changes the log level,
// message, or WebView2/Worker behaviour.
type workerSetupDiagnosticHandler struct {
	next     slog.Handler
	snapshot func() []slog.Attr
}

func newWorkerSetupDiagnosticHandler(next slog.Handler, environment string, monitor *desktop.MonitorService) slog.Handler {
	return &workerSetupDiagnosticHandler{
		next: next,
		snapshot: func() []slog.Attr {
			heartbeat := monitor.GetFrontendHeartbeatSnapshot()
			windows := monitor.GetWebView2Instances()
			windowJSON, _ := json.Marshal(windows)

			lastHeartbeat := "never"
			heartbeatAgeMS := int64(-1)
			if !heartbeat.LastSeenAt.IsZero() {
				lastHeartbeat = heartbeat.LastSeenAt.Format(time.RFC3339Nano)
				heartbeatAgeMS = time.Since(heartbeat.LastSeenAt).Milliseconds()
			}

			return []slog.Attr{
				slog.String("worker_diag_environment", environment),
				slog.Int("worker_diag_pid", os.Getpid()),
				slog.String("worker_diag_go_version", runtime.Version()),
				slog.String("worker_diag_goos", runtime.GOOS),
				slog.String("worker_diag_goarch", runtime.GOARCH),
				slog.Int64("worker_diag_process_uptime_ms", time.Since(workerDiagnosticProcessStartedAt).Milliseconds()),
				slog.Int("worker_diag_goroutines", runtime.NumGoroutine()),
				slog.Bool("worker_diag_frontend_heartbeat_stale", heartbeat.Stale),
				slog.String("worker_diag_frontend_heartbeat_last_seen", lastHeartbeat),
				slog.Int64("worker_diag_frontend_heartbeat_age_ms", heartbeatAgeMS),
				slog.String("worker_diag_webview_windows", string(windowJSON)),
				slog.String("worker_diag_cdp_detail", "Wails v3.1.0 does not include the failing CDP method or Worker target metadata in this error callback"),
			}
		},
	}
}

func (h *workerSetupDiagnosticHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *workerSetupDiagnosticHandler) Handle(ctx context.Context, record slog.Record) error {
	if strings.Contains(record.Message, "Worker request cancellation setup") && h.snapshot != nil {
		record.AddAttrs(h.snapshot()...)
	}
	return h.next.Handle(ctx, record)
}

func (h *workerSetupDiagnosticHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &workerSetupDiagnosticHandler{next: h.next.WithAttrs(attrs), snapshot: h.snapshot}
}

func (h *workerSetupDiagnosticHandler) WithGroup(name string) slog.Handler {
	return &workerSetupDiagnosticHandler{next: h.next.WithGroup(name), snapshot: h.snapshot}
}

//go:build lxquickjs || lxproduction

package lxruntime

import (
    "context"
    "errors"
    "log"
    "time"
)

func timingResult(err error) string {
    if err == nil { return "ok" }
    if errors.Is(err, context.Canceled) { return "canceled" }
    if errors.Is(err, context.DeadlineExceeded) { return "deadline" }
    return "error"
}

// logLXTiming emits bounded diagnostics; callers must never pass URLs,
// headers, cookies, signatures, or request/response payloads.
func logLXTiming(stage string, started time.Time, fields string) {
    log.Printf("[LX-Timing] stage=%s duration_ms=%.3f %s", stage, float64(time.Since(started))/float64(time.Millisecond), fields)
}

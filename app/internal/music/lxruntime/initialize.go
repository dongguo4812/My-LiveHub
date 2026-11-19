//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// InitializeLXSource installs the desktop host, evaluates a source, and pumps
// initialization-time timers and lx.request calls until the source registers
// its request handler and sends the inited event.
func (r *Runtime) InitializeLXSource(ctx context.Context, info ScriptInfo, source string, client *http.Client) error {
	if r == nil {
		return errors.New("LX initialization: runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(source) == "" {
		return errors.New("LX initialization: source script is empty")
	}
	if client == nil {
		client = http.DefaultClient
	}
	if err := r.InstallLXHost(ctx, info); err != nil {
		return fmt.Errorf("LX initialization: install host: %w", err)
	}
	filename := info.Name
	if filename == "" {
		filename = "lx-source.js"
	}
	if _, err := r.Eval(ctx, source, filename); err != nil {
		return fmt.Errorf("LX initialization: evaluate source: %w", err)
	}

	const maxRequests = 256
	processed := 0
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Separate evals are intentional: each Eval drains QuickJS Promise jobs.
		if _, err := r.Eval(ctx, "globalThis.__lxRunDueTimers(); 'timers-pumped';", "lx-init-timers.js"); err != nil {
			return fmt.Errorf("LX initialization: pump timers: %w", err)
		}
		stateText, err := r.Eval(ctx, `
JSON.stringify({
  initialized: !!globalThis.__lxHostState.initialized,
  requestHandler: typeof globalThis.__lxHostHandlers.request,
  nextTimerDelay: globalThis.__lxNextTimerDelay(),
  requests: globalThis.__lxTakeRequests()
})
`, "lx-init-poll.js")
		if err != nil {
			return fmt.Errorf("LX initialization: poll host: %w", err)
		}
		var state struct {
			Initialized    bool                  `json:"initialized"`
			RequestHandler string                `json:"requestHandler"`
			NextTimerDelay int64                 `json:"nextTimerDelay"`
			Requests       []lxRequestDescriptor `json:"requests"`
		}
		if err := json.Unmarshal([]byte(stateText), &state); err != nil {
			return fmt.Errorf("LX initialization: decode host state: %w", err)
		}
		if len(state.Requests) > 0 {
			for _, request := range state.Requests {
				processed++
				if processed > maxRequests {
					return fmt.Errorf("LX initialization: request limit (%d) exceeded", maxRequests)
				}
				response, requestErr := executeLXHTTPRequest(ctx, request, client)
				if err := r.completeLXRequest(ctx, request.ID, response, requestErr); err != nil {
					return err
				}
			}
			continue
		}
		if state.Initialized && state.RequestHandler == "function" {
			return nil
		}
		if state.NextTimerDelay < 0 {
			return fmt.Errorf("LX initialization: source did not initialize (initialized=%t, requestHandler=%s, requests=%d)", state.Initialized, state.RequestHandler, processed)
		}
		wait := time.Duration(state.NextTimerDelay) * time.Millisecond
		if wait > 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		if wait < time.Millisecond {
			wait = time.Millisecond
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

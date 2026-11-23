//go:build (lxquickjs || lxproduction) && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// InitializeLXSource installs the desktop host, evaluates a source, and pumps
// initialization-time timers and lx.request calls until the source registers
// its request handler and sends the inited event.
func (r *Runtime) InitializeLXSource(ctx context.Context, info ScriptInfo, source string, client *http.Client) error {
	r.initializeMu.Lock()
	defer r.initializeMu.Unlock()
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
	tasks := newLXHTTPTasks(client)
	defer tasks.cancelAll()
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
  requests: globalThis.__lxTakeRequests(),
  aborts: globalThis.__lxTakeAborts()
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
			Aborts         []uint64 `json:"aborts"`
		}
		if err := json.Unmarshal([]byte(stateText), &state); err != nil {
			return fmt.Errorf("LX initialization: decode host state: %w", err)
		}
		tasks.abort(state.Aborts)
		if len(state.Requests) > 0 {
			processed += len(state.Requests)
			if processed > maxRequests {
				return fmt.Errorf("LX initialization: request limit (%d) exceeded", maxRequests)
			}
			if err := tasks.start(ctx, state.Requests); err != nil {
				return err
			}
		}
		select {
		case result := <-tasks.completed:
			if err := tasks.finish(ctx, r, result); err != nil {
				return err
			}
		default:
		}
		if state.Initialized && state.RequestHandler == "function" && len(tasks.active) == 0 {
			return nil
		}
		if !state.Initialized && len(tasks.active) == 0 && state.NextTimerDelay < 0 {
			return fmt.Errorf("LX initialization: source did not initialize (initialized=%t, requestHandler=%s, requests=%d)", state.Initialized, state.RequestHandler, processed)
		}
		result, waitErr := tasks.wait(ctx, state.NextTimerDelay)
		if waitErr != nil {
			return waitErr
		}
		if result != nil {
			if err := tasks.finish(ctx, r, *result); err != nil {
				return err
			}
		}
	}
}

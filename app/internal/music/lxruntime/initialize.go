//go:build lxquickjs || lxproduction

package lxruntime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const maxLXScriptSourceBytes = 2 << 20

func ValidateLXScriptSource(source string) error {
	if strings.TrimSpace(source) == "" {
		return errors.New("LX script source is empty")
	}
	if len(source) > maxLXScriptSourceBytes {
		return fmt.Errorf("LX script source exceeds %d-byte limit", maxLXScriptSourceBytes)
	}
	return nil
}

// InitializeLXSource installs the desktop host, evaluates a source, and pumps
// initialization-time timers and lx.request calls until the source registers
// its request handler and sends the inited event.
func (r *Runtime) InitializeLXSource(ctx context.Context, info ScriptInfo, source string, client *http.Client) error {
	return r.InitializeLXSourceWithCache(ctx, info, source, client, nil)
}

// InitializeLXSourceWithCache reuses the verified rotation count for the exact
// script hash. Cache misses execute the original loop and update the cache only
// after the complete source evaluation succeeds.
func (r *Runtime) InitializeLXSourceWithCache(ctx context.Context, info ScriptInfo, source string, client *http.Client, cache *LXObfuscationCache) error {
	if r == nil {
		return errors.New("LX initialization: runtime is nil")
	}
	r.initializeMu.Lock()
	defer r.initializeMu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ValidateLXScriptSource(source); err != nil {
		return fmt.Errorf("LX initialization: %w", err)
	}
	if len(info.RawScript) > maxLXScriptSourceBytes {
		return fmt.Errorf("LX initialization: script metadata exceeds %d-byte limit", maxLXScriptSourceBytes)
	}
	if client == nil {
		client = http.DefaultClient
	}
	hostStarted := time.Now()
	hostErr := r.InstallLXHost(ctx, info)
	logLXTiming("initialize_host_install", hostStarted, "result="+timingResult(hostErr))
	if hostErr != nil {
		return fmt.Errorf("LX initialization: install host: %w", hostErr)
	}
	filename := info.Name
	if filename == "" {
		filename = "lx-source.js"
	}
	cacheSource := source
	evalSource, cacheHit, cacheable := prepareLXObfuscationCacheSource(source, cache)
	if cacheHit {
		log.Printf("[LX-Obfuscation] script=%q cache=hit sha256=%s rotations=%d", filename, cache.SourceSHA256, cache.RotationCount)
	} else if cacheable {
		log.Printf("[LX-Obfuscation] script=%q cache=miss sha256=%s", filename, lxSourceSHA256(source))
	}
	profileObfuscation := os.Getenv(lxObfuscationProfileEnv) == "1"
	if profileObfuscation {
		var instrumented bool
		evalSource, instrumented = instrumentLXObfuscationProfile(evalSource)
		if instrumented {
			log.Printf("[LX-Obfuscation] script=%q profiling=enabled", filename)
		} else {
			log.Printf("[LX-Obfuscation] script=%q profiling=unsupported_source_shape", filename)
		}
	}
	sourceEvalStarted := time.Now()
	_, sourceEvalErr := r.EvalWithTimeout(ctx, evalSource, filename, maxInitializationEvalTimeout)
	logLXTiming("initialize_source_eval", sourceEvalStarted, "script="+fmt.Sprintf("%q", filename)+" result="+timingResult(sourceEvalErr))
	if sourceEvalErr != nil {
		return fmt.Errorf("LX initialization: evaluate source: %w", sourceEvalErr)
	}
	if cache != nil && cacheable && !cacheHit {
		rotationText, readErr := r.Eval(ctx, "String(globalThis."+lxObfuscationCacheGlobal+" || 0)", "lx-obfuscation-cache-read.js")
		if readErr != nil {
			return fmt.Errorf("LX initialization: read obfuscation cache result: %w", readErr)
		}
		rotations, parseErr := strconv.Atoi(rotationText)
		if parseErr != nil || rotations < 0 || rotations > 1_000_000 {
			return fmt.Errorf("LX initialization: invalid obfuscation rotation count %q", rotationText)
		}
		cache.Version = lxObfuscationCacheVersion
		cache.SourceSHA256 = lxSourceSHA256(cacheSource)
		cache.RotationCount = rotations
		log.Printf("[LX-Obfuscation] script=%q cache=stored sha256=%s rotations=%d", filename, cache.SourceSHA256, rotations)
	}
	if profileObfuscation {
		r.logLXObfuscationProfile(ctx, filename)
	}

	const maxRequests = 256
	processed := 0
	tasks := newLXHTTPTasksWithPolicy(client, r.allowPrivateNetworkForTests)
	defer tasks.cancelAll()
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Separate evals are intentional: each Eval drains QuickJS Promise jobs.
		timerPumpStarted := time.Now()
		_, timerPumpErr := r.Eval(ctx, "globalThis.__lxRunDueTimers(); 'timers-pumped';", "lx-init-timers.js")
		logLXTiming("initialize_timer_pump", timerPumpStarted, "result="+timingResult(timerPumpErr))
		if timerPumpErr != nil {
			return fmt.Errorf("LX initialization: pump timers: %w", timerPumpErr)
		}
		pollStarted := time.Now()
		stateText, err := r.Eval(ctx, `
JSON.stringify({
  initialized: !!globalThis.__lxHostState.initialized,
  requestHandler: typeof globalThis.__lxHostHandlers.request,
  nextTimerDelay: globalThis.__lxNextTimerDelay(),
  requests: globalThis.__lxTakeRequests(),
  aborts: globalThis.__lxTakeAborts()
})
`, "lx-init-poll.js")
		logLXTiming("initialize_host_poll", pollStarted, "result="+timingResult(err))
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
		waitStarted := time.Now()
		result, waitErr := tasks.wait(ctx, state.NextTimerDelay)
		logLXTiming("initialize_wait_timer_or_http", waitStarted, fmt.Sprintf("next_timer_ms=%d result=%q", state.NextTimerDelay, timingResult(waitErr)))
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

//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLXTimerClearTimeoutAndIntervalRescheduling(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "timer-contract"}); err != nil {
		t.Fatal(err)
	}
	script := `
globalThis.__timerEvents = [];
const canceled = setTimeout(() => __timerEvents.push('canceled'), 0);
clearTimeout(canceled);
let ticks = 0;
const interval = setInterval(() => {
  ticks++;
  __timerEvents.push('tick-' + ticks);
  if (ticks === 2) clearInterval(interval);
}, 1);
JSON.stringify(__timerEvents);
`
	if _, err := rt.Eval(context.Background(), script, "timer-register.js"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		time.Sleep(3 * time.Millisecond)
		if _, err := rt.Eval(context.Background(), "globalThis.__lxRunDueTimers(); 'pumped';", "timer-pump.js"); err != nil {
			t.Fatal(err)
		}
	}
	value, err := rt.Eval(context.Background(), "JSON.stringify(globalThis.__timerEvents)", "timer-read.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != `["tick-1","tick-2"]` {
		t.Fatalf("timer events = %s, want only two interval ticks and no cleared timeout", value)
	}
}

func TestLXTimerPumpCapsCallbacksPerTurn(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "timer-limit"}); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Eval(context.Background(), `
globalThis.__timerPumpCount = 0;
function scheduleNext() {
  __timerPumpCount++;
  setTimeout(scheduleNext, 0);
}
setTimeout(scheduleNext, 0);
const executed = __lxRunDueTimers();
JSON.stringify({ executed, count: __timerPumpCount, pending: __lxNextTimerDelay() >= 0 });
`, "timer-limit.js")
	if err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), `JSON.stringify({ count: __timerPumpCount, pending: __lxNextTimerDelay() >= 0 })`, "timer-limit-result.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(value, `"count":1000`) || !strings.Contains(value, `"pending":true`) {
		t.Fatalf("timer pump did not enforce the 1000-callback turn cap: %s", value)
	}
}

func TestDispatchPumpsTimersAndPromiseJobsAroundAsyncHTTPCompletion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(80 * time.Millisecond)
		_, _ = io.WriteString(w, "ok")
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "async-ordering"}); err != nil {
		t.Fatal(err)
	}
	install := `
lx.on('request', async () => {
  const events = [];
  setTimeout(() => events.push('timer'), 10);
  await new Promise((resolve, reject) => {
    lx.request(` + string(mustJSONQuote(server.URL)) + `, {}, (error, response, body) => {
      if (error) return reject(error);
      events.push('http-callback');
      Promise.resolve().then(() => {
        events.push('callback-microtask');
        resolve(body);
      });
    });
  });
  events.push('action-continuation');
  return events.join(',');
});
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-async-ordering.js"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	const want = "timer,http-callback,callback-microtask,action-continuation"
	if value != want {
		t.Fatalf("async event order = %#v, want %q", value, want)
	}
}

func TestDispatchContextCancellationCancelsInflightHTTP(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "dispatch-context-cancel"}); err != nil {
		t.Fatal(err)
	}
	install := `
lx.on('request', async () => await new Promise((resolve, reject) => {
  lx.request(` + string(mustJSONQuote(server.URL)) + `, {}, error => error ? reject(error) : resolve('unexpected'));
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-context-cancel.js"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{})
	if err == nil || err != context.DeadlineExceeded {
		t.Fatalf("dispatch error = %v, want context deadline exceeded", err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not start")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("dispatch context did not cancel in-flight HTTP")
	}
}

// TestDispatchOrdersNativeCryptoAndZlibPromiseJobsAroundTimers verifies that
// native crypto remains synchronous, native zlib Promise jobs are drained,
// and due timers run before dispatch observes the completed source action.
func TestDispatchOrdersNativeCryptoAndZlibPromiseJobsAroundTimers(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "native-async-ordering"}); err != nil {
		t.Fatal(err)
	}
	install := `
globalThis.__nativeAsyncEvents = [];
lx.on('request', async () => {
  const input = Buffer.from('quickjs-native-zlib-ordering', 'utf8');
  setTimeout(() => __nativeAsyncEvents.push('timer'), 0);
  const digest = lx.utils.crypto.md5('fixture');
  if (digest !== '4cf9d4f0069fc18fb3fcc0a50dceb852') throw new Error('native MD5 result mismatch');
  __nativeAsyncEvents.push('crypto');
  const compressed = await lx.utils.zlib.deflate(input);
  __nativeAsyncEvents.push('zlib-deflate');
  const restored = await lx.utils.zlib.inflate(compressed);
  if (Buffer.from(restored).toString('utf8') !== 'quickjs-native-zlib-ordering') {
    throw new Error('native zlib round trip mismatch');
  }
  __nativeAsyncEvents.push('zlib-inflate');
  __nativeAsyncEvents.push('action-complete');
  return 'native-async-ordering-ok';
});
'registered';
`;
	if _, err := rt.Eval(context.Background(), install, "register-native-async-ordering.js"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if value != "native-async-ordering-ok" {
		t.Fatalf("dispatch result = %#v, want native-async-ordering-ok", value)
	}
	events, err := rt.Eval(ctx, "JSON.stringify(globalThis.__nativeAsyncEvents)", "read-native-async-events.js")
	if err != nil {
		t.Fatal(err)
	}
	const want = `["crypto","zlib-deflate","zlib-inflate","action-complete","timer"]`
	if events != want {
		t.Fatalf("native crypto/zlib/timer event order = %s, want %s", events, want)
	}
}



func TestRuntimeCloseCancelsInflightDispatchAndHTTP(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "runtime-shutdown"}); err != nil {
		t.Fatal(err)
	}
	install := `
lx.on('request', async () => await new Promise((resolve, reject) => {
  lx.request(` + string(mustJSONQuote(server.URL)) + `, {}, error => error ? reject(error) : resolve('unexpected'));
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-runtime-shutdown.js"); err != nil {
		t.Fatal(err)
	}

	dispatchDone := make(chan error, 1)
	go func() {
		_, err := rt.DispatchLXRequest(context.Background(), "kw", "musicUrl", map[string]any{})
		dispatchDone <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("HTTP request did not start")
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("runtime close did not cancel in-flight HTTP request")
	}
	select {
	case err := <-dispatchDone:
		if err == nil {
			t.Fatal("dispatch unexpectedly succeeded after runtime shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("dispatch did not stop after runtime shutdown")
	}
}


func TestRuntimeCloseInterruptsActiveJavaScriptEvaluation(t *testing.T) {
	rt, err := New(Options{
		MemoryLimitBytes: 16 << 20,
		StackLimitBytes:  512 << 10,
		EvalTimeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	evalDone := make(chan error, 1)
	go func() {
		_, evalErr := rt.Eval(context.Background(), "while (true) {}", "runtime-close-interrupt.js")
		evalDone <- evalErr
	}()
	// Let the owner goroutine enter the non-terminating JavaScript loop.
	time.Sleep(30 * time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- rt.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatalf("close runtime: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runtime close did not interrupt active JavaScript evaluation")
	}
	select {
	case err := <-evalDone:
		if err == nil {
			t.Fatal("active evaluation unexpectedly succeeded after runtime shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("active evaluation did not stop after runtime shutdown")
	}
}


func TestEvalContextCancellationInterruptsActiveJavaScript(t *testing.T) {
	rt, err := New(Options{
		MemoryLimitBytes: 16 << 20,
		StackLimitBytes:  512 << 10,
		EvalTimeout:      5 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	ctx, cancel := context.WithCancel(context.Background())
	evalDone := make(chan error, 1)
	go func() {
		_, evalErr := rt.Eval(ctx, "while (true) {}", "eval-context-cancel.js")
		evalDone <- evalErr
	}()
	// Give the owner goroutine time to enter the JavaScript loop; the runtime
	// timeout is deliberately much longer than the cancellation assertion.
	time.Sleep(30 * time.Millisecond)
	started := time.Now()
	cancel()
	select {
	case evalErr := <-evalDone:
		if !errors.Is(evalErr, context.Canceled) {
			t.Fatalf("evaluation error = %v, want context.Canceled", evalErr)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("context cancellation took too long: %s", elapsed)
		}
	case <-time.After(time.Second):
		t.Fatal("context cancellation did not interrupt active JavaScript evaluation")
	}
}

func TestRuntimeConcurrentCloseIsIdempotentAndRejectsNewEval(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	const callers = 8
	results := make(chan error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- rt.Close()
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent close returned error: %v", err)
		}
	}
	if _, err := rt.Eval(context.Background(), "1 + 1", "eval-after-close.js"); err == nil {
		t.Fatal("evaluation unexpectedly succeeded after runtime close")
	}
}


func TestRuntimeCloseCancelsMaximumInflightHTTPBatch(t *testing.T) {
	const requestCount = 256
	started := make(chan struct{}, requestCount)
	canceled := make(chan struct{}, requestCount)
	var startedCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		startedCount.Add(1)
		started <- struct{}{}
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	defer server.Close()

	rt := newTestRuntime(t, 10*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "maximum-inflight-shutdown"}); err != nil {
		t.Fatal(err)
	}
	install := `
lx.on('request', async () => {
  const requests = [];
  for (let i = 0; i < 256; i++) {
    requests.push(new Promise((resolve, reject) => {
      lx.request(` + string(mustJSONQuote(server.URL)) + `, {}, error => error ? reject(error) : resolve());
    }));
  }
  await Promise.all(requests);
  return 'unexpected-success';
});
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-maximum-inflight-shutdown.js"); err != nil {
		t.Fatal(err)
	}

	dispatchDone := make(chan error, 1)
	go func() {
		_, err := rt.DispatchLXRequest(context.Background(), "kw", "musicUrl", map[string]any{})
		dispatchDone <- err
	}()
	// The host-wide HTTP semaphore intentionally caps network concurrency at
	// eight even though one JS action may enqueue up to 256 requests. Wait for
	// all permitted slots, then verify shutdown cancels active and queued work.
	const minimumStarted = lxMaxConcurrentRequests
	startDeadline := time.NewTimer(20 * time.Second)
	defer startDeadline.Stop()
	for startedCount.Load() < minimumStarted {
		select {
		case <-started:
		case <-startDeadline.C:
			_ = rt.Close()
			t.Fatalf("only %d HTTP requests started; want at least %d", startedCount.Load(), minimumStarted)
		}
	}
	if err := rt.Close(); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	startedAtShutdown := int(startedCount.Load())
	if startedAtShutdown != lxMaxConcurrentRequests {
		t.Fatalf("active HTTP requests at shutdown = %d, want exactly %d", startedAtShutdown, lxMaxConcurrentRequests)
	}
	for i := 0; i < startedAtShutdown; i++ {
		select {
		case <-canceled:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d started HTTP requests observed cancellation", i, startedAtShutdown)
		}
	}
	select {
	case err := <-dispatchDone:
		if err == nil {
			t.Fatal("dispatch unexpectedly succeeded after runtime shutdown")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch did not stop after maximum-batch runtime shutdown")
	}
}

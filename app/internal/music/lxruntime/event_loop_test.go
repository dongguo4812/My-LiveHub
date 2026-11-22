//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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


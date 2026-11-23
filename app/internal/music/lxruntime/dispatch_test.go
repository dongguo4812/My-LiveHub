//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestDispatchLXRequestServicesQueuedHTTPAndResolvesAction(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("id") != "song-1" { t.Errorf("query = %q", r.URL.RawQuery) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://cdn.example/song.mp3"}`))
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name:"dispatch-test", RawScript:""}); err != nil {
		t.Fatal(err)
	}
	install := `
lx.on('request', async ({ source, action, info }) => {
  if (source !== 'kw' || action !== 'musicUrl') throw new Error('unexpected action');
  return await new Promise((resolve, reject) => {
    lx.request(` + string(mustJSONQuote(server.URL+"?id=song-1")) + `, {}, (err, resp, body) => {
      if (err) return reject(err);
      if (resp.statusCode !== 200 || resp.body.url !== 'https://cdn.example/song.mp3') return reject(new Error('bad response'));
      resolve(resp.body.url);
    });
  });
});
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-dispatch-test.js"); err != nil {
		t.Fatal(err)
	}
	value, err := rt.DispatchLXRequest(context.Background(), "kw", "musicUrl", map[string]any{
		"musicInfo": map[string]any{"id":"song-1"}, "type":"320k",
	})
	if err != nil { t.Fatal(err) }
	if value != "https://cdn.example/song.mp3" { t.Fatalf("dispatch result = %#v", value) }
}


func TestDispatchLXRequestPreservesHTTPErrorStatusInCallback(t *testing.T) {
	const responseBody = "not found"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-LX-Trace", "fixture-418")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(responseBody))
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "http-status-dispatch"}); err != nil {
		t.Fatal(err)
	}
	install := `
lx.on('request', async () => await new Promise((resolve, reject) => {
  lx.request(` + string(mustJSONQuote(server.URL)) + `, {}, (err, resp, body) => {
    if (err) return reject(err);
    if (resp.statusCode !== 418) return reject(new Error('statusCode was not preserved'));
    if (resp.statusMessage !== "418 I'm a teapot") return reject(new Error('statusMessage was not preserved: ' + resp.statusMessage));
    if (!resp.headers['X-Lx-Trace'] || resp.headers['X-Lx-Trace'][0] !== 'fixture-418') return reject(new Error('response headers were not preserved'));
    if (resp.bytes !== 9 || body !== 'not found' || resp.body !== body) return reject(new Error('response body metadata was not preserved'));
    if (!Buffer.isBuffer(resp.raw) || !(resp.raw instanceof Uint8Array) || resp.raw.length !== 9) return reject(new Error('raw response must be a Buffer-backed Uint8Array'));
    if (resp.raw.toString('hex') !== '6e6f7420666f756e64') return reject(new Error('raw Buffer hex conversion mismatch'));
    resolve('http-status-and-callback-contract-ok');
  });
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-http-status-dispatch.js"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if value != "http-status-and-callback-contract-ok" {
		t.Fatalf("dispatch result = %#v", value)
	}
}

func TestDispatchLXRequestPropagatesHTTPFailure(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	_, err := rt.Eval(context.Background(), `
lx.on('request', async () => await new Promise((resolve, reject) => {
  lx.request('http://127.0.0.1:1/unreachable', {}, error => error ? reject(error) : resolve('unexpected'));
}));
'registered';
`, "register-failing-request.js")
	if err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err = rt.DispatchLXRequest(ctx, "kw", "musicUrl", json.RawMessage(`{}`))
	if err == nil { t.Fatal("expected network error to reject source action") }
}

func TestXinglanRealHostDispatchesRealSourceThroughHTTPClient(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Query().Get("song") != "fixture-song-real-http" { t.Errorf("query = %q", r.URL.RawQuery) }
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"url":"https://cdn.example.invalid/real-http-fixture.mp3"}`))
	}))
	defer server.Close()
	rt := newTestRuntime(t, 10*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name:"Xinglan integration", Version:"4.0.2", RawScript:string(source)}); err != nil { t.Fatalf("install real LX host: %v", err) }
	if _, err := rt.Eval(context.Background(), string(source), "xinglan-real-dispatch.js"); err != nil { t.Fatalf("initialize real source: %v", err) }
	setup := "BACKEND_POOLS.kw = [{ name: 'local integration backend', fetch: async (songId) => { const response = await httpFetch(" + string(mustJSONQuote(server.URL+"?song=fixture-song-real-http")) + ", { method: 'GET', timeout: 5000 }); if (!response.body || !response.body.url) throw new Error('fixture backend returned no URL'); return response.body.url; } }]; 'backend-installed';"
	if _, err := rt.Eval(context.Background(), setup, "xinglan-local-backend.js"); err != nil { t.Fatalf("install local backend: %v", err) }
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	value, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{"musicInfo": map[string]any{"songmid":"fixture-song-real-http", "name":"Fixture Song"}, "type":"320k"}, server.Client())
	if err != nil { t.Fatalf("dispatch real Xinglan source: %v", err) }
	if value != "https://cdn.example.invalid/real-http-fixture.mp3" { t.Fatalf("resolved URL = %#v", value) }
	if requests != 1 { t.Fatalf("HTTP requests = %d, want 1", requests) }
}


// TestDispatchLXRequestPreservesBinaryRawResponseBytes verifies that lx.request
// exposes the exact response octets through resp.raw, independently of the
// text/JSON body decoding path.
func TestDispatchLXRequestPreservesBinaryRawResponseBytes(t *testing.T) {
	want := []byte{0x00, 0xff, 0x41, 0x80, 0x7f}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(want)
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "binary-response-contract"}); err != nil {
		t.Fatal(err)
	}
	install := `
lx.on('request', async () => await new Promise((resolve, reject) => {
  lx.request(` + string(mustJSONQuote(server.URL)) + `, {}, (err, resp, body) => {
    if (err) return reject(err);
    const expected = [0, 255, 65, 128, 127];
    if (resp.bytes !== expected.length) return reject(new Error('wrong response byte count: ' + resp.bytes));
    if (!Buffer.isBuffer(resp.raw) || !(resp.raw instanceof Uint8Array)) return reject(new Error('raw response is not a Node-compatible Buffer'));
    if (resp.raw.length !== expected.length) return reject(new Error('wrong raw response length'));
    for (let i = 0; i < expected.length; i++) {
      if (resp.raw[i] !== expected[i]) return reject(new Error('raw byte mismatch at ' + i + ': ' + resp.raw[i]));
    }
    const bufferHex = resp.raw.toString('hex');
    if (bufferHex !== '00ff41807f') return reject(new Error('resp.raw.toString(\'hex\') mismatch: ' + bufferHex));
    resolve('binary-raw-response-ok');
  });
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-binary-response.js"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	value, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if value != "binary-raw-response-ok" {
		t.Fatalf("dispatch result = %#v", value)
	}
}


// TestXinglanEAPIUsesNativeCryptoAndBufferHost exercises the real source's
// MD5 -> AES-128-ECB -> lx.utils.buffer.bufToString('hex') call chain.
func TestXinglanEAPIUsesNativeCryptoAndBufferHost(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }

	rt := newTestRuntime(t, 10*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{
		Name: "Xinglan EAPI integration", Version: "4.0.2", RawScript: string(source),
	}); err != nil { t.Fatalf("install LX host: %v", err) }
	if _, err := rt.Eval(context.Background(), string(source), "xinglan-eapi-crypto.js"); err != nil {
		t.Fatalf("initialize real Xinglan source: %v", err)
	}

	const endpoint = "/api/song/enhance/player/url/v1"
	const payloadJSON = `{"ids":"[\"fixture-song\"]","level":"standard","encodeType":"flac"}`
	value, err := rt.Eval(context.Background(), `
		const payload = { ids: '["fixture-song"]', level: 'standard', encodeType: 'flac' };
		JSON.stringify(wyEapi('/api/song/enhance/player/url/v1', payload))
	`, "xinglan-eapi-crypto-result.js")
	if err != nil { t.Fatalf("execute Xinglan EAPI helper: %v", err) }

	digestInput := "nobody" + endpoint + "use" + payloadJSON + "md5forencrypt"
	digest := md5.Sum([]byte(digestInput))
	plain := []byte(endpoint + "-36cd479b6b5-" + payloadJSON + "-36cd479b6b5-" + hex.EncodeToString(digest[:]))
	padding := aes.BlockSize - len(plain)%aes.BlockSize
	for i := 0; i < padding; i++ { plain = append(plain, byte(padding)) }
	key := []byte("e82ckenh8dichen8")
	block, err := aes.NewCipher(key)
	if err != nil { t.Fatal(err) }
	encrypted := make([]byte, len(plain))
	for offset := 0; offset < len(plain); offset += aes.BlockSize {
		block.Encrypt(encrypted[offset:offset+aes.BlockSize], plain[offset:offset+aes.BlockSize])
	}
	want := `{"params":"` + strings.ToUpper(hex.EncodeToString(encrypted)) + `"}`
	if value != want {
		t.Fatalf("Xinglan EAPI host result mismatch:\n got: %s\nwant: %s", value, want)
	}
}


func TestDispatchLXRequestAbortsInFlightHTTPAndCallsBackOnce(t *testing.T) {
	requestStarted := make(chan struct{})
	requestCanceled := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
		close(requestCanceled)
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "in-flight-abort"}); err != nil {
		t.Fatal(err)
	}
	install := `
globalThis.__abortCallbackCount = 0;
lx.on('request', async () => await new Promise((resolve, reject) => {
  const cancel = lx.request(`+string(mustJSONQuote(server.URL))+`, { timeout: 5000 }, error => {
    globalThis.__abortCallbackCount++;
    if (error) reject(error);
    else resolve('unexpected-response');
  });
  setTimeout(() => { cancel(); cancel(); }, 30);
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), install, "register-in-flight-abort.js"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{})
	if err == nil || err.Error() != "Cancel request" {
		t.Fatalf("dispatch error = %v, want Cancel request", err)
	}
	select {
	case <-requestStarted:
	case <-ctx.Done():
		t.Fatal("HTTP request never reached server")
	}
	select {
	case <-requestCanceled:
	case <-ctx.Done():
		t.Fatal("in-flight HTTP request context was not canceled")
	}
	count, evalErr := rt.Eval(ctx, "globalThis.__abortCallbackCount", "read-abort-callback-count.js")
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if count != "1" {
		t.Fatalf("abort callback count = %s, want 1", count)
	}
}

func TestDispatchLXRequestAbortBeforeDispatchRemovesQueuedRequest(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_, _ = io.WriteString(w, "unexpected")
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "queued-abort"}); err != nil {
		t.Fatal(err)
	}
	script := `
globalThis.__queuedAbortCallbackCount = 0;
lx.on('request', async () => await new Promise((resolve, reject) => {
  const cancel = lx.request(`+string(mustJSONQuote(server.URL))+`, {}, error => {
    globalThis.__queuedAbortCallbackCount++;
    if (error) reject(error);
    else resolve('unexpected-response');
  });
  cancel();
  cancel();
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), script, "register-queued-abort.js"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{})
	if err == nil || err.Error() != "Cancel request" {
		t.Fatalf("dispatch error = %v, want Cancel request", err)
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("HTTP requests = %d, want 0", got)
	}
	count, evalErr := rt.Eval(ctx, "globalThis.__queuedAbortCallbackCount", "read-queued-abort-count.js")
	if evalErr != nil {
		t.Fatal(evalErr)
	}
	if count != "1" {
		t.Fatalf("abort callback count = %s, want 1", count)
	}
}

func TestDispatchLXRequestTimeoutWinsOverLaterAbort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "timeout-vs-abort"}); err != nil { t.Fatal(err) }
	script := `
globalThis.__timeoutRaceCallbackCount = 0;
lx.on('request', async () => await new Promise((resolve, reject) => {
  const cancel = lx.request(`+string(mustJSONQuote(server.URL))+`, { timeout: 30 }, error => {
    globalThis.__timeoutRaceCallbackCount++;
    if (!error) return reject(new Error('timeout request unexpectedly succeeded'));
    setTimeout(() => resolve(error.message), 80);
  });
  setTimeout(() => cancel(), 60);
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), script, "register-timeout-vs-abort.js"); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second); defer cancel()
	value, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{}); if err != nil { t.Fatal(err) }
	message, ok := value.(string); if !ok || !strings.Contains(message, "deadline exceeded") { t.Fatalf("timeout result = %#v, want deadline exceeded", value) }
	count, evalErr := rt.Eval(ctx, "globalThis.__timeoutRaceCallbackCount", "read-timeout-race-count.js"); if evalErr != nil { t.Fatal(evalErr) }
	if count != "1" { t.Fatalf("timeout callback count = %s, want 1", count) }
}

func TestDispatchLXRequestResponseWinsOverLaterAbort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "response-won") }))
	defer server.Close()
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "response-vs-abort"}); err != nil { t.Fatal(err) }
	script := `
globalThis.__responseRaceCallbackCount = 0;
lx.on('request', async () => await new Promise((resolve, reject) => {
  const cancel = lx.request(`+string(mustJSONQuote(server.URL))+`, {}, (error, response, body) => {
    globalThis.__responseRaceCallbackCount++;
    if (error) return reject(error);
    setTimeout(() => resolve(body), 200);
  });
  setTimeout(() => cancel(), 100);
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), script, "register-response-vs-abort.js"); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second); defer cancel()
	value, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{}); if err != nil { t.Fatal(err) }
	if value != "response-won" { t.Fatalf("response race result = %#v, want response-won", value) }
	count, evalErr := rt.Eval(ctx, "globalThis.__responseRaceCallbackCount", "read-response-race-count.js"); if evalErr != nil { t.Fatal(evalErr) }
	if count != "1" { t.Fatalf("response callback count = %s, want 1", count) }
}


func TestDispatchCallbackExceptionDoesNotPoisonRuntime(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "callback-isolation"}); err != nil {
		t.Fatal(err)
	}
	badHandler := `
lx.on('request', async () => await new Promise(resolve => {
  lx.request(` + string(mustJSONQuote(server.URL)) + `, {}, () => {
    throw new Error('intentional callback exception');
  });
}));
'registered';
`
	if _, err := rt.Eval(context.Background(), badHandler, "callback-exception.js"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{}); err == nil || !strings.Contains(err.Error(), "intentional callback exception") {
		t.Fatalf("dispatch error = %v, want callback exception", err)
	}

	if _, err := rt.Eval(ctx, `lx.on('request', async ({ info }) => info.value); 'recovered';`, "callback-recovery.js"); err != nil {
		t.Fatalf("register recovery handler: %v", err)
	}
	value, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{"value": "runtime-recovered"})
	if err != nil {
		t.Fatalf("dispatch after callback exception: %v", err)
	}
	if value != "runtime-recovered" {
		t.Fatalf("recovery dispatch result = %#v", value)
	}
}

func TestDispatchLXRequestSerializesConcurrentActions(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "concurrent-dispatch"}); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Eval(context.Background(), `lx.on('request', async ({ info }) => info.value); 'registered';`, "concurrent-dispatch.js"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		value any
		err error
	}
	results := make(chan result, 2)
	var workers sync.WaitGroup
	for _, want := range []string{"first", "second"} {
		workers.Add(1)
		go func(value string) {
			defer workers.Done()
			got, err := rt.DispatchLXRequest(context.Background(), "kw", "musicUrl", map[string]any{"value": value})
			results <- result{value: got, err: err}
		}(want)
	}
	workers.Wait()
	close(results)
	seen := map[string]bool{}
	for item := range results {
		if item.err != nil {
			t.Fatal(item.err)
		}
		value, ok := item.value.(string)
		if !ok {
			t.Fatalf("dispatch result type = %T", item.value)
		}
		seen[value] = true
	}
	if !seen["first"] || !seen["second"] {
		t.Fatalf("concurrent dispatch results = %#v", seen)
	}
}

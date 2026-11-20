//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
    if (!(resp.raw instanceof Uint8Array) || resp.raw.length !== 9) return reject(new Error('raw response bytes were not delivered as Uint8Array'));
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
    if (!(resp.raw instanceof Uint8Array)) return reject(new Error('raw response is not Uint8Array'));
    if (resp.raw.length !== expected.length) return reject(new Error('wrong raw response length'));
    for (let i = 0; i < expected.length; i++) {
      if (resp.raw[i] !== expected[i]) return reject(new Error('raw byte mismatch at ' + i + ': ' + resp.raw[i]));
    }
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

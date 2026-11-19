//go:build lxquickjs && cgo

package lxruntime

import (
	"fmt"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestRuntime(t *testing.T, timeout time.Duration) *Runtime {
	t.Helper()
	rt, err := New(Options{MemoryLimitBytes: 48 << 20, StackLimitBytes: 512 << 10, EvalTimeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func TestQuickJSEvalAndPromiseJobs(t *testing.T) {
	rt := newTestRuntime(t, time.Second)
	if _, err := rt.Eval(context.Background(), "Promise.resolve(42).then(v => globalThis.__promiseValue = v); 'queued'", "promise.js"); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), "globalThis.__promiseValue", "result.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != "42" {
		t.Fatalf("promise job result = %q, want 42", value)
	}
}

func TestQuickJSInterruptsInfiniteLoop(t *testing.T) {
	rt := newTestRuntime(t, 100*time.Millisecond)
	start := time.Now()
	_, err := rt.Eval(context.Background(), "while (true) {}", "infinite-loop.js")
	if err == nil {
		t.Fatal("expected infinite loop to be interrupted")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("interrupt took too long: %s", time.Since(start))
	}
}

func TestQuickJSSourceSyntaxFixtures(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	for _, name := range []string{"lx.js", "xinglan.js", "helloworld.js"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("testdata", "sources", name))
			if err != nil {
				t.Fatal(err)
			}
			_, err = rt.Eval(context.Background(), "new Function("+quoteJS(string(source))+"); 'compiled'", "compile-fixture.js")
			if err != nil {
				t.Fatalf("compile %s: %v", name, err)
			}
		})
	}
}

func quoteJS(source string) string {
	// JSON string quoting is sufficient for a JavaScript string literal.
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range source {
		switch r {
		case '\\':
			b.WriteString("\\\\")
		case '"':
			b.WriteString("\\\"")
		case '\n':
			b.WriteString("\\n")
		case '\r':
			b.WriteString("\\r")
		case '\t':
			b.WriteString("\\t")
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

const lxTestHostShim = `
globalThis.console = { log() {}, info() {}, warn() {}, error() {} };
let __lxTimerID = 0;
globalThis.setTimeout = () => ++__lxTimerID;
globalThis.clearTimeout = () => {};
globalThis.setInterval = () => ++__lxTimerID;
globalThis.clearInterval = () => {};
globalThis.queueMicrotask = fn => Promise.resolve().then(fn);
globalThis.window = globalThis;
globalThis.document = { currentScript: { textContent: '' } };
globalThis.URLSearchParams = class URLSearchParams {
  constructor(value = '') { this.value = String(value); }
  get() { return null; }
  toString() { return this.value; }
};
globalThis.URL = class URL {
  constructor(value, base) {
    this.href = String(value);
    this.protocol = this.href.split(':')[0] + ':';
    this.hostname = '';
    this.pathname = '/';
    this.search = '';
    this.hash = '';
    this.searchParams = new URLSearchParams();
    if (base && !/^[a-z]+:/i.test(this.href)) this.href = String(base).replace(/\/$/, '') + '/' + this.href.replace(/^\//, '');
  }
};
globalThis.TextEncoder = class TextEncoder {
  encode(value) { return new Uint8Array(Array.from(unescape(encodeURIComponent(String(value)))).map(c => c.charCodeAt(0))); }
};
globalThis.TextDecoder = class TextDecoder {
  decode(value) { return decodeURIComponent(escape(Array.from(value || []).map(v => String.fromCharCode(v)).join(''))); }
};
globalThis.crypto = { getRandomValues(value) { return value; } };
const __b64 = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
globalThis.btoa = value => {
  let out = '';
  for (let i = 0; i < value.length; i += 3) {
    const a = value.charCodeAt(i), hasB = i + 1 < value.length, hasC = i + 2 < value.length;
    const b = hasB ? value.charCodeAt(i + 1) : 0, c = hasC ? value.charCodeAt(i + 2) : 0;
    out += __b64[a >> 2] + __b64[((a & 3) << 4) | (b >> 4)] + (hasB ? __b64[((b & 15) << 2) | (c >> 6)] : '=') + (hasC ? __b64[c & 63] : '=');
  }
  return out;
};
globalThis.atob = value => {
  const input = String(value).replace(/[^A-Za-z0-9+/=]/g, '');
  let out = '';
  for (let i = 0; i < input.length; i += 4) {
    const a = __b64.indexOf(input[i]), b = __b64.indexOf(input[i + 1]);
    const c = __b64.indexOf(input[i + 2]), d = __b64.indexOf(input[i + 3]);
    if (a < 0 || b < 0) break;
    out += String.fromCharCode((a << 2) | (b >> 4));
    if (c >= 0) out += String.fromCharCode(((b & 15) << 4) | (c >> 2));
    if (d >= 0) out += String.fromCharCode(((c & 3) << 6) | d);
  }
  return out;
};
globalThis.performance = { now: () => Date.now() };
globalThis.navigator = { userAgent: 'Mozilla/5.0 My-LiveHub QuickJS test' };
globalThis.location = { href: 'https://localhost/' };
const __lxHandlers = Object.create(null);
globalThis.__lxSent = [];
globalThis.lx = {
  EVENT_NAMES: { request: 'request', inited: 'inited', updateAlert: 'updateAlert' },
  version: '2.0.0',
  env: 'desktop',
  currentScriptInfo: { rawScript: '', name: 'Fixture Source', version: '1.0.0', author: 'My-LiveHub test' },
  request(url, options, callback) {
    if (typeof options === 'function') callback = options;
    if (typeof callback === 'function') callback(null, {
      statusCode: 200,
      headers: { 'content-type': 'text/plain' },
      body: '{\"code\":200,\"url\":\"https://cdn.example.invalid/fixture.mp3\",\"data\":{\"url\":\"https://cdn.example.invalid/fixture.mp3\"}}'
    });
    return () => {};
  },
  on(name, handler) { __lxHandlers[name] = handler; },
  send(name, data) {
    __lxSent.push({ name, data });
    if (name === 'inited') globalThis.__lxInit = data;
  },
  utils: {
    buffer: {
      from(value, encoding = 'utf-8') {
        if (value instanceof Uint8Array) return value;
        if (typeof value === 'string') {
          if (encoding === 'hex') {
            const bytes = [];
            for (let i = 0; i + 1 < value.length; i += 2) bytes.push(parseInt(value.slice(i, i + 2), 16));
            return new Uint8Array(bytes);
          }
          if (encoding === 'base64') return new Uint8Array(Array.from(atob(value)).map(c => c.charCodeAt(0)));
          return new TextEncoder().encode(value);
        }
        return new Uint8Array(value);
      },
      bufToString(value, encoding = 'utf-8') {
        if (typeof value === 'string') return value;
        const bytes = value instanceof Uint8Array ? value : new Uint8Array(value || []);
        if (encoding === 'hex') return Array.from(bytes).map(v => v.toString(16).padStart(2, '0')).join('');
        if (encoding === 'base64') return btoa(Array.from(bytes).map(v => String.fromCharCode(v)).join(''));
        return new TextDecoder().decode(bytes);
      }
    },
    crypto: {
      md5() { return '00000000000000000000000000000000'; },
      aesEncrypt() { return new Uint8Array(16); },
      rsaEncrypt() { return new Uint8Array(128); },
      randomBytes(size) { return new Uint8Array(size); }
    },
    zlib: {
      inflate(value) { return Promise.resolve(value); },
      deflate(value) { return Promise.resolve(value); }
    }
  }
};
globalThis.__lxGetHandler = name => __lxHandlers[name];
`;

func TestLXSourceInitializationFixtures(t *testing.T) {
	for _, name := range []string{"lx.js", "xinglan.js", "helloworld.js"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("testdata", "sources", name))
			if err != nil {
				t.Fatal(err)
			}
			rt := newTestRuntime(t, 5*time.Second)
			if _, err := rt.Eval(context.Background(), lxTestHostShim, "lx-test-host.js"); err != nil {
				t.Fatalf("install LX host shim: %v", err)
			}
			if _, err := rt.Eval(context.Background(), "globalThis.lx.currentScriptInfo.rawScript = "+quoteJS(string(source)), "set-current-script.js"); err != nil {
				t.Fatalf("set current script metadata: %v", err)
			}
			if _, err := rt.Eval(context.Background(), string(source), name); err != nil {
				t.Fatalf("initialize source: %v", err)
			}
			initType, err := rt.Eval(context.Background(), "typeof globalThis.__lxInit", "check-init.js")
			if err != nil {
				t.Fatal(err)
			}
			if initType != "object" {
				t.Fatalf("source did not send inited event; got %q", initType)
			}
			sourceCount, err := rt.Eval(context.Background(), "Object.keys(globalThis.__lxInit.sources || {}).length", "check-sources.js")
			if err != nil {
				t.Fatal(err)
			}
			if sourceCount == "0" {
				t.Fatal("source initialized without registering any music sources")
			}
			handlerType, err := rt.Eval(context.Background(), "typeof globalThis.__lxGetHandler('request')", "check-handler.js")
			if err != nil {
				t.Fatal(err)
			}
			if handlerType != "function" {
				t.Fatalf("source did not register request handler; got %q", handlerType)
			}
		})
	}
}

func TestHelloWorldMusicURLResolutionWithMockHost(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "helloworld.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 5*time.Second)
	if _, err := rt.Eval(context.Background(), lxTestHostShim, "lx-test-host.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Eval(context.Background(), "globalThis.lx.currentScriptInfo.rawScript = "+quoteJS(string(source)), "set-current-script.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Eval(context.Background(), string(source), "helloworld.js"); err != nil {
		t.Fatal(err)
	}
	_, err = rt.Eval(context.Background(), `
globalThis.__lxResolveError = '';
globalThis.__lxResolveValue = '';
Promise.resolve(globalThis.__lxGetHandler('request')({
  source: 'kw',
  action: 'musicUrl',
  info: { musicInfo: { rid: 'fixture-song-001', name: 'Fixture Song' }, type: '320k' }
})).then(value => { globalThis.__lxResolveValue = value; })
  .catch(error => { globalThis.__lxResolveError = String(error); });
'started';
`, "resolve-fixture.js")
	if err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), "globalThis.__lxResolveValue", "read-url.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != "https://cdn.example.invalid/fixture.mp3" {
		errText, _ := rt.Eval(context.Background(), "globalThis.__lxResolveError", "read-error.js")
		t.Fatalf("music URL result = %q, error = %q", value, errText)
	}
}

func TestXinglanMusicURLResolutionWithMockHost(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 5*time.Second)
	if _, err := rt.Eval(context.Background(), lxTestHostShim, "lx-test-host.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Eval(context.Background(), "globalThis.lx.currentScriptInfo.rawScript = "+quoteJS(string(source)), "set-current-script.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Eval(context.Background(), string(source), "xinglan.js"); err != nil {
		t.Fatal(err)
	}
	_, err = rt.Eval(context.Background(), `
globalThis.__lxResolveError = '';
globalThis.__lxResolveValue = '';
Promise.resolve(globalThis.__lxGetHandler('request')({
  source: 'kw',
  action: 'musicUrl',
  info: { musicInfo: { songmid: 'fixture-song-001', name: 'Fixture Song' }, type: '320k' }
})).then(value => { globalThis.__lxResolveValue = value; })
  .catch(error => { globalThis.__lxResolveError = String(error); });
'started';
`, "resolve-xinglan-fixture.js")
	if err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), "globalThis.__lxResolveValue", "read-xinglan-url.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != "https://cdn.example.invalid/fixture.mp3" {
		errText, _ := rt.Eval(context.Background(), "globalThis.__lxResolveError", "read-xinglan-error.js")
		t.Fatalf("music URL result = %q, error = %q", value, errText)
	}
}

func TestQuickJSRuntimeRepeatedLifecycleAndClose(t *testing.T) {
	for i := 0; i < 10; i++ {
		rt, err := New(Options{MemoryLimitBytes: 16 << 20, StackLimitBytes: 512 << 10, EvalTimeout: time.Second})
		if err != nil {
			t.Fatalf("runtime %d: %v", i, err)
		}
		value, err := rt.Eval(context.Background(), "6 * 7", "lifecycle.js")
		if err != nil || value != "42" {
			t.Fatalf("runtime %d eval = %q, err = %v", i, value, err)
		}
		if err := rt.Close(); err != nil {
			t.Fatalf("runtime %d close: %v", i, err)
		}
		if err := rt.Close(); err != nil {
			t.Fatalf("runtime %d second close: %v", i, err)
		}
	}
}

func TestQuickJSMemoryAndStackLimits(t *testing.T) {
	rt, err := New(Options{MemoryLimitBytes: 8 << 20, StackLimitBytes: 256 << 10, EvalTimeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()

	if _, err := rt.Eval(context.Background(), "new Uint8Array(64 * 1024 * 1024)", "memory-limit.js"); err == nil {
		t.Fatal("expected oversized allocation to fail under the configured memory limit")
	}
	if _, err := rt.Eval(context.Background(), "(function recurse(){ return recurse(); })()", "stack-limit.js"); err == nil {
		t.Fatal("expected recursion to fail under the configured stack limit")
	}
}


func TestXinglanInitializesAgainstRealLXHost(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 10*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{
		Name: "Xinglan fixture", Version: "4.0.2", RawScript: string(source),
	}); err != nil { t.Fatalf("install real LX host: %v", err) }
	if _, err := rt.Eval(context.Background(), string(source), "xinglan-real-host.js"); err != nil {
		t.Fatalf("initialize Xinglan against real LX host: %v", err)
	}
	value, err := rt.Eval(context.Background(), `
JSON.stringify({
  initialized: __lxHostState.initialized,
  sourceNames: Object.keys(__lxHostState.initInfo.sources).sort(),
  requestHandler: typeof __lxHostHandlers.request,
  rawScriptAvailable: lx.currentScriptInfo.rawScript.length > 60000
})
`, "xinglan-real-host-result.js")
	if err != nil { t.Fatal(err) }
	want := `{"initialized":true,"sourceNames":["kg","kw","mg","tx","wy"],"requestHandler":"function","rawScriptAvailable":true}`
	if value != want { t.Fatalf("real Xinglan host initialization = %q, want %q", value, want) }
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestPinnedSourcesInitializeAgainstRealLXHost(t *testing.T) {
	for _, name := range []string{"xinglan.js", "helloworld.js"} {
		t.Run(name, func(t *testing.T) {
			source, err := os.ReadFile(filepath.Join("testdata", "sources", name))
			if err != nil {
				t.Fatal(err)
			}
			rt := newTestRuntime(t, 10*time.Second)
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
			})}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := rt.InitializeLXSource(ctx, ScriptInfo{Name: name, Version: "fixture", RawScript: string(source)}, string(source), client); err != nil {
				t.Fatalf("initialize source against real LX host: %v", err)
			}
			value, err := rt.Eval(context.Background(), `JSON.stringify({ initialized: __lxHostState.initialized, sourceNames: Object.keys((__lxHostState.initInfo && __lxHostState.initInfo.sources) || {}).sort(), requestHandler: typeof __lxHostHandlers.request, rawScriptAvailable: lx.currentScriptInfo.rawScript.length > 0 })`, "pinned-source-real-host-result.js")
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				Initialized bool `json:"initialized"`
				SourceNames []string `json:"sourceNames"`
				RequestHandler string `json:"requestHandler"`
				RawScriptAvailable bool `json:"rawScriptAvailable"`
			}
			if err := json.Unmarshal([]byte(value), &result); err != nil {
				t.Fatalf("decode initialization result %q: %v", value, err)
			}
			if !result.Initialized {
				t.Fatalf("source did not initialize; host state: %s", value)
			}
			if len(result.SourceNames) == 0 {
				t.Fatal("source initialized without supported music platforms")
			}
			if result.RequestHandler != "function" {
				t.Fatalf("request handler type = %q", result.RequestHandler)
			}
			if !result.RawScriptAvailable {
				t.Fatal("real LX host did not expose rawScript metadata")
			}
		})
	}
}

// The pinned lx.js source fetches server-managed configuration before it sends
// its inited event. An empty JSON object is not a valid server configuration,
// so this test verifies the real host/request path without claiming that the
// source initialized successfully against a fabricated response.
func TestLXSourceRequestsServerConfigurationAgainstRealLXHost(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	type capturedRequest struct {
		method string
		scheme string
		host string
		path string
		queryKeys []string
		versionParam string
		headerNames []string
		bodyLength int
		bodyJSONKeys []string
	}
	var requests []capturedRequest
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		captured := capturedRequest{method: request.Method, scheme: request.URL.Scheme, host: request.URL.Host, path: request.URL.Path, versionParam: request.URL.Query().Get("v")}
		for name := range request.URL.Query() {
			captured.queryKeys = append(captured.queryKeys, name)
		}
		for name := range request.Header {
			captured.headerNames = append(captured.headerNames, name)
		}
		var body []byte
		if request.Body != nil {
			body, _ = io.ReadAll(request.Body)
			request.Body = io.NopCloser(bytes.NewReader(body))
		}
		captured.bodyLength = len(body)
		var bodyObject map[string]json.RawMessage
		if json.Unmarshal(body, &bodyObject) == nil {
			for name := range bodyObject {
				captured.bodyJSONKeys = append(captured.bodyJSONKeys, name)
			}
		}
		requests = append(requests, captured)
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = rt.InitializeLXSource(ctx, ScriptInfo{Name: "独家音源", Version: "7", Author: "w", Description: "后续更新，请关注微信公众号: 洛雪科技", RawScript: string(source)}, string(source), client)
	if err == nil {
		t.Fatal("expected initialization to remain incomplete with an empty server-configuration response")
	}
	if len(requests) != 1 {
		t.Fatalf("server configuration requests = %d, want exactly 1 (initialization error: %v)", len(requests), err)
	}
	t.Logf("captured /rconfig request: method=%s scheme=%s host=%s path=%s queryKeys=%v versionParam=%q headerNames=%v bodyLength=%d bodyJSONKeys=%v",
		requests[0].method, requests[0].scheme, requests[0].host, requests[0].path, requests[0].queryKeys, requests[0].versionParam, requests[0].headerNames, requests[0].bodyLength, requests[0].bodyJSONKeys)
	if requests[0].method != http.MethodGet {
		t.Fatalf("configuration request method = %q, want GET", requests[0].method)
	}
	if requests[0].path != "/rconfig" {
		t.Fatalf("configuration request path = %q, want /rconfig", requests[0].path)
	}
	if requests[0].host == "" || requests[0].scheme != "https" {
		t.Fatalf("configuration request is not absolute HTTPS: scheme=%q host=%q", requests[0].scheme, requests[0].host)
	}
	if requests[0].versionParam != "7" {
		t.Fatalf("configuration request version parameter = %q, want source version 7", requests[0].versionParam)
	}
	if requests[0].bodyLength != 0 {
		t.Fatalf("configuration request body length = %d, want 0", requests[0].bodyLength)
	}
}


// Huanyin is a non-handshake LX source: initialization is local, and its QQ
// musicUrl action resolves a URL without issuing an HTTP request. This checks
// real LX host registration, Promise dispatch, and source return-value handling.
func TestHuanyinSourceInitializesAndDispatchesMusicURL(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	defer rt.Close()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected network request during local QQ URL resolution: %s", request.URL.Redacted())
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize Huanyin source against real LX host: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "tx", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{"songmid": "test-song-mid"},
	}, client)
	if err != nil {
		t.Fatalf("dispatch Huanyin QQ musicUrl: %v", err)
	}
	got, ok := result.(string)
	if !ok {
		t.Fatalf("musicUrl result type = %T, want string (value: %#v)", result, result)
	}
	want := "https://music-dl.sayqz.com/api/?source=qq&id=test-song-mid&type=url&br=320k"
	if got != want {
		t.Fatalf("musicUrl = %q, want %q", got, want)
	}

	var searchRequests int
	searchClient := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		searchRequests++
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin search request: %s %s", request.Method, request.URL.Redacted())
		}
		if request.URL.Query().Get("gm") != "周杰伦" || request.URL.Query().Get("n") != "1" {
			return nil, fmt.Errorf("unexpected Huanyin search query: %s", request.URL.RawQuery)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"code":200,"title":"周杰伦","artist":"周杰伦","album":"范特西","duration":"04:00"}`)),
			Request: request,
		}, nil
	})}
	searchResult, err := rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{
		"keyword": "周杰伦", "limit": 1,
	}, searchClient)
	if err != nil {
		t.Fatalf("dispatch Huanyin Migu search through Go HTTP host: %v", err)
	}
	items, ok := searchResult.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("search result = %#v, want one-item array", searchResult)
	}
	item, ok := items[0].(map[string]any)
	if !ok || item["name"] != "周杰伦" || item["singer"] != "周杰伦" || item["source"] != "mg" {
		t.Fatalf("search item = %#v, want mapped Migu song metadata", items[0])
	}
	if searchRequests != 1 {
		t.Fatalf("Huanyin search HTTP requests = %d, want 1", searchRequests)
	}
}

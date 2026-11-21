//go:build lxquickjs && cgo

package lxruntime

import (
	"fmt"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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
		rawQuery string
		queryKeys []string
		versionParam string
		headerNames []string
		bodyLength int
		bodyJSONKeys []string
	}
	var requests []capturedRequest
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		captured := capturedRequest{method: request.Method, scheme: request.URL.Scheme, host: request.URL.Host, path: request.URL.Path, rawQuery: request.URL.RawQuery, versionParam: request.URL.Query().Get("v")}
		for name := range request.URL.Query() {
			captured.queryKeys = append(captured.queryKeys, name)
		}
		sort.Strings(captured.queryKeys)
		for name := range request.Header {
			captured.headerNames = append(captured.headerNames, name)
		}
		sort.Strings(captured.headerNames)
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
	t.Logf("captured /rconfig request: method=%s scheme=%s host=%s path=%s rawQuery=%q queryKeys=%v versionParam=%q headerNames=%v bodyLength=%d bodyJSONKeys=%v",
		requests[0].method, requests[0].scheme, requests[0].host, requests[0].path, requests[0].rawQuery, requests[0].queryKeys, requests[0].versionParam, requests[0].headerNames, requests[0].bodyLength, requests[0].bodyJSONKeys)
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
	// The V7 fixture embeds this server fingerprint. The remaining values are
	// per-request hexadecimal protocol fields and must not be hard-coded.
	if got := requests[0].rawQuery; got == "" {
		t.Fatal("configuration request raw query is empty")
	}
	parsedQuery, err := url.ParseQuery(requests[0].rawQuery)
	if err != nil {
		t.Fatalf("parse configuration request query: %v", err)
	}
	if got := parsedQuery.Get("fp"); got != "42bc92aaf1a30247e40eab9523ca0713" {
		t.Fatalf("configuration fingerprint = %q, want pinned V7 fingerprint", got)
	}
	for key, wantLength := range map[string]int{"e": 32, "to": 22, "s2": 32, "si": 32} {
		value := parsedQuery.Get(key)
		decoded, err := hex.DecodeString(value)
		if err != nil || len(value) != wantLength || len(decoded)*2 != wantLength {
			t.Errorf("configuration query %s = %q, want %d hexadecimal characters", key, value, wantLength)
		}
	}
	if got, want := strings.Join(requests[0].queryKeys, ","), "e,fp,s2,si,to,v"; got != want {
		t.Fatalf("configuration request query keys = %q, want exact protocol keys %q", got, want)
	}
	if got, want := strings.Join(requests[0].headerNames, ","), "Content-Type,User-Agent"; got != want {
		t.Fatalf("configuration request headers = %q, want %q", got, want)
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


// TestXinglanRealSourceResolvesMiguURLThroughNativeLXRequest exercises the
// pinned aggregate source's real musicUrl handler, native HTTP dispatch,
// backend response parsing, and Migu quality-path rewrite. All network traffic
// is intercepted; no third-party service is contacted.
func TestXinglanRealSourceResolvesMiguURLThroughNativeLXRequest(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var resolveRequests, probeRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch {
		case request.Method == http.MethodPost &&
			request.URL.Host == "musicserver.haitangw.cc" &&
			request.URL.Path == "/v1/music/resolve-url":
			resolveRequests++
			if request.Header.Get("Content-Type") != "application/json" {
				return nil, fmt.Errorf("resolve content type = %q", request.Header.Get("Content-Type"))
			}
			var body struct {
				Source string `json:"source"`
				RID    string `json:"rid"`
				Level  string `json:"level"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				return nil, fmt.Errorf("decode resolve request: %w", err)
			}
			if body.Source != "mg" || body.RID != "migu-song-001" || body.Level != "standard" {
				return nil, fmt.Errorf("unexpected resolve payload: %+v", body)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"data":{"url":"https://cdn.example/标清高清/MP3_128_16_Stero/song.mp3?token=abc"}} `)),
				Request: request,
			}, nil
		case request.Method == http.MethodHead && request.URL.Host == "cdn.example":
			probeRequests++
			if !strings.Contains(request.URL.EscapedPath(), "MP3_320_16_Stero") {
				return nil, fmt.Errorf("Migu quality probe path = %q", request.URL.EscapedPath())
			}
			return &http.Response{
				StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader("")), Request: request,
			}, nil
		default:
			// Other backends in the source's parallel first-wave are deliberately
			// unavailable so the test proves the configured Haitang route works.
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable",
				Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request,
			}, nil
		}
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "星澜聚合音源", Version: "v4.0.2", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}

	result, err := rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"songmid": "migu-song-001",
			"name": "fixture song",
		},
	}, client)
	if err != nil {
		t.Fatalf("dispatch Xinglan Migu musicUrl through real LX host: %v", err)
	}
	resolved, ok := result.(string)
	if !ok {
		t.Fatalf("musicUrl result type = %T (%#v), want string", result, result)
	}
	if !strings.Contains(resolved, "cdn.example/") ||
		!strings.Contains(resolved, "MP3_320_16_Stero") ||
		strings.Contains(resolved, "token=abc") {
		t.Fatalf("resolved Migu URL = %q, want rewritten 320k CDN URL without query", resolved)
	}
	if resolveRequests != 1 {
		t.Fatalf("Haitang resolve requests = %d, want 1", resolveRequests)
	}
	if probeRequests != 1 {
		t.Fatalf("Migu quality probe requests = %d, want 1", probeRequests)
	}
}

// TestHuanyinSearchResultFlowsIntoMusicURLResolution exercises the complete
// search -> selected song metadata -> musicUrl path through the pinned source
// and native LX HTTP host. The transport is deterministic and makes no
// third-party network requests.
func TestHuanyinSearchResultFlowsIntoMusicURLResolution(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var searchRequests, resolveRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin request: %s %s", request.Method, request.URL.Redacted())
		}
		switch request.URL.Query().Get("gm") {
		case "周杰伦":
			searchRequests++
			if request.URL.Query().Get("n") != "1" || request.URL.Query().Get("num") != "1" {
				return nil, fmt.Errorf("unexpected search pagination: %s", request.URL.RawQuery)
			}
		case "周杰伦范特西":
			resolveRequests++
			if request.URL.Query().Get("n") != "1" || request.URL.Query().Get("num") != "1" {
				return nil, fmt.Errorf("unexpected resolve pagination: %s", request.URL.RawQuery)
			}
		default:
			return nil, fmt.Errorf("unexpected Huanyin keyword: %q", request.URL.Query().Get("gm"))
		}
		body := `{"code":200,"title":"周杰伦","artist":"周杰伦","album":"范特西","duration":"04:00","music_url":"https://cdn.example/jay-chou.mp3"}`
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize Huanyin source: %v", err)
	}

	searchResult, err := rt.dispatchLXRequest(ctx, "mg", "search", map[string]any{
		"keyword": "周杰伦", "page": 1, "limit": 1,
	}, client)
	if err != nil {
		t.Fatalf("search Migu through real LX host: %v", err)
	}
	items, ok := searchResult.([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("search result = %#v, want one song", searchResult)
	}
	selectedSong, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("search item type = %T, want map[string]any", items[0])
	}
	for key, want := range map[string]string{
		"name": "周杰伦", "singer": "周杰伦", "albumName": "范特西", "source": "mg",
	} {
		if got, ok := selectedSong[key].(string); !ok || got != want {
			t.Fatalf("search item %s = %#v, want %q", key, selectedSong[key], want)
		}
	}

	playResult, err := rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "320k", "musicInfo": selectedSong,
	}, client)
	if err != nil {
		t.Fatalf("resolve selected search result through musicUrl: %v", err)
	}
	playURL, ok := playResult.(string)
	if !ok || playURL != "https://cdn.example/jay-chou.mp3" {
		t.Fatalf("musicUrl result = %#v, want fixture CDN URL", playResult)
	}
	if searchRequests != 1 || resolveRequests != 1 {
		t.Fatalf("HTTP request counts: search=%d resolve=%d, want exactly one each", searchRequests, resolveRequests)
	}
}

// TestHuanyinMiguPlaybackRejectsNegativeResponsesAcrossMatchFallbacks
// exercises the pinned source's album -> singer -> title lookup sequence.
// The mocked API returns, respectively, mismatched metadata, a success code
// without a playback URL, and an explicit negative response. None may be
// accepted as a playable URL.
func TestHuanyinMiguPlaybackRejectsNegativeResponsesAcrossMatchFallbacks(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Host != "api.xcvts.cn" || request.URL.Path != "/api/music/migu" {
			return nil, fmt.Errorf("unexpected Huanyin negative-response request: %s %s", request.Method, request.URL.Redacted())
		}
		requests = append(requests, request.URL.Query().Get("gm"))
		var body string
		switch len(requests) {
		case 1:
			if requests[0] != "测试歌曲测试专辑" {
				return nil, fmt.Errorf("first lookup keyword = %q, want album match", requests[0])
			}
			body = `{"code":200,"title":"完全不同的歌曲","artist":"测试歌手","album":"测试专辑","music_url":"https://cdn.example.invalid/wrong-song.mp3"}`
		case 2:
			if requests[1] != "测试歌曲测试歌手" {
				return nil, fmt.Errorf("second lookup keyword = %q, want singer match", requests[1])
			}
			body = `{"code":200,"title":"测试歌曲","artist":"测试歌手","album":"测试专辑"}`
		case 3:
			if requests[2] != "测试歌曲" {
				return nil, fmt.Errorf("third lookup keyword = %q, want title-only fallback", requests[2])
			}
			body = `{"code":404,"message":"not found","music_url":""}`
		default:
			return nil, fmt.Errorf("unexpected extra Huanyin lookup %d", len(requests))
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Huanyin source: %v", err)
	}

	_, err = rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"name": "测试歌曲", "singer": "测试歌手", "albumName": "测试专辑",
		},
	}, client)
	if err == nil || !strings.Contains(err.Error(), "咪咕找不到") {
		t.Fatalf("negative playback response error = %v, want source-level not-found error", err)
	}
	if len(requests) != 3 {
		t.Fatalf("Huanyin Migu playback lookups = %d (%v), want exactly 3 match fallbacks", len(requests), requests)
	}
}


// TestHuanyinPlatformURLMappingCoversAllNativePlatforms verifies that the
// pinned source maps each declared LX platform and quality to the expected
// resolver URL, and rejects missing identifiers instead of emitting a
// malformed playback URL. The source itself performs no HTTP request here.
func TestHuanyinPlatformURLMappingCoversAllNativePlatforms(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "huanyin.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 10*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected network request for direct URL mapping: %s", request.URL.Redacted())
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "幻音音源", Version: "v3", Author: "竹佀", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize Huanyin source: %v", err)
	}

	tests := []struct {
		name, platform, id, quality, want string
	}{
		{"QQ 128k", "tx", "qq-song-001", "128k", "https://music-dl.sayqz.com/api/?source=qq&id=qq-song-001&type=url&br=128k"},
		{"QQ 320k", "tx", "qq-song-001", "320k", "https://music-dl.sayqz.com/api/?source=qq&id=qq-song-001&type=url&br=320k"},
		{"QQ FLAC", "tx", "qq-song-001", "flac", "https://music-dl.sayqz.com/api/?source=qq&id=qq-song-001&type=url&br=flac"},
		{"QQ FLAC 24bit", "tx", "qq-song-001", "flac24bit", "https://music-dl.sayqz.com/api/?source=qq&id=qq-song-001&type=url&br=flac24bit"},
		{"Kuwo", "kw", "kw-song-002", "320k", "https://music-dl.sayqz.com/api/?source=kuwo&id=kw-song-002&type=url&br=320k"},
		{"Netease", "wy", "wy-song-003", "flac", "https://music-dl.sayqz.com/api/?source=netease&id=wy-song-003&type=url&br=flac"},
		{"unknown quality fallback", "tx", "qq-song-001", "lossless", "https://music-dl.sayqz.com/api/?source=qq&id=qq-song-001&type=url&br=128k"},
		{"missing quality default", "tx", "qq-song-001", "", "https://music-dl.sayqz.com/api/?source=qq&id=qq-song-001&type=url&br=128k"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := map[string]any{"musicInfo": map[string]any{"songmid": tc.id}}
			if tc.quality != "" {
				info["type"] = tc.quality
			}
			result, err := rt.dispatchLXRequest(ctx, tc.platform, "musicUrl", info, client)
			if err != nil {
				t.Fatalf("dispatch musicUrl: %v", err)
			}
			got, ok := result.(string)
			if !ok || got != tc.want {
				t.Fatalf("musicUrl = %#v, want %q", result, tc.want)
			}
		})
	}

	t.Run("missing song id", func(t *testing.T) {
		_, err := rt.dispatchLXRequest(ctx, "tx", "musicUrl", map[string]any{
			"type": "320k", "musicInfo": map[string]any{"name": "Song without ID"},
		}, client)
		if err == nil || !strings.Contains(err.Error(), "没ID") {
			t.Fatalf("missing ID error = %v, want source validation error", err)
		}
	})
	t.Run("unsupported platform", func(t *testing.T) {
		_, err := rt.dispatchLXRequest(ctx, "local", "musicUrl", map[string]any{
			"type": "320k", "musicInfo": map[string]any{"songmid": "song-001"},
		}, client)
		if err == nil || !strings.Contains(err.Error(), "不支持") {
			t.Fatalf("unsupported platform error = %v, want unsupported-source error", err)
		}
	})
}


// TestHelloWorldSourceBuildsRequestTokenThroughNativeLXBuffer executes the
// pinned real source's tx musicUrl action. Its second API's before-hook calls
// lx.utils.buffer.from and bufToString to construct X-Token; the test decodes
// the captured header and verifies the token payload rather than testing a
// synthetic JavaScript reimplementation of that hook.
func TestHelloWorldSourceBuildsRequestTokenThroughNativeLXBuffer(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "helloworld.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var firstAPIRequests, tokenAPIRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Host {
		case "103.79.184.97":
			firstAPIRequests++
			return &http.Response{
				StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable",
				Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{}`)), Request: request,
			}, nil
		case "yy.zddyr.top":
			tokenAPIRequests++
			if request.Header.Get("X-Client") != "YYMusicSource/v1.0.0 (Android)" {
				return nil, fmt.Errorf("X-Client = %q", request.Header.Get("X-Client"))
			}
			encodedToken := request.Header.Get("X-Token")
			if encodedToken == "" {
				return nil, fmt.Errorf("X-Token header is empty")
			}
			tokenBytes, err := base64.StdEncoding.DecodeString(encodedToken)
			if err != nil {
				return nil, fmt.Errorf("decode X-Token base64: %w", err)
			}
			var token struct {
				DeviceID string `json:"device_id"`
				IP       string `json:"ip"`
				Timestamp int64 `json:"timestamp"`
				Random   string `json:"random"`
			}
			if err := json.Unmarshal(tokenBytes, &token); err != nil {
				return nil, fmt.Errorf("decode X-Token payload: %w", err)
			}
			if !strings.HasPrefix(token.DeviceID, "lx-online-") || token.IP != "0.0.0.0" || token.Timestamp <= 0 || token.Random == "" {
				return nil, fmt.Errorf("unexpected X-Token payload: %+v", token)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"url":"https://cdn.example/hello-world.mp3"}`)),
				Request: request,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected HelloWorld API request: %s", request.URL.Redacted())
		}
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "HelloWorld", Version: "260925", Author: "hello world", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned HelloWorld source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "tx", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"songmid": "tx-song-001",
			"name": "fixture song",
		},
	}, client)
	if err != nil {
		t.Fatalf("dispatch pinned HelloWorld musicUrl: %v", err)
	}
	if got, ok := result.(string); !ok || got != "https://cdn.example/hello-world.mp3" {
		t.Fatalf("HelloWorld musicUrl = %#v, want fixture CDN URL", result)
	}
	if firstAPIRequests != 1 || tokenAPIRequests != 1 {
		t.Fatalf("API requests: first=%d token=%d, want exactly one each", firstAPIRequests, tokenAPIRequests)
	}
}


// TestHelloWorldQQQualityFallbackRetriesLowerQualityThroughNativeLXRequest
// exercises the pinned source's quality fallback after every configured API
// fails for FLAC. The first QQ API succeeds only after the source downgrades
// the request to 320k; all traffic is intercepted by the deterministic host.
func TestHelloWorldQQQualityFallbackRetriesLowerQualityThroughNativeLXRequest(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "helloworld.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 20*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests = append(requests, request.Method+" "+request.URL.Host+request.URL.Path+"?"+request.URL.RawQuery)
		if request.URL.Host == "103.79.184.97" {
			if request.Method != http.MethodGet || request.URL.Path != "/api/music/url" {
				return nil, fmt.Errorf("unexpected QQ primary request: %s %s", request.Method, request.URL.Redacted())
			}
			if request.URL.Query().Get("source") != "tx" || request.URL.Query().Get("songId") != "qq-quality-fallback-001" {
				return nil, fmt.Errorf("unexpected QQ primary query: %s", request.URL.RawQuery)
			}
			if request.URL.Query().Get("quality") == "320k" {
				return &http.Response{
					StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
					Body: io.NopCloser(strings.NewReader(`{"url":"https://cdn.example.invalid/qq-quality-fallback-320k.mp3"}`)),
					Request: request,
				}, nil
			}
			if request.URL.Query().Get("quality") != "flac" {
				return nil, fmt.Errorf("unexpected QQ primary quality: %q", request.URL.Query().Get("quality"))
			}
		}
		switch request.URL.Host {
		case "103.79.184.97", "yy.zddyr.top", "tang.api.s01s.cn", "musicserver.haitangw.cc", "a.aa.cab":
			if request.URL.Host == "musicserver.haitangw.cc" && request.Method != http.MethodPost {
				return nil, fmt.Errorf("Haitang method = %s, want POST", request.Method)
			}
			return &http.Response{
				StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{}`)), Request: request,
			}, nil
		default:
			return nil, fmt.Errorf("unexpected HelloWorld QQ fallback request: %s %s", request.Method, request.URL.Redacted())
		}
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "HelloWorld", Version: "260925", Author: "hello world", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned HelloWorld source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "tx", "musicUrl", map[string]any{
		"type": "flac",
		"musicInfo": map[string]any{
			"songmid": "qq-quality-fallback-001",
			"name": "fixture QQ quality fallback",
		},
	}, client)
	if err != nil {
		t.Fatalf("dispatch HelloWorld QQ FLAC fallback: %v", err)
	}
	if got, ok := result.(string); !ok || got != "https://cdn.example.invalid/qq-quality-fallback-320k.mp3" {
		t.Fatalf("QQ musicUrl = %#v, want 320k fallback URL", result)
	}

	// Five configured APIs must fail in source order for FLAC. The first API
	// is then retried with 320k and succeeds; no later API should be started.
	if len(requests) != 6 {
		t.Fatalf("observed %d QQ API requests, want 5 FLAC attempts plus one 320k retry: %v", len(requests), requests)
	}
	if !strings.Contains(requests[0], "quality=flac") || !strings.Contains(requests[5], "quality=320k") {
		t.Fatalf("quality fallback request order = %v, want FLAC attempts before 320k retry", requests)
	}
	wantHosts := []string{"103.79.184.97", "yy.zddyr.top", "tang.api.s01s.cn", "musicserver.haitangw.cc", "a.aa.cab", "103.79.184.97"}
	for i, host := range wantHosts {
		if !strings.Contains(requests[i], " "+host+"/") {
			t.Errorf("request %d = %q, want host %s", i, requests[i], host)
		}
	}
}


// TestXinglanQQV2FailureFallsBackToV3ThroughNativeLXRequest exercises the
// pinned source's real QQ backend chain through the production LX host.
// The v2 endpoint deliberately returns no playable URL; v3 must then be
// requested with the same song ID and quality and its URL must be returned.
func TestXinglanQQV2FailureFallsBackToV3ThroughNativeLXRequest(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var v2Requests, v3Requests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			return &http.Response{
				StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		}
		switch {
		case request.Method == http.MethodGet &&
			request.URL.Host == "api.vkeys.cn" &&
			request.URL.Path == "/v2/music/tencent/geturl":
			mu.Lock()
			v2Requests++
			mu.Unlock()
			if request.URL.Query().Get("mid") != "qq-song-fallback-001" ||
				request.URL.Query().Get("quality") != "8" {
				return nil, fmt.Errorf("unexpected v2 query: %s", request.URL.RawQuery)
			}
			if request.Header.Get("Accept") != "application/json" {
				return nil, fmt.Errorf("v2 Accept header = %q", request.Header.Get("Accept"))
			}
			return jsonResponse(http.StatusOK, `{"code":404,"message":"fixture v2 URL unavailable"}`)
		case request.Method == http.MethodGet &&
			request.URL.Host == "api.vkeys.cn" &&
			request.URL.Path == "/music/tencent/song/link":
			mu.Lock()
			v3Requests++
			mu.Unlock()
			if request.URL.Query().Get("mid") != "qq-song-fallback-001" ||
				request.URL.Query().Get("quality") != "8" {
				return nil, fmt.Errorf("unexpected v3 query: %s", request.URL.RawQuery)
			}
			if request.Header.Get("Accept") != "application/json" {
				return nil, fmt.Errorf("v3 Accept header = %q", request.Header.Get("Accept"))
			}
			return jsonResponse(http.StatusOK, `{"code":200,"data":{"url":"https://cdn.example.invalid/qq-v3-fallback.mp3"}}`)
		case request.Method == http.MethodPost &&
			(request.URL.Host == "musicserver.haitangw.cc" || request.URL.Host == "qt.haitangw.net") &&
			request.URL.Path == "/v1/music/resolve-url":
			var body struct {
				Source string `json:"source"`
				RID string `json:"rid"`
				Level string `json:"level"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				return nil, fmt.Errorf("decode parallel Haitang request: %w", err)
			}
			if body.Source != "tx" || body.RID != "qq-song-fallback-001" || body.Level != "exhigh" {
				return nil, fmt.Errorf("unexpected parallel Haitang payload: %+v", body)
			}
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"message":"fixture backend unavailable"}`)
		case request.Method == http.MethodGet && request.URL.Host == "103.79.184.97" &&
			request.URL.Path == "/api/music/url":
			if request.URL.Query().Get("platform") != "tx" ||
				request.URL.Query().Get("songId") != "qq-song-fallback-001" ||
				request.URL.Query().Get("quality") != "320k" {
				return nil, fmt.Errorf("unexpected HYWmusic query: %s", request.URL.RawQuery)
			}
			return jsonResponse(http.StatusForbidden, `{"code":403,"message":"fixture key unavailable"}`)
		default:
			return nil, fmt.Errorf("unexpected Xinglan QQ request: %s %s", request.Method, request.URL.Redacted())
		}
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "星澜聚合音源", Version: "v4.0.2", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}

	result, err := rt.dispatchLXRequest(ctx, "tx", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"songmid": "qq-song-fallback-001",
			"name": "fixture QQ song",
		},
	}, client)
	if err != nil {
		t.Fatalf("dispatch Xinglan QQ musicUrl with v2 failure and v3 fallback: %v", err)
	}
	got, ok := result.(string)
	if !ok || got != "https://cdn.example.invalid/qq-v3-fallback.mp3" {
		t.Fatalf("QQ musicUrl = %#v, want v3 fallback URL", result)
	}
	mu.Lock()
	gotV2, gotV3 := v2Requests, v3Requests
	mu.Unlock()
	if gotV2 != 1 || gotV3 != 1 {
		t.Fatalf("Luoyue requests: v2=%d v3=%d, want exactly one each", gotV2, gotV3)
	}
}

// TestXinglanQQBackendPoolReportsAllFailures exercises the pinned source's
// complete QQ backend pool. The first three providers fail concurrently;
// every remaining provider must be attempted in source order and its failure
// must be represented in the final aggregate error.
func TestXinglanQQBackendPoolReportsAllFailures(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.Host+request.URL.Path)
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status: http.StatusText(http.StatusServiceUnavailable),
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"code":503,"message":"deterministic backend failure"}`)),
			Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "星澜聚合音源", Version: "v4.0.2", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}

	_, err = rt.dispatchLXRequest(ctx, "tx", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"songmid": "qq-song-all-backends-fail",
			"name": "fixture QQ song",
		},
	}, client)
	if err == nil {
		t.Fatal("expected all QQ backends to fail")
	}
	message := err.Error()
	for _, want := range []string{
		"所有后端均失败（共 13 个）",
		"落月QQ:",
		"云记QQ(海堂):",
		"HYWmusic QQ:",
		"baimusic QQ:",
		"QQ官方:",
		"星海主后端:",
		"HelloWorld QQ:",
		"vkeys:",
		"317ak:",
		"念心直链:",
		"FishAPI:",
		"ygking QQ:",
		"FFAPI:",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("aggregate error missing %q: %s", want, message)
		}
	}
	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	if len(gotRequests) < 13 {
		t.Fatalf("observed %d HTTP requests, want at least 13 across QQ backend pool: %v", len(gotRequests), gotRequests)
	}
}

// TestXinglanQQPrimarySuccessSkipsSequentialFallbacks verifies that a valid
// Luoyue v2 URL wins the concurrent head group and prevents every sequential
// fallback backend from being contacted. The other two head providers are
// deliberately failed so the result does not depend on request scheduling.
// TestXinglanMiguBackendPoolReportsAllFailures exercises every configured
// Migu resolver against deterministic HTTP failures and verifies that the
// source reports the complete seven-provider failure rather than returning
// an empty or misleading playback URL.
func TestXinglanQsPrimaryFailureFallsBackAndAppendsKey(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var primaryRequests, backupRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/lx/api/" {
			return nil, fmt.Errorf("unexpected Xinglan QS request: %s %s", request.Method, request.URL.Redacted())
		}
		if request.Header.Get("X-Client") != "XingHaiMusicSource" ||
			request.Header.Get("User-Agent") != "Mozilla/5.0" {
			return nil, fmt.Errorf("unexpected Xinglan QS headers: %#v", request.Header)
		}
		query := request.URL.Query()
		for key, want := range map[string]string{
			"source": "qs", "name": "fixture 汽水歌曲", "singer": "fixture singer",
			"songmid": "qs-song-fallback-001", "interval": "03:21",
			"albumName": "fixture album", "quality": "320k",
		} {
			if got := query.Get(key); got != want {
				return nil, fmt.Errorf("QS query %s = %q, want %q (raw %s)", key, got, want, request.URL.RawQuery)
			}
		}

		status, body := http.StatusServiceUnavailable,
			`{"code":503,"msg":"deterministic primary route failure"}`
		mu.Lock()
		switch request.URL.Host {
		case "yy.zddyr.top":
			primaryRequests++
		case "zrcdy.dpdns.org":
			backupRequests++
			status, body = http.StatusOK,
				`{"code":200,"data":{"url":"https://cdn.example.invalid/qs/audio.m4a?token=fixture","key":"key value/+","source_url":"https://unused.example.invalid/source"}}`
		default:
			mu.Unlock()
			return nil, fmt.Errorf("unexpected Xinglan QS host: %s", request.URL.Host)
		}
		mu.Unlock()
		return &http.Response{
			StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "星澜聚合音源", Version: "v4.0.2", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}

	result, err := rt.dispatchLXRequest(ctx, "qs", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"songmid": "qs-song-fallback-001",
			"name": "fixture 汽水歌曲",
			"singer": "fixture singer",
			"interval": "03:21",
			"albumName": "fixture album",
		},
	}, client)
	if err != nil {
		t.Fatalf("dispatch Xinglan QS musicUrl after primary route failure: %v", err)
	}
	gotURL, ok := result.(string)
	if !ok {
		t.Fatalf("QS musicUrl result type = %T (%#v), want string", result, result)
	}
	parsed, err := url.Parse(gotURL)
	if err != nil {
		t.Fatalf("parse resolved QS URL %q: %v", gotURL, err)
	}
	if parsed.Host != "cdn.example.invalid" || parsed.Path != "/qs/audio.m4a" ||
		parsed.Query().Get("token") != "fixture" || parsed.Query().Get("key") != "key value/+" {
		t.Fatalf("resolved QS URL = %q, want backup URL with correctly encoded key", gotURL)
	}
	mu.Lock()
	defer mu.Unlock()
	if primaryRequests != 1 || backupRequests != 1 {
		t.Fatalf("QS endpoint requests: primary=%d backup=%d, want one each", primaryRequests, backupRequests)
	}
}

func TestXinglanMiguBackendPoolReportsAllFailures(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.Host+request.URL.Path)
		mu.Unlock()
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Status: http.StatusText(http.StatusServiceUnavailable),
			Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{"code":503,"message":"deterministic Migu backend failure"}`)),
			Request: request,
		}, nil
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "星澜聚合音源", Version: "v4.0.2", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}

	_, err = rt.dispatchLXRequest(ctx, "mg", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"songmid": "migu-song-all-backends-fail",
			"name": "fixture Migu song",
			"singer": "fixture singer",
		},
	}, client)
	if err == nil {
		t.Fatal("expected all Migu backends to fail")
	}
	message := err.Error()
	for _, want := range []string{
		"所有后端均失败（共 7 个）",
		"云记咪咕(海堂):",
		"星海主后端:",
		"Migu直接源:",
		"Migu API:",
		"HYWmusic MG:",
		"星海咪咕:",
		"FFAPI:",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("Migu aggregate error missing %q: %s", want, message)
		}
	}
	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	if len(gotRequests) < 7 {
		t.Fatalf("observed %d HTTP requests, want requests from all Migu backend paths: %v", len(gotRequests), gotRequests)
	}
}

func TestXinglanQQPrimarySuccessSkipsSequentialFallbacks(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.Host+request.URL.Path)
		mu.Unlock()

		jsonResponse := func(status int, body string) (*http.Response, error) {
			return &http.Response{
				StatusCode: status, Status: http.StatusText(status), Header: make(http.Header),
				Body: io.NopCloser(strings.NewReader(body)), Request: request,
			}, nil
		}

		switch {
		case request.Method == http.MethodGet &&
			request.URL.Host == "api.vkeys.cn" &&
			request.URL.Path == "/v2/music/tencent/geturl":
			if request.URL.Query().Get("mid") != "qq-song-primary-success" ||
				request.URL.Query().Get("quality") != "8" {
				return nil, fmt.Errorf("unexpected Luoyue v2 query: %s", request.URL.RawQuery)
			}
			return jsonResponse(http.StatusOK, `{"code":200,"data":{"url":"https://cdn.example.invalid/qq-primary-v2.mp3"}}`)
		case request.Method == http.MethodPost &&
			(request.URL.Host == "musicserver.haitangw.cc" || request.URL.Host == "qt.haitangw.net") &&
			request.URL.Path == "/v1/music/resolve-url":
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"message":"deterministic Haitang failure"}`)
		case request.Method == http.MethodGet &&
			request.URL.Host == "103.79.184.97" &&
			request.URL.Path == "/api/music/url":
			return jsonResponse(http.StatusForbidden, `{"code":403,"message":"deterministic HYW failure"}`)
		default:
			return nil, fmt.Errorf("unexpected Xinglan QQ request: %s %s", request.Method, request.URL.Redacted())
		}
	})}

	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "星澜聚合音源", Version: "v4.0.2", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}

	result, err := rt.dispatchLXRequest(ctx, "tx", "musicUrl", map[string]any{
		"type": "320k",
		"musicInfo": map[string]any{
			"songmid": "qq-song-primary-success",
			"name": "fixture QQ song",
		},
	}, client)
	if err != nil {
		t.Fatalf("dispatch Xinglan QQ musicUrl with primary success: %v", err)
	}
	if got, ok := result.(string); !ok || got != "https://cdn.example.invalid/qq-primary-v2.mp3" {
		t.Fatalf("QQ musicUrl = %#v, want Luoyue v2 URL", result)
	}

	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	// The three head providers run concurrently. Haitang itself tries its two
	// configured hosts sequentially, so four HTTP requests are expected.
	if len(gotRequests) != 4 {
		t.Fatalf("observed %d HTTP requests, want 4 requests from the three head providers (including Haitang's two hosts): %v", len(gotRequests), gotRequests)
	}
	var luoyueV2, haitangPrimary, haitangSecondary, hyw int
	for _, got := range gotRequests {
		switch {
		case got == "GET api.vkeys.cn/v2/music/tencent/geturl":
			luoyueV2++
		case got == "POST musicserver.haitangw.cc/v1/music/resolve-url":
			haitangPrimary++
		case got == "POST qt.haitangw.net/v1/music/resolve-url":
			haitangSecondary++
		case got == "GET 103.79.184.97/api/music/url":
			hyw++
		default:
			t.Errorf("unexpected backend request: %s", got)
		}
	}
	if luoyueV2 != 1 || haitangPrimary != 1 || haitangSecondary != 1 || hyw != 1 {
		t.Fatalf("head-provider request counts: Luoyue v2=%d Haitang primary=%d Haitang secondary=%d HYW=%d, want one request per configured endpoint; requests=%v",
			luoyueV2, haitangPrimary, haitangSecondary, hyw, gotRequests)
	}
}


// Kuwo requests are routed through Xinghai's stream endpoint for every
// declared quality tier. The source constructs the stream URL locally; this
// matrix verifies the complete quality-to-level mapping without resolver HTTP.
// TestXinglanKuwoStandardQualityUsesProviderPool verifies that a standard
// Kuwo tier bypasses the local stream URL and reaches the ordinary provider pool.
func TestXinglanKuwoStandardQualityUsesProviderPool(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var starRequests, backupRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header,
				Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		}
		if request.URL.Host == "c.wwwweb.top" {
			if request.Method != http.MethodPost || request.URL.Path != "/music/url" {
				return nil, fmt.Errorf("unexpected HelloWorld Kuwo backup request: %s %s", request.Method, request.URL.Redacted())
			}
			if request.Header.Get("Content-Type") != "application/json" ||
				request.Header.Get("User-Agent") != "lx-music-desktop/2.10.1" {
				return nil, fmt.Errorf("unexpected HelloWorld Kuwo backup headers: %#v", request.Header)
			}
			var body struct {
				Source string `json:"source"`
				MusicID string `json:"musicId"`
				Quality string `json:"quality"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				return nil, fmt.Errorf("decode Kuwo backup payload: %w", err)
			}
			if body.Source != "kw" || body.MusicID != "kw-standard-song-001" || body.Quality != "320k" {
				return nil, fmt.Errorf("unexpected Kuwo backup payload: %+v", body)
			}
			mu.Lock(); backupRequests++; mu.Unlock()
			return jsonResponse(http.StatusOK, `{"code":200,"url":"https://cdn.example.invalid/kw-standard-320k.mp3"}`)
		}
		mu.Lock(); starRequests++; mu.Unlock()
		return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"msg":"deterministic provider failure"}`)
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
		"type":"320k", "musicInfo":map[string]any{"songmid":"kw-standard-song-001","name":"fixture Kuwo standard song","singer":"fixture singer"},
	}, client)
	if err != nil { t.Fatalf("dispatch standard Kuwo musicUrl: %v", err) }
	got, ok := result.(string)
	if !ok || got != "https://cdn.example.invalid/kw-standard-320k.mp3" {
		t.Fatalf("Kuwo standard musicUrl = %#v, want checked provider URL", result)
	}
	mu.Lock(); gotStar, gotBackup := starRequests, backupRequests; mu.Unlock()
	if gotStar != 1 || gotBackup != 1 {
		t.Fatalf("Kuwo standard provider requests: star=%d backup=%d, want exactly one each", gotStar, gotBackup)
	}
}


func TestXinglanKuwoOfficialFallbackGETContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var starRequests, backupRequests, officialRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header,
				Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		}
		switch request.URL.Host {
		case "yy.zddyr.top":
			if request.Method != http.MethodGet {
				return nil, fmt.Errorf("Xinghai Kuwo method = %s, want GET", request.Method)
			}
			mu.Lock(); starRequests++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"msg":"deterministic primary failure"}`)
		case "c.wwwweb.top":
			if request.Method != http.MethodPost || request.URL.Path != "/music/url" {
				return nil, fmt.Errorf("unexpected HelloWorld Kuwo backup request: %s %s", request.Method, request.URL.Redacted())
			}
			if request.Header.Get("Content-Type") != "application/json" ||
				request.Header.Get("User-Agent") != "lx-music-desktop/2.10.1" {
				return nil, fmt.Errorf("unexpected HelloWorld Kuwo backup headers: %#v", request.Header)
			}
			var body struct {
				Source string `json:"source"`
				MusicID string `json:"musicId"`
				Quality string `json:"quality"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				return nil, fmt.Errorf("decode Kuwo backup payload: %w", err)
			}
			if body.Source != "kw" || body.MusicID != "kw-official-fallback-001" || body.Quality != "320k" {
				return nil, fmt.Errorf("unexpected Kuwo backup payload: %+v", body)
			}
			mu.Lock(); backupRequests++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"message":"deterministic backup failure"}`)
		case "mobi.kuwo.cn":
			if request.Method != http.MethodGet {
				return nil, fmt.Errorf("Kuwo official method = %s, want GET", request.Method)
			}
			query := request.URL.Query()
			if query.Get("rid") != "kw-official-fallback-001" ||
				query.Get("br") != "320kmp3" ||
				query.Get("source") != "jiakong" ||
				query.Get("type") != "convert_url_with_sign" ||
				query.Get("surl") != "1" {
				return nil, fmt.Errorf("unexpected Kuwo official query: %s", request.URL.RawQuery)
			}
			if !strings.Contains(request.Header.Get("User-Agent"), "Android") {
				return nil, fmt.Errorf("Kuwo official User-Agent = %q, want Android mobile UA", request.Header.Get("User-Agent"))
			}
			mu.Lock(); officialRequests++; mu.Unlock()
			return jsonResponse(http.StatusOK, `{"code":200,"data":{"surl":"https://cdn.example.invalid/kw-official-320k.mp3"}}`)
		default:
			return nil, fmt.Errorf("unexpected Xinglan Kuwo backend host: %s", request.URL.Host)
		}
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
		"type":"320k", "musicInfo":map[string]any{
			"songmid":"kw-official-fallback-001", "rid":"kw-official-fallback-001",
			"name":"fixture Kuwo fallback song", "singer":"fixture singer",
		},
	}, client)
	if err != nil { t.Fatalf("dispatch Kuwo official fallback: %v", err) }
	got, ok := result.(string)
	if !ok || got != "https://cdn.example.invalid/kw-official-320k.mp3" {
		t.Fatalf("Kuwo official fallback URL = %#v, want checked official response URL", result)
	}
	mu.Lock()
	gotStar, gotBackup, gotOfficial := starRequests, backupRequests, officialRequests
	mu.Unlock()
	if gotStar != 1 || gotBackup != 1 || gotOfficial != 1 {
		t.Fatalf("Kuwo fallback request counts: Xinghai=%d HelloWorldBackup=%d KuwoOfficial=%d, want exactly one each", gotStar, gotBackup, gotOfficial)
	}
}

func TestXinglanKuwoHighQualityUsesStreamBackendOnly(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var requests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		return nil, fmt.Errorf("unexpected HTTP request for Kuwo high-quality stream route: %s %s", request.Method, request.URL.Redacted())
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{
		Name: "星澜聚合音源", Version: "v4.0.2", RawScript: string(source),
	}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}

	for _, tc := range []struct {
		quality string
		level   string
	}{
		{quality: "flac24bit", level: "flac"},
		{quality: "atmos", level: "atmos"},
		{quality: "atmos_plus", level: "atmos_plus"},
		{quality: "master", level: "master"},
	} {
		t.Run(tc.quality, func(t *testing.T) {
			result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
				"type": tc.quality,
				"musicInfo": map[string]any{
					"songmid": "kw-stream-song-001",
					"id": "kw-fallback-id-must-not-win",
				},
			}, client)
			if err != nil {
				t.Fatalf("dispatch Xinglan Kuwo %s musicUrl: %v", tc.quality, err)
			}
			got, ok := result.(string)
			if !ok {
				t.Fatalf("Kuwo %s musicUrl result type = %T (%#v), want string", tc.quality, result, result)
			}
			want := "http://175.27.166.236:8928/kwstream?id=kw-stream-song-001&level=" + tc.level + "&stream=1"
			if got != want {
				t.Fatalf("Kuwo %s stream URL = %q, want %q", tc.quality, got, want)
			}
		})
	}
	if requests != 0 {
		t.Fatalf("Kuwo high-quality routes issued %d HTTP requests, want zero (stream URL is constructed locally)", requests)
	}
}


func TestXinglanKuwoMobileFallbackGETContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	var starRequests, backupRequests, officialRequests, mobileRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header,
				Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		}
		switch request.URL.Host {
		case "yy.zddyr.top":
			if request.Method != http.MethodGet { return nil, fmt.Errorf("Xinghai Kuwo method = %s, want GET", request.Method) }
			mu.Lock(); starRequests++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"msg":"deterministic primary failure"}`)
		case "c.wwwweb.top":
			if request.Method != http.MethodPost || request.URL.Path != "/music/url" {
				return nil, fmt.Errorf("unexpected HelloWorld Kuwo backup request: %s %s", request.Method, request.URL.Redacted())
			}
			var body struct {
				Source string `json:"source"`
				MusicID string `json:"musicId"`
				Quality string `json:"quality"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil { return nil, fmt.Errorf("decode Kuwo backup payload: %w", err) }
			if body.Source != "kw" || body.MusicID != "kw-mobile-fallback-001" || body.Quality != "320k" {
				return nil, fmt.Errorf("unexpected Kuwo backup payload: %+v", body)
			}
			mu.Lock(); backupRequests++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"message":"deterministic backup failure"}`)
		case "mobi.kuwo.cn":
			if request.Method != http.MethodGet { return nil, fmt.Errorf("Kuwo official method = %s, want GET", request.Method) }
			q := request.URL.Query()
			if q.Get("rid") != "kw-mobile-fallback-001" || q.Get("br") != "320kmp3" ||
				q.Get("source") != "jiakong" || q.Get("type") != "convert_url_with_sign" || q.Get("surl") != "1" {
				return nil, fmt.Errorf("unexpected Kuwo official query: %s", request.URL.RawQuery)
			}
			mu.Lock(); officialRequests++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"msg":"deterministic official failure"}`)
		case "nmobi.kuwo.cn":
			if request.Method != http.MethodGet || request.URL.Path != "/mobi.s" {
				return nil, fmt.Errorf("Kuwo mobile request = %s %s, want GET /mobi.s", request.Method, request.URL.Redacted())
			}
			q := request.URL.Query()
			if q.Get("rid") != "kw-mobile-fallback-001" || q.Get("br") != "320kmp3" ||
				q.Get("f") != "web" || q.Get("user") != "0" || q.Get("type") != "convert_url_with_sign" {
				return nil, fmt.Errorf("unexpected Kuwo mobile query: %s", request.URL.RawQuery)
			}
			if !strings.Contains(request.Header.Get("User-Agent"), "Android") {
				return nil, fmt.Errorf("Kuwo mobile User-Agent = %q, want Android mobile UA", request.Header.Get("User-Agent"))
			}
			mu.Lock(); mobileRequests++; mu.Unlock()
			return jsonResponse(http.StatusOK, `{"code":200,"data":{"url":"https://cdn.example.invalid/kw-mobile-320k.mp3"}}`)
		default:
			return nil, fmt.Errorf("unexpected Xinglan Kuwo backend host: %s", request.URL.Host)
		}
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
		"type":"320k", "musicInfo":map[string]any{
			"songmid":"kw-mobile-fallback-001", "rid":"kw-mobile-fallback-001",
			"name":"fixture Kuwo mobile fallback song", "singer":"fixture singer",
		},
	}, client)
	if err != nil { t.Fatalf("dispatch Kuwo mobile fallback: %v", err) }
	got, ok := result.(string)
	if !ok || got != "https://cdn.example.invalid/kw-mobile-320k.mp3" {
		t.Fatalf("Kuwo mobile fallback URL = %#v, want checked mobile response URL", result)
	}
	mu.Lock()
	gotStar, gotBackup, gotOfficial, gotMobile := starRequests, backupRequests, officialRequests, mobileRequests
	mu.Unlock()
	if gotStar != 1 || gotBackup != 1 || gotOfficial != 1 || gotMobile != 1 {
		t.Fatalf("Kuwo fallback request counts: Xinghai=%d HelloWorldBackup=%d KuwoOfficial=%d KuwoMobile=%d, want exactly one each", gotStar, gotBackup, gotOfficial, gotMobile)
	}
}


func TestXinglanHYWMusicKuwoGETContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil {
		t.Fatal(err)
	}
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var mu sync.Mutex
	counts := map[string]int{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header,
				Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
		}
		switch request.URL.Host {
		case "yy.zddyr.top":
			if request.Method != http.MethodGet {
				return nil, fmt.Errorf("Xinghai Kuwo method = %s, want GET", request.Method)
			}
			mu.Lock(); counts["xinghai"]++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"msg":"deterministic primary failure"}`)
		case "c.wwwweb.top":
			if request.Method != http.MethodPost || request.URL.Path != "/music/url" {
				return nil, fmt.Errorf("unexpected HelloWorld Kuwo backup request: %s %s", request.Method, request.URL.Redacted())
			}
			mu.Lock(); counts["helloworld"]++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"message":"deterministic backup failure"}`)
		case "mobi.kuwo.cn":
			if request.Method != http.MethodGet {
				return nil, fmt.Errorf("Kuwo official method = %s, want GET", request.Method)
			}
			mu.Lock(); counts["official"]++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"msg":"deterministic official failure"}`)
		case "nmobi.kuwo.cn":
			if request.Method != http.MethodGet || request.URL.Path != "/mobi.s" {
				return nil, fmt.Errorf("Kuwo mobile request = %s %s, want GET /mobi.s", request.Method, request.URL.Redacted())
			}
			mu.Lock(); counts["mobile"]++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"msg":"deterministic mobile failure"}`)
		case "music.nxinxz.com":
			if request.Method != http.MethodGet || request.URL.Path != "/kgqq/kw.php" {
				return nil, fmt.Errorf("Nianxin Kuwo request = %s %s, want GET /kgqq/kw.php", request.Method, request.URL.Redacted())
			}
			q := request.URL.Query()
			if q.Get("id") != "kw-hyw-contract-001" || q.Get("level") != "standard" || q.Get("type") != "mp3" {
				return nil, fmt.Errorf("unexpected Nianxin Kuwo query: %s", request.URL.RawQuery)
			}
			mu.Lock(); counts["nianxin"]++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, `{"code":503,"msg":"deterministic Nianxin failure"}`)
		case "103.79.184.97":
			if request.Method != http.MethodGet || request.URL.Path != "/api/music/url" {
				return nil, fmt.Errorf("HYWmusic Kuwo request = %s %s, want GET /api/music/url", request.Method, request.URL.Redacted())
			}
			q := request.URL.Query()
			want := map[string]string{
				"songId":"kw-hyw-contract-001", "songmid":"kw-hyw-contract-001",
				"rid":"kw-hyw-contract-001", "name":"fixture HYW song",
				"singer":"fixture singer", "platform":"kw", "source":"kw",
				"quality":"128k", "key":"6C1F-53W0-GRKI-EVFG",
			}
			for key, value := range want {
				if q.Get(key) != value {
					return nil, fmt.Errorf("HYWmusic Kuwo query %s = %q, want %q (raw %s)", key, q.Get(key), value, request.URL.RawQuery)
				}
			}
			if request.Header.Get("Accept") != "application/json" ||
				request.Header.Get("User-Agent") != "Mozilla/5.0" {
				return nil, fmt.Errorf("unexpected HYWmusic headers: %#v", request.Header)
			}
			mu.Lock(); counts["hyw"]++; mu.Unlock()
			return jsonResponse(http.StatusOK, `{"code":200,"data":{"url":"https://cdn.example.invalid/kw-hyw-128k.mp3"}}`)
		default:
			return nil, fmt.Errorf("unexpected Xinglan Kuwo backend host: %s", request.URL.Host)
		}
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
		"type":"128k", "musicInfo":map[string]any{
			"songmid":"kw-hyw-contract-001", "rid":"kw-hyw-contract-001",
			"name":"fixture HYW song", "singer":"fixture singer",
		},
	}, client)
	if err != nil {
		t.Fatalf("dispatch HYWmusic Kuwo fallback: %v", err)
	}
	got, ok := result.(string)
	if !ok || got != "https://cdn.example.invalid/kw-hyw-128k.mp3" {
		t.Fatalf("HYWmusic Kuwo fallback URL = %#v, want checked HYWmusic response URL", result)
	}
	mu.Lock()
	gotCounts := map[string]int{}
	for key, value := range counts { gotCounts[key] = value }
	mu.Unlock()
	wantCounts := map[string]int{"xinghai":1, "helloworld":1, "official":1, "mobile":1, "nianxin":1, "hyw":1}
	for key, want := range wantCounts {
		if gotCounts[key] != want {
			t.Fatalf("Kuwo fallback %s requests = %d, want %d (all counts: %+v)", key, gotCounts[key], want, gotCounts)
		}
	}
}


func TestXinglanKuwoYunmgeXinghaiAndFFAPIFallbackContracts(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }

	for _, target := range []string{"yunmge", "xinghai", "ffapi"} {
		t.Run(target, func(t *testing.T) {
			rt := newTestRuntime(t, 15*time.Second)
			defer rt.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			counts := map[string]int{}
			var mu sync.Mutex
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				jsonResponse := func(status int, body string) (*http.Response, error) {
					header := make(http.Header)
					header.Set("Content-Type", "application/json")
					return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: header,
						Body: io.NopCloser(strings.NewReader(body)), Request: request}, nil
				}
				fail := func() (*http.Response, error) {
					return jsonResponse(http.StatusServiceUnavailable, "{\"code\":503,\"message\":\"deterministic provider failure\"}")
				}
				switch request.URL.Host {
				case "yy.zddyr.top":
					q := request.URL.Query()
					if q.Get("source") != "kw" || q.Get("songmid") != "kw-remaining-fallback-001" {
						return nil, fmt.Errorf("unexpected Xinghai primary Kuwo query: %s", request.URL.RawQuery)
					}
					if _, primary := q["interval"]; !primary {
						return nil, fmt.Errorf("Xinghai primary query missing interval field: %s", request.URL.RawQuery)
					}
					mu.Lock(); counts["xinghai_primary"]++; mu.Unlock()
					return fail()
				case "api.xinghai.com":
					q := request.URL.Query()
					if q.Get("source") != "kw" || q.Get("songmid") != "kw-remaining-fallback-001" ||
						q.Get("name") != "fixture Kuwo fallback song fixture singer" || q.Get("quality") != "320kmp3" {
						return nil, fmt.Errorf("unexpected Xinghai Kuwo fallback query: %s", request.URL.RawQuery)
					}
					mu.Lock(); counts["xinghai_kw"]++; mu.Unlock()
					if target == "xinghai" {
						return jsonResponse(http.StatusOK, "{\"code\":200,\"url\":\"https://cdn.example.invalid/kw-xinghai-320k.mp3\"}")
					}
					return fail()
				case "c.wwwweb.top":
					if request.Method != http.MethodPost || request.URL.Path != "/music/url" {
						return nil, fmt.Errorf("unexpected HelloWorld Kuwo request: %s %s", request.Method, request.URL.Redacted())
					}
					mu.Lock(); counts["helloworld_backup"]++; mu.Unlock()
					return fail()
				case "mobi.kuwo.cn":
					if request.Method != http.MethodGet {
						return nil, fmt.Errorf("Kuwo official method = %s, want GET", request.Method)
					}
					mu.Lock(); counts["kuwo_official"]++; mu.Unlock()
					return fail()
				case "nmobi.kuwo.cn":
					if request.Method != http.MethodGet || request.URL.Path != "/mobi.s" {
						return nil, fmt.Errorf("Kuwo mobile request = %s %s, want GET /mobi.s", request.Method, request.URL.Redacted())
					}
					mu.Lock(); counts["kuwo_mobile"]++; mu.Unlock()
					return fail()
				case "music.nxinxz.com":
					if request.Method != http.MethodGet || request.URL.Path != "/kgqq/kw.php" {
						return nil, fmt.Errorf("Nianxin Kuwo request = %s %s, want GET /kgqq/kw.php", request.Method, request.URL.Redacted())
					}
					q := request.URL.Query()
					if q.Get("id") != "kw-remaining-fallback-001" || q.Get("level") != "exhigh" || q.Get("type") != "mp3" {
						return nil, fmt.Errorf("unexpected Nianxin Kuwo query: %s", request.URL.RawQuery)
					}
					mu.Lock(); counts["nianxin"]++; mu.Unlock()
					return fail()
				case "103.79.184.97":
					if request.Method != http.MethodGet || request.URL.Path != "/api/music/url" {
						return nil, fmt.Errorf("HYWmusic request = %s %s, want GET /api/music/url", request.Method, request.URL.Redacted())
					}
					mu.Lock(); counts["hyw"]++; mu.Unlock()
					return fail()
				case "api.yunmge.com":
					if request.Method != http.MethodGet || request.URL.Path != "/kuwo" {
						return nil, fmt.Errorf("yunmge request = %s %s, want GET /kuwo", request.Method, request.URL.Redacted())
					}
					q := request.URL.Query()
					for key, want := range map[string]string{
						"key":"yunmge_key", "token":"yunmge_token", "id":"kw-remaining-fallback-001",
					} {
						if q.Get(key) != want {
							return nil, fmt.Errorf("yunmge query %s = %q, want %q (raw %s)", key, q.Get(key), want, request.URL.RawQuery)
						}
					}
					mu.Lock(); counts["yunmge"]++; mu.Unlock()
					if target == "yunmge" {
						return jsonResponse(http.StatusOK, "{\"code\":200,\"data\":{\"all_bitrates\":[{\"bitrate\":128,\"play_url\":\"https://cdn.example.invalid/kw-yunmge-128k.mp3\"},{\"bitrate\":320,\"play_url\":\"https://cdn.example.invalid/kw-yunmge-320k.mp3\"}]}}")
					}
					return fail()
				case "ffapi.cn":
					if request.Method != http.MethodGet || request.URL.Path != "/int/v2/songurl" {
						return nil, fmt.Errorf("FFAPI request = %s %s, want GET /int/v2/songurl", request.Method, request.URL.Redacted())
					}
					if got := request.URL.Query().Get("url"); got != "https://www.kuwo.cn/play_detail/kw-remaining-fallback-001" {
						return nil, fmt.Errorf("FFAPI page URL = %q, want Kuwo play_detail URL", got)
					}
					mu.Lock(); counts["ffapi"]++; mu.Unlock()
					if target == "ffapi" {
						return jsonResponse(http.StatusOK, "{\"code\":200,\"data\":{\"play_url\":\"https://cdn.example.invalid/kw-ffapi-320k.mp3\"}}")
					}
					return fail()
				default:
					return nil, fmt.Errorf("unexpected Xinglan Kuwo backend host: %s", request.URL.Host)
				}
			})}

			if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
				t.Fatalf("initialize pinned Xinglan source: %v", err)
			}
			result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
				"type":"320k",
				"musicInfo":map[string]any{
					"songmid":"kw-remaining-fallback-001", "rid":"kw-remaining-fallback-001",
					"source":"kw", "name":"fixture Kuwo fallback song", "singer":"fixture singer",
				},
			}, client)
			if err != nil { t.Fatalf("dispatch Kuwo fallback target %s: %v", target, err) }
			got, ok := result.(string)
			if !ok { t.Fatalf("Kuwo fallback result = %#v (%T), want string", result, result) }
			wantURL := map[string]string{
				"yunmge":"https://cdn.example.invalid/kw-yunmge-320k.mp3",
				"xinghai":"https://cdn.example.invalid/kw-xinghai-320k.mp3",
				"ffapi":"https://cdn.example.invalid/kw-ffapi-320k.mp3",
			}[target]
			if got != wantURL { t.Fatalf("Kuwo %s URL = %q, want %q", target, got, wantURL) }

			mu.Lock()
			gotCounts := map[string]int{}
			for key, value := range counts { gotCounts[key] = value }
			mu.Unlock()
			for _, key := range []string{"xinghai_primary", "helloworld_backup", "kuwo_official", "kuwo_mobile", "nianxin", "hyw", "yunmge"} {
				if gotCounts[key] != 1 {
					t.Errorf("Kuwo %s fallback %s requests = %d, want 1 (all counts: %+v)", target, key, gotCounts[key], gotCounts)
				}
			}
			if target == "yunmge" && gotCounts["xinghai_kw"] != 0 {
				t.Errorf("yunmge success should stop before Xinghai fallback, got counts: %+v", gotCounts)
			}
			if target != "yunmge" && gotCounts["xinghai_kw"] != 1 {
				t.Errorf("%s fallback should reach Xinghai once, got counts: %+v", target, gotCounts)
			}
			if target == "ffapi" && gotCounts["ffapi"] != 1 {
				t.Errorf("FFAPI fallback requests = %d, want 1", gotCounts["ffapi"])
			}
		})
	}
}

func TestXinglanNeteaseAndKugouBackendPoolsReportAllFailures(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }

	for _, tc := range []struct {
		source string
		quality string
		count int
		backendNames []string
	}{
		{source:"wy", quality:"320k", count:9, backendNames:[]string{
			"网易云官方:", "ikun网易云:", "笒鬼鬼:", "星海主后端:", "念心直链:",
			"FishAPI:", "HYWmusic WY:", "残像 WY:", "FFAPI:",
		}},
		{source:"kg", quality:"320k", count:11, backendNames:[]string{
			"海堂KG:", "HYWmusic KG:", "白姬KG:", "星海KG:", "星海主后端:",
			"长青海棠:", "酷狗官方:", "念心KG:", "星海酷狗:", "念心酷狗:", "FFAPI:",
		}},
	} {
		t.Run(tc.source, func(t *testing.T) {
			rt := newTestRuntime(t, 15*time.Second)
			defer rt.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var mu sync.Mutex
			var requests []string
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				mu.Lock()
				requests = append(requests, request.Method+" "+request.URL.Host+request.URL.Path)
				mu.Unlock()
				return &http.Response{
					StatusCode:http.StatusServiceUnavailable, Status:http.StatusText(http.StatusServiceUnavailable),
					Header:make(http.Header), Body:io.NopCloser(strings.NewReader("{\"code\":503,\"message\":\"deterministic provider failure\"}")),
					Request:request,
				}, nil
			})}
			if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
				t.Fatalf("initialize pinned Xinglan source: %v", err)
			}
			_, err := rt.dispatchLXRequest(ctx, tc.source, "musicUrl", map[string]any{
				"type":tc.quality,
				"musicInfo":map[string]any{
					"songmid":"xinglan-"+tc.source+"-all-fail-001", "hash":"xinglan-"+tc.source+"-hash-001",
					"rid":"xinglan-"+tc.source+"-rid-001", "id":"xinglan-"+tc.source+"-id-001",
					"name":"fixture Xinglan song", "songName":"fixture Xinglan song",
					"singer":"fixture singer", "albumId":"fixture-album-001", "source":tc.source,
				},
			}, client)
			if err == nil { t.Fatalf("expected all %s backends to fail", tc.source) }
			message := err.Error()
			if !strings.Contains(message, fmt.Sprintf("所有后端均失败（共 %d 个）", tc.count)) {
				t.Errorf("%s aggregate error does not report %d backends: %s", tc.source, tc.count, message)
			}
			for _, name := range tc.backendNames {
				if !strings.Contains(message, name) { t.Errorf("%s aggregate error missing %q: %s", tc.source, name, message) }
			}
			mu.Lock()
			gotRequests := append([]string(nil), requests...)
			mu.Unlock()
			if len(gotRequests) < tc.count {
				t.Errorf("%s observed %d HTTP requests, want at least %d backend requests: %v", tc.source, len(gotRequests), tc.count, gotRequests)
			}
		})
	}
}


func TestXinglanNeteaseOfficialPOSTContractAndTrialRejection(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	for _, trial := range []bool{false, true} {
		name := "full_url"
		if trial { name = "trial_url_rejected" }
		t.Run(name, func(t *testing.T) {
			rt := newTestRuntime(t, 15*time.Second)
			defer rt.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var officialRequests int
			client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
				jsonResponse := func(status int, body string) (*http.Response, error) {
					header := make(http.Header)
					header.Set("Content-Type", "application/json")
					return &http.Response{StatusCode:status, Status:http.StatusText(status), Header:header,
						Body:io.NopCloser(strings.NewReader(body)), Request:request}, nil
				}
				if request.URL.Host != "interface3.music.163.com" {
					return jsonResponse(http.StatusServiceUnavailable, "{\"code\":503,\"message\":\"deterministic fallback failure\"}")
				}
				if request.Method != http.MethodPost || request.URL.Path != "/eapi/song/enhance/player/url/v1" {
					return nil, fmt.Errorf("Netease official request = %s %s, want POST /eapi/song/enhance/player/url/v1", request.Method, request.URL.Redacted())
				}
				if !strings.HasPrefix(request.Header.Get("Content-Type"), "application/x-www-form-urlencoded") ||
					!strings.Contains(request.Header.Get("User-Agent"), "NeteaseMusicDesktop") ||
					request.Header.Get("Referer") != "https://music.163.com/" ||
					!strings.Contains(request.Header.Get("Cookie"), "os=pc") {
					return nil, fmt.Errorf("unexpected Netease official headers: %#v", request.Header)
				}
				body, err := io.ReadAll(request.Body)
				if err != nil { return nil, fmt.Errorf("read Netease EAPI body: %w", err) }
				form, err := url.ParseQuery(string(body))
				if err != nil { return nil, fmt.Errorf("parse Netease EAPI form: %w", err) }
				params := form.Get("params")
				if params == "" || len(params)%2 != 0 || strings.Trim(params, "0123456789ABCDEF") != "" {
					return nil, fmt.Errorf("Netease EAPI params is not non-empty uppercase hex: %q", params)
				}
				if len(form) != 1 {
					return nil, fmt.Errorf("Netease EAPI form keys = %v, want only params", form)
				}
				officialRequests++
				if trial {
					return jsonResponse(http.StatusOK, "{\"data\":[{\"url\":\"https://cdn.example.invalid/trial.mp3\",\"freeTrialInfo\":{\"start\":0,\"end\":30}}]}")
				}
				return jsonResponse(http.StatusOK, "{\"data\":[{\"url\":\"https://cdn.example.invalid/netease-320k.mp3\",\"freeTrialInfo\":null}]}")
			})}
			if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
				t.Fatalf("initialize pinned Xinglan source: %v", err)
			}
			result, err := rt.dispatchLXRequest(ctx, "wy", "musicUrl", map[string]any{
				"type":"320k",
				"musicInfo":map[string]any{"songmid":"wy-eapi-contract-001", "name":"fixture Netease song", "singer":"fixture singer", "source":"wy"},
			}, client)
			if trial {
				if err == nil || !strings.Contains(err.Error(), "VIP歌曲仅试听") {
					t.Fatalf("Netease trial response error = %v, want explicit trial rejection", err)
				}
			} else {
				if err != nil { t.Fatalf("dispatch Netease official full URL: %v", err) }
				if got, ok := result.(string); !ok || got != "https://cdn.example.invalid/netease-320k.mp3" {
					t.Fatalf("Netease official URL = %#v, want full playback URL", result)
				}
			}
			if officialRequests != 1 { t.Fatalf("Netease official requests = %d, want 1", officialRequests) }
		})
	}
}

func TestXinglanKugouHaiTangPrimaryPOSTContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var primaryRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{StatusCode:status, Status:http.StatusText(status), Header:header,
				Body:io.NopCloser(strings.NewReader(body)), Request:request}, nil
		}
		if request.URL.Host != "musicserver.haitangw.cc" {
			return jsonResponse(http.StatusServiceUnavailable, "{\"code\":503,\"message\":\"deterministic fallback failure\"}")
		}
		if request.Method != http.MethodPost || request.URL.Path != "/v1/music/resolve-url" {
			return nil, fmt.Errorf("HaiTang KG request = %s %s, want POST /v1/music/resolve-url", request.Method, request.URL.Redacted())
		}
		if request.Header.Get("Content-Type") != "application/json" || request.Header.Get("User-Agent") != "Mozilla/5.0" {
			return nil, fmt.Errorf("unexpected HaiTang KG headers: %#v", request.Header)
		}
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, fmt.Errorf("decode HaiTang KG payload: %w", err)
		}
		if body["source"] != "kg" || body["rid"] != "kg-haitang-contract-001" || body["level"] != "exhigh" {
			return nil, fmt.Errorf("unexpected HaiTang KG payload: %+v", body)
		}
		primaryRequests++
		return jsonResponse(http.StatusOK, "{\"data\":{\"url\":\"https://cdn.example.invalid/kg-haitang-320k.mp3\"}}")
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "kg", "musicUrl", map[string]any{
		"type":"320k",
		"musicInfo":map[string]any{"songmid":"kg-haitang-contract-001", "hash":"kg-haitang-contract-001", "name":"fixture Kugou song", "singer":"fixture singer"},
	}, client)
	if err != nil { t.Fatalf("dispatch HaiTang Kugou primary: %v", err) }
	if got, ok := result.(string); !ok || got != "https://cdn.example.invalid/kg-haitang-320k.mp3" {
		t.Fatalf("HaiTang Kugou URL = %#v, want checked primary response URL", result)
	}
	if primaryRequests != 1 { t.Fatalf("HaiTang Kugou primary requests = %d, want 1", primaryRequests) }
}


func TestXinglanKuwoPrimarySuccessSkipsSequentialFallbacks(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	primaryRequests, backupRequests, tailRequests := 0, 0, 0
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{StatusCode:status, Status:http.StatusText(status), Header:header,
				Body:io.NopCloser(strings.NewReader(body)), Request:request}, nil
		}
		switch request.URL.Host {
		case "yy.zddyr.top":
			q := request.URL.Query()
			for key, want := range map[string]string{
				"source":"kw", "name":"fixture Kuwo primary song", "singer":"fixture singer",
				"songmid":"kw-primary-success-001", "interval":"03:21",
				"albumName":"fixture album", "quality":"320k",
			} {
				if q.Get(key) != want {
					return nil, fmt.Errorf("Xinghai primary Kuwo query %s = %q, want %q (raw %s)", key, q.Get(key), want, request.URL.RawQuery)
				}
			}
			if request.Method != http.MethodGet || request.Header.Get("User-Agent") != "Mozilla/5.0" {
				return nil, fmt.Errorf("unexpected Xinghai primary request: %s headers=%#v", request.Method, request.Header)
			}
			mu.Lock(); primaryRequests++; mu.Unlock()
			return jsonResponse(http.StatusOK, "{\"code\":200,\"url\":\"https://cdn.example.invalid/kw-primary-320k.mp3\"}")
		case "c.wwwweb.top":
			mu.Lock(); backupRequests++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, "{\"code\":503,\"message\":\"deterministic concurrent backup failure\"}")
		default:
			mu.Lock(); tailRequests++; mu.Unlock()
			return jsonResponse(http.StatusServiceUnavailable, "{\"code\":503,\"message\":\"sequential fallback must not run\"}")
		}
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
		"type":"320k",
		"musicInfo":map[string]any{
			"songmid":"kw-primary-success-001", "rid":"kw-primary-success-001",
			"name":"fixture Kuwo primary song", "singer":"fixture singer",
			"interval":"03:21", "albumName":"fixture album",
		},
	}, client)
	if err != nil { t.Fatalf("dispatch Xinghai primary Kuwo success: %v", err) }
	if got, ok := result.(string); !ok || got != "https://cdn.example.invalid/kw-primary-320k.mp3" {
		t.Fatalf("Xinghai primary Kuwo URL = %#v, want primary response URL", result)
	}
	mu.Lock()
	gotPrimary, gotBackup, gotTail := primaryRequests, backupRequests, tailRequests
	mu.Unlock()
	if gotPrimary != 1 || gotBackup != 1 || gotTail != 0 {
		t.Fatalf("Kuwo primary/head/tail requests = primary:%d backup:%d tail:%d, want 1,1,0", gotPrimary, gotBackup, gotTail)
	}
}

func TestXinglanKuwoBackendPoolReportsAllFailures(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	var requests []string
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		mu.Lock()
		requests = append(requests, request.Method+" "+request.URL.Host+request.URL.Path)
		mu.Unlock()
		return &http.Response{
			StatusCode:http.StatusServiceUnavailable, Status:http.StatusText(http.StatusServiceUnavailable),
			Header:make(http.Header), Body:io.NopCloser(strings.NewReader("{\"code\":503,\"message\":\"deterministic Kuwo provider failure\"}")),
			Request:request,
		}, nil
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	_, err = rt.dispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{
		"type":"320k",
		"musicInfo":map[string]any{
			"songmid":"kw-all-backends-fail-001", "rid":"kw-all-backends-fail-001",
			"source":"kw", "name":"fixture Kuwo failure song", "singer":"fixture singer",
			"interval":"03:21", "albumName":"fixture album",
		},
	}, client)
	if err == nil { t.Fatal("expected all Kuwo backends to fail") }
	message := err.Error()
	if !strings.Contains(message, "所有后端均失败（共 10 个）") {
		t.Fatalf("Kuwo aggregate error does not report ten ordinary providers: %s", message)
	}
	for _, name := range []string{
		"星海主后端:", "HelloWorld KW 主API:", "HelloWorld KW 备用API:",
		"酷我官方:", "酷我手机版:", "念心直链:", "HYWmusic KW:",
		"yunmge酷我:", "星海酷我:", "FFAPI:",
	} {
		if !strings.Contains(message, name) { t.Errorf("Kuwo aggregate error missing %q: %s", name, message) }
	}
	mu.Lock()
	gotRequests := append([]string(nil), requests...)
	mu.Unlock()
	if len(gotRequests) < 9 {
		t.Fatalf("observed %d Kuwo HTTP requests, want at least 9 actual provider requests: %v", len(gotRequests), gotRequests)
	}
}

func TestXinglanQsBackendPoolReportsAllFailures(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var mu sync.Mutex
	requests := map[string]int{}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || request.URL.Path != "/lx/api/" {
			return nil, fmt.Errorf("unexpected Xinglan QS request: %s %s", request.Method, request.URL.Redacted())
		}
		if request.Header.Get("X-Client") != "XingHaiMusicSource" || request.Header.Get("User-Agent") != "Mozilla/5.0" {
			return nil, fmt.Errorf("unexpected Xinglan QS headers: %#v", request.Header)
		}
		q := request.URL.Query()
		for key, want := range map[string]string{
			"source":"qs", "name":"fixture QS failure song", "singer":"fixture singer",
			"songmid":"qs-all-fail-001", "interval":"02:59", "albumName":"fixture QS album", "quality":"320k",
		} {
			if q.Get(key) != want {
				return nil, fmt.Errorf("QS query %s = %q, want %q (raw %s)", key, q.Get(key), want, request.URL.RawQuery)
			}
		}
		mu.Lock(); requests[request.URL.Host]++; mu.Unlock()
		return &http.Response{
			StatusCode:http.StatusServiceUnavailable, Status:http.StatusText(http.StatusServiceUnavailable),
			Header:make(http.Header), Body:io.NopCloser(strings.NewReader("{\"code\":503,\"msg\":\"deterministic QS provider failure\"}")),
			Request:request,
		}, nil
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	_, err = rt.dispatchLXRequest(ctx, "qs", "musicUrl", map[string]any{
		"type":"320k",
		"musicInfo":map[string]any{
			"songmid":"qs-all-fail-001", "name":"fixture QS failure song",
			"singer":"fixture singer", "interval":"02:59", "albumName":"fixture QS album",
		},
	}, client)
	if err == nil { t.Fatal("expected both QS backends to fail") }
	for _, name := range []string{"星海汽水主线路:", "星海汽水备用线路:"} {
		if !strings.Contains(err.Error(), name) { t.Errorf("QS aggregate error missing %q: %s", name, err) }
	}
	mu.Lock()
	primary, backup := requests["yy.zddyr.top"], requests["zrcdy.dpdns.org"]
	mu.Unlock()
	if primary != 1 || backup != 1 {
		t.Fatalf("QS provider requests: primary=%d backup=%d, want one each", primary, backup)
	}
}


func TestXinglanNeteaseCanxiangSequentialFallbackGETContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var fallbackRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{StatusCode:status, Status:http.StatusText(status), Header:header,
				Body:io.NopCloser(strings.NewReader(body)), Request:request}, nil
		}
		if request.URL.Host != "api.canxiang.cn" {
			return jsonResponse(http.StatusServiceUnavailable, "{\"code\":503,\"message\":\"deterministic preceding provider failure\"}")
		}
		if request.Method != http.MethodGet || request.URL.Path != "/api/wyymusic" {
			return nil, fmt.Errorf("Canxiang WY request = %s %s, want GET /api/wyymusic", request.Method, request.URL.Redacted())
		}
		q := request.URL.Query()
		if q.Get("id") != "wy-canxiang-contract-001" || q.Get("type") != "320k" || q.Get("token") != "canxiang_token_2026" {
			return nil, fmt.Errorf("unexpected Canxiang WY query: %s", request.URL.RawQuery)
		}
		fallbackRequests++
		return jsonResponse(http.StatusOK, "{\"code\":200,\"data\":{\"url\":\"https://cdn.example.invalid/wy-canxiang-320k.mp3\"}}")
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "wy", "musicUrl", map[string]any{
		"type":"320k",
		"musicInfo":map[string]any{"songmid":"wy-canxiang-contract-001", "id":"wy-canxiang-contract-001", "name":"fixture Netease song", "singer":"fixture singer", "source":"wy"},
	}, client)
	if err != nil { t.Fatalf("dispatch Canxiang Netease fallback: %v", err) }
	if got, ok := result.(string); !ok || got != "https://cdn.example.invalid/wy-canxiang-320k.mp3" {
		t.Fatalf("Canxiang Netease URL = %#v, want fallback URL", result)
	}
	if fallbackRequests != 1 { t.Fatalf("Canxiang Netease requests = %d, want 1", fallbackRequests) }
}

func TestXinglanKugouNianxinSequentialFallbackGETContract(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "xinglan.js"))
	if err != nil { t.Fatal(err) }
	rt := newTestRuntime(t, 15*time.Second)
	defer rt.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var fallbackRequests int
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		jsonResponse := func(status int, body string) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Content-Type", "application/json")
			return &http.Response{StatusCode:status, Status:http.StatusText(status), Header:header,
				Body:io.NopCloser(strings.NewReader(body)), Request:request}, nil
		}
		if request.URL.Host != "mcp.nianxinxz.com" {
			return jsonResponse(http.StatusServiceUnavailable, "{\"code\":503,\"message\":\"deterministic preceding provider failure\"}")
		}
		if request.Method != http.MethodGet || request.URL.Path != "/kgqq/kg.php" {
			return nil, fmt.Errorf("Nianxin KG request = %s %s, want GET /kgqq/kg.php", request.Method, request.URL.Redacted())
		}
		q := request.URL.Query()
		if q.Get("id") != "kg-nianxin-contract-001" || q.Get("level") != "320kmp3" {
			return nil, fmt.Errorf("unexpected Nianxin KG query: %s", request.URL.RawQuery)
		}
		fallbackRequests++
		return jsonResponse(http.StatusOK, "{\"code\":200,\"url\":\"https://cdn.example.invalid/kg-nianxin-320k.mp3\"}")
	})}
	if err := rt.InitializeLXSource(ctx, ScriptInfo{Name:"星澜聚合音源", Version:"v4.0.2", RawScript:string(source)}, string(source), client); err != nil {
		t.Fatalf("initialize pinned Xinglan source: %v", err)
	}
	result, err := rt.dispatchLXRequest(ctx, "kg", "musicUrl", map[string]any{
		"type":"320k",
		"musicInfo":map[string]any{"songmid":"kg-nianxin-contract-001", "hash":"kg-nianxin-contract-001", "name":"fixture Kugou song", "singer":"fixture singer", "source":"kg"},
	}, client)
	if err != nil { t.Fatalf("dispatch Nianxin Kugou fallback: %v", err) }
	if got, ok := result.(string); !ok || got != "https://cdn.example.invalid/kg-nianxin-320k.mp3" {
		t.Fatalf("Nianxin Kugou URL = %#v, want fallback URL", result)
	}
	if fallbackRequests != 1 { t.Fatalf("Nianxin Kugou requests = %d, want 1", fallbackRequests) }
}

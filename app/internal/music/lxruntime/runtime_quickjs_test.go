//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestRuntime(t *testing.T, timeout time.Duration) *Runtime {
	t.Helper()
	rt, err := New(Options{MemoryLimitBytes: 48 << 20, StackLimitBytes: 1 << 20, EvalTimeout: timeout})
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
    if (base && !/^[a-z]+:/i.test(this.href)) this.href = String(base).replace(/\\/$/, '') + '/' + this.href.replace(/^\\//, '');
  }
};
globalThis.TextEncoder = class TextEncoder {
  encode(value) { return new Uint8Array(Array.from(unescape(encodeURIComponent(String(value)))).map(c => c.charCodeAt(0))); }
};
globalThis.TextDecoder = class TextDecoder {
  decode(value) { return decodeURIComponent(escape(Array.from(value || []).map(v => String.fromCharCode(v)).join(''))); }
};
globalThis.crypto = { getRandomValues(value) { return value; } };
globalThis.btoa = value => value;
globalThis.atob = value => value;
const __lxHandlers = Object.create(null);
globalThis.__lxSent = [];
globalThis.lx = {
  EVENT_NAMES: { request: 'request', inited: 'inited', updateAlert: 'updateAlert' },
  version: '2.0.0',
  env: 'desktop',
  currentScriptInfo: { rawScript: '' },
  request(url, options, callback) {
    if (typeof options === 'function') callback = options;
    if (typeof callback === 'function') callback(null, {
      statusCode: 200,
      headers: { 'content-type': 'text/plain' },
      body: 'https://cdn.example.invalid/fixture.mp3'
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
      from(value) { return value instanceof Uint8Array ? value : new Uint8Array(value); },
      bufToString(value) {
        if (typeof value === 'string') return value;
        return Array.from(value || []).map(v => String.fromCharCode(v)).join('');
      }
    },
    crypto: {
      md5() { return '00000000000000000000000000000000'; },
      aesEncrypt() { return new Uint8Array(16); },
      rsaEncrypt() { return new Uint8Array(128); },
      randomBytes(size) { return new Uint8Array(size); }
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

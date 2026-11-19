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

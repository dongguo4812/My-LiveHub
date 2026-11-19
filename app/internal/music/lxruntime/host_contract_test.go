//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLXHostContractInitializationAndFiltering(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	err := rt.InstallLXHost(context.Background(), ScriptInfo{
		Name: "Contract fixture", Version: "1.0.0", RawScript: "/* fixture */",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Eval(context.Background(), `
if (lx.version !== '2.0.0' || lx.env !== 'desktop') throw new Error('wrong host version or env');
if (lx.currentScriptInfo.name !== 'Contract fixture') throw new Error('script metadata missing');
lx.on('request', () => Promise.resolve('unused'));
lx.send('inited', { sources: {
  kw: { type: 'music', actions: ['musicUrl', 'lyric'], qualitys: ['128k', 'flac', 'flac24bit'] },
  tx: { type: 'music', actions: ['musicUrl'], qualitys: ['320k'] },
  local: { type: 'music', actions: ['musicUrl', 'lyric', 'pic'], qualitys: ['128k'] },
  unsupported: { type: 'music', actions: ['musicUrl'], qualitys: ['320k'] }
}});
'installed';
`, "lx-contract-init.js"); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), `
JSON.stringify(__lxHostState.initInfo)
`, "lx-contract-result.js")
	if err != nil {
		t.Fatal(err)
	}
	want := `{"sources":{"kw":{"type":"music","actions":["musicUrl"],"qualitys":["128k","flac","flac24bit"]},"tx":{"type":"music","actions":["musicUrl"],"qualitys":["320k"]},"local":{"type":"music","actions":["musicUrl","lyric","pic"],"qualitys":[]}}}`
	if value != want {
		t.Fatalf("filtered source info = %s, want %s", value, want)
	}
}

func TestLXHostContractRejectsInvalidAndRepeatedEvents(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Eval(context.Background(), `
globalThis.__badEvent = '';
lx.send('not-supported', {}).catch(error => __badEvent = error.message);
'queued';
`, "lx-invalid-event.js")
	if err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), `__badEvent`, "lx-invalid-event-result.js")
	if err != nil || !strings.Contains(value, "not supported") {
		t.Fatalf("unsupported event rejection = %q, err = %v", value, err)
	}
	_, err = rt.Eval(context.Background(), `
lx.send('inited', { sources: {} }).then(() =>
  lx.send('inited', { sources: {} }).then(
    () => { __badEvent = 'unexpected success'; },
    error => { __badEvent = error.message; }
  )
);
'queued';
`, "lx-repeat-init.js")
	if err != nil {
		t.Fatal(err)
	}
	value, err = rt.Eval(context.Background(), `__badEvent`, "lx-repeat-init-result.js")
	if err != nil || value != "Script is inited" {
		t.Fatalf("repeated init rejection = %q, err = %v", value, err)
	}
}

func TestLXHostContractFailsClosedForUnimplementedCapabilities(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), `
try { lx.utils.crypto.rsaEncrypt('abc', 'invalid-key'); } catch (error) { error.message; }
`, "lx-capability-gate.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != "LX host capability is not implemented: crypto.rsaEncrypt" {
		t.Fatalf("unimplemented capability response = %q", value)
	}
}


func TestLXHostRequestAbortRemovesQueuedRequest(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), `
globalThis.__abortError = '';
const abort = lx.request('https://example.com/test', {}, error => {
  __abortError = error ? error.message : 'missing error';
});
abort();
JSON.stringify({ queued: __lxTakeRequests().length, error: __abortError });
`, "lx-request-abort.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != `{"queued":0,"error":"Cancel request"}` {
		t.Fatalf("abort result = %s", value)
	}
}


func TestLXHostCryptoMD5UsesNativeImplementation(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `lx.utils.crypto.md5('abc')`, "lx-native-md5.js")
	if err != nil { t.Fatal(err) }
	if value != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatalf("native md5 = %q", value)
	}
}


func TestLXHostCryptoRandomBytesUsesNativeImplementation(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `(() => {
		const bytes = lx.utils.crypto.randomBytes(32);
		return JSON.stringify({ isUint8Array: bytes instanceof Uint8Array, length: bytes.length,
			hasNonZero: Array.from(bytes).some(value => value !== 0) });
	})()`, "lx-native-random.js")
	if err != nil { t.Fatal(err) }
	if value != `{"isUint8Array":true,"length":32,"hasNonZero":true}` {
		t.Fatalf("native randomBytes = %q", value)
	}
	value, err = rt.Eval(context.Background(), `try { lx.utils.crypto.randomBytes(1048577); } catch (error) { error.name; }`, "lx-random-limit.js")
	if err != nil { t.Fatal(err) }
	if value != "RangeError" { t.Fatalf("randomBytes limit error = %q", value) }
}


func TestLXHostCryptoAESEncryptUsesNativeImplementation(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `(() => {
		const encrypted = lx.utils.crypto.aesEncrypt('hello', 'aes-128-cbc',
			'0123456789abcdef', 'abcdef0123456789');
		return JSON.stringify({ isUint8Array: encrypted instanceof Uint8Array, length: encrypted.length,
			hex: Array.from(encrypted, value => value.toString(16).padStart(2, '0')).join('') });
	})()`, "lx-native-aes.js")
	if err != nil { t.Fatal(err) }
	if value != `{"isUint8Array":true,"length":16,"hex":"ffcb80bec2437fc2e30b0ef0a8ec5b64"}` {
		t.Fatalf("native AES result = %q", value)
	}
}


func TestLXHostBufferFromAndBufToStringUseNativeImplementation(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `(() => {
		const original = '{"ok":true}';
		const bytes = lx.utils.buffer.from(original, 'utf-8');
		const base64 = lx.utils.buffer.bufToString(bytes, 'base64');
		const hex = lx.utils.buffer.bufToString(bytes, 'hex');
		const decoded = Buffer.from(base64, 'base64');
		const roundTrip = lx.utils.buffer.bufToString(decoded, 'utf8');
		return JSON.stringify({
			isUint8Array: bytes instanceof Uint8Array,
			base64,
			hex,
			roundTripOK: roundTrip === original,
			globalBuffer: typeof Buffer.from === 'function'
		});
	})()`, "lx-native-buffer.js")
	if err != nil { t.Fatal(err) }
	want := `{"isUint8Array":true,"base64":"eyJvayI6dHJ1ZX0=","hex":"7b226f6b223a747275657d","roundTripOK":true,"globalBuffer":true}`
	if value != want { t.Fatalf("native buffer result = %q, want %q", value, want) }
}



func TestLXHostBufferGlobalNodeCompatibilitySubset(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `(() => {
		const original = '{"ok":true}';
		const a = Buffer.from(original, 'utf8');
		const b = Buffer.from(a);
		const joined = Buffer.concat([Buffer.from('A'), b]);
		const allocated = Buffer.alloc(2, 0x2a);
		return JSON.stringify({
			isBuffer: Buffer.isBuffer(a),
			isUint8Array: a instanceof Uint8Array,
			text: b.toString('utf8'),
			hex: a.toString('hex'),
			joined: joined.toString('utf8'),
			allocated: allocated.toString('hex'),
			byteLength: Buffer.byteLength(original, 'utf8')
		});
	})()`, "lx-buffer-global.js")
	if err != nil { t.Fatal(err) }
	want := `{"isBuffer":true,"isUint8Array":true,"text":"{\"ok\":true}","hex":"7b226f6b223a747275657d","joined":"A{\"ok\":true}","allocated":"2a2a","byteLength":11}`
	if value != want { t.Fatalf("global Buffer compatibility = %q, want %q", value, want) }
}

func TestLXHostCryptoAESEncryptSupportsXinglanECBBufferInput(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), `(() => {
		const plaintext = lx.utils.buffer.from('hello', 'utf8');
		const encrypted = lx.utils.crypto.aesEncrypt(
			plaintext, 'aes-128-ecb', '0123456789abcdef', ''
		);
		return Buffer.from(encrypted).toString('hex');
	})()`, "lx-xinglan-aes-ecb.js")
	if err != nil {
		t.Fatal(err)
	}
	const want = "674c7ef38e78cabd9cec9c125823a639"
	if value != want {
		t.Fatalf("Xinglan AES-ECB result = %q, want %q", value, want)
	}
}

func TestLXHostBufferUTF16LECompatibility(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `(() => {
		const source = '音乐🎵';
		const viaLX = lx.utils.buffer.from(source, 'utf-16le');
		const viaBuffer = Buffer.from(source, 'ucs2');
		return JSON.stringify({
			lxHex: Buffer.from(viaLX).toString('hex'),
			lxRoundTrip: lx.utils.buffer.bufToString(viaLX, 'utf16le'),
			bufferHex: viaBuffer.toString('hex'),
			bufferRoundTrip: viaBuffer.toString('ucs-2'),
			oddByteIgnored: Buffer.from([0x41, 0x00, 0xff]).toString('utf16le')
		});
	})()`, "lx-buffer-utf16le.js")
	if err != nil { t.Fatal(err) }
	want := `{"lxHex":"f397504e3cd8b5df","lxRoundTrip":"音乐🎵","bufferHex":"f397504e3cd8b5df","bufferRoundTrip":"音乐🎵","oddByteIgnored":"A"}`
	if value != want { t.Fatalf("UTF-16LE compatibility = %q, want %q", value, want) }
}


func TestLXDispatchPumpsSetTimeoutCallbacks(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	if _, err := rt.Eval(context.Background(), `
lx.on('request', async () => new Promise(resolve => {
  setTimeout(() => resolve('timer-fired'), 15);
}));
'registered';
`, "lx-timer-handler.js"); err != nil { t.Fatal(err) }
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	value, err := rt.DispatchLXRequest(ctx, "kw", "musicUrl", map[string]any{"id":"timer-test"})
	if err != nil { t.Fatal(err) }
	if value != "timer-fired" { t.Fatalf("timer dispatch result = %#v", value) }
}

func TestLXHostSetTimeoutAndClearTimeout(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `
globalThis.__timerProbe = 0;
const cancelled = setTimeout(() => { __timerProbe += 100; }, 0);
clearTimeout(cancelled);
setTimeout((increment) => { __timerProbe += increment; }, 0, 7);
__lxRunDueTimers();
__timerProbe;
`, "lx-timer-contract.js")
	if err != nil { t.Fatal(err) }
	if value != "7" { t.Fatalf("timer contract result = %q, want 7", value) }
}


func TestLXHostZlibAPIsReturnPromisesAndRoundTripBinaryData(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	_, err := rt.Eval(context.Background(), `
globalThis.__zlibRoundTrip = null;
const source = Buffer.from('洛雪音源🎵\\n'.repeat(64), 'utf8');
const deflatePromise = lx.utils.zlib.deflate(source);
if (!(deflatePromise instanceof Promise)) throw new Error('deflate must return a Promise');
deflatePromise.then(compressed => {
  if (!(compressed instanceof Uint8Array)) throw new Error('deflate result must be Uint8Array');
  return lx.utils.zlib.inflate(compressed);
}).then(restored => {
  __zlibRoundTrip = JSON.stringify({
    isPromise: deflatePromise instanceof Promise,
    isUint8Array: restored instanceof Uint8Array,
    text: Buffer.from(restored).toString('utf8')
  });
});
'queued';
`, "lx-zlib-roundtrip.js")
	if err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `__zlibRoundTrip`, "lx-zlib-roundtrip-result.js")
	if err != nil { t.Fatal(err) }
	expected, _ := json.Marshal(map[string]any{
		"isPromise": true, "isUint8Array": true, "text": strings.Repeat("洛雪音源🎵\\n", 64),
	})
	if value != string(expected) { t.Fatalf("zlib round-trip = %s, want %s", value, expected) }
}

func TestLXHostZlibInflateRejectsInvalidStreamAsPromise(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	_, err := rt.Eval(context.Background(), `
globalThis.__zlibError = '';
const promise = lx.utils.zlib.inflate(Buffer.from('not a zlib stream'));
if (!(promise instanceof Promise)) throw new Error('inflate must return a Promise');
promise.catch(error => { __zlibError = error.message; });
'queued';
`, "lx-zlib-invalid.js")
	if err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `__zlibError`, "lx-zlib-invalid-result.js")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(value, "zlib inflate") { t.Fatalf("invalid stream rejection = %q", value) }
}

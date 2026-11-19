//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
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
	want := `{"isUint8Array":true,"base64":"eyJvayI6dHJ1ZX0=","hex":"7b226f6b223a747275657d","roundTrip":"{\\\"ok\\\":true}","globalBuffer":true}`
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
		const bytes = lx.utils.buffer.from('{"ok":true}', 'utf-8');
		const base64 = lx.utils.buffer.bufToString(bytes, 'base64');
		const hex = lx.utils.buffer.bufToString(bytes, 'hex');
		const decoded = Buffer.from(base64, 'base64');
		return JSON.stringify({
			isUint8Array: bytes instanceof Uint8Array,
			base64,
			hex,
			roundTrip: lx.utils.buffer.bufToString(decoded, 'utf8'),
			globalBuffer: typeof Buffer.from === 'function'
		});
	})()`, "lx-native-buffer.js")
	if err != nil { t.Fatal(err) }
	want := `{"isUint8Array":true,"base64":"eyJvayI6dHJ1ZX0=","hex":"7b226f6b223a747275657d","roundTrip":"{\\"ok\\":true}","globalBuffer":true}`
	if value != want { t.Fatalf("native buffer result = %q, want %q", value, want) }
}

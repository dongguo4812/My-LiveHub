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

func TestLXHostCryptoRSAEncryptRejectsInvalidKey(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), `
try { lx.utils.crypto.rsaEncrypt('abc', 'invalid-key'); } catch (error) { error.message; }
`, "lx-rsa-invalid-key.js")
	if err != nil { t.Fatal(err) }
	if !strings.Contains(value, "RSA encryption failed") {
		t.Fatalf("invalid RSA key error = %q", value)
	}
}


func TestLXHostBufferFromAndToStringNodeContract(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `
const hex = lx.utils.buffer.bufToString(lx.utils.buffer.from('Hello', 'utf8'), 'hex');
const decoded = lx.utils.buffer.bufToString(lx.utils.buffer.from('SGVsbG8=', 'base64'), 'utf8');
const array = Buffer.from([0, 255, 256, -1]);
const arrayHex = array.toString('hex');
const typed = Buffer.from(new Uint8Array([65, 66, 67]));
const typedText = typed.toString('utf8');
JSON.stringify({hex, decoded, arrayHex, typedText, isBuffer: Buffer.isBuffer(array)})
`, "lx-buffer-contract.js")
	if err != nil { t.Fatal(err) }
	want := `{"hex":"48656c6c6f","decoded":"Hello","arrayHex":"00ff00ff","typedText":"ABC","isBuffer":true}`
	if value != want { t.Fatalf("Buffer host contract = %s, want %s", value, want) }
}

func TestLXHostBufferSupportsHexAndUTF16LE(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `
JSON.stringify({
  hex: lx.utils.buffer.bufToString(lx.utils.buffer.from('4869', 'hex'), 'utf8'),
  utf16: lx.utils.buffer.bufToString(lx.utils.buffer.from('A音乐', 'utf16le'), 'utf16le')
})
`, "lx-buffer-encoding-contract.js")
	if err != nil { t.Fatal(err) }
	want := `{"hex":"Hi","utf16":"A音乐"}`
	if value != want { t.Fatalf("Buffer encodings = %s, want %s", value, want) }
}

func TestLXHostCryptoNativeCallbacksAreLinkedAndCallable(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), `
const digest = lx.utils.crypto.md5('abc');
const random = lx.utils.crypto.randomBytes(24);
const encrypted = lx.utils.crypto.aesEncrypt(
  Buffer.from('abc'), 'aes-128-cbc',
  Buffer.from('2b7e151628aed2a6abf7158809cf4f3c', 'hex'),
  Buffer.from('000102030405060708090a0b0c0d0e0f', 'hex')
);
JSON.stringify({digest, randomLength: random.length, encryptedLength: encrypted.length})
`, "lx-crypto-native-contract.js")
	if err != nil { t.Fatal(err) }
	want := `{"digest":"900150983cd24fb0d6963f7d28e17f72","randomLength":24,"encryptedLength":16}`
	if value != want { t.Fatalf("native crypto callbacks = %s, want %s", value, want) }
}



// The Eval call drains QuickJS promise jobs; the test reads the settled result in a second Eval.
func TestLXHostZlibNativeCallbacksAreLinkedAndPromiseBased(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "zlib-host-contract"}); err != nil {
		t.Fatal(err)
	}
	_, err := rt.Eval(context.Background(), `
globalThis.__zlibHostContractResult = 'pending';
(async () => {
  const original = Buffer.from('洛雪音源 QuickJS zlib host contract');
  const compressed = await lx.utils.zlib.deflate(original);
  if (!(compressed instanceof Uint8Array)) throw new Error('deflate did not return Uint8Array');
  if (compressed.length === 0) throw new Error('deflate returned empty output');
  const restored = await lx.utils.zlib.inflate(compressed);
  if (!(restored instanceof Uint8Array)) throw new Error('inflate did not return Uint8Array');
  const text = Buffer.from(restored).toString('utf8');
  if (text !== '洛雪音源 QuickJS zlib host contract') throw new Error('inflate round-trip mismatch: ' + text);
  let rejected = false;
  try { await lx.utils.zlib.inflate(Buffer.from('not a zlib stream')); }
  catch (error) { rejected = /zlib inflate/.test(error.message); }
  if (!rejected) throw new Error('invalid zlib input did not reject with host error');
  return 'zlib-native-host-ok';
})().then(
  value => { globalThis.__zlibHostContractResult = value; },
  error => { globalThis.__zlibHostContractResult = 'ERROR: ' + (error && error.message || error); }
);
'queued';
`, "lx-zlib-host-contract.js")
	if err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), "globalThis.__zlibHostContractResult", "lx-zlib-host-contract-result.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != "zlib-native-host-ok" {
		t.Fatalf("zlib host contract = %q", value)
	}
}

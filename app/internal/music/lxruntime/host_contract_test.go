//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
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
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{}); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), `
const digest = lx.utils.crypto.md5('abc');
const randomA = lx.utils.crypto.randomBytes(24);
const randomB = lx.utils.crypto.randomBytes(24);
const key = Buffer.from('2b7e151628aed2a6abf7158809cf4f3c', 'hex');
const iv = Buffer.from('000102030405060708090a0b0c0d0e0f', 'hex');
const encrypted = lx.utils.crypto.aesEncrypt(Buffer.from('abc'), 'aes-128-cbc', key, iv);
if (!(randomA instanceof Uint8Array) || !(randomB instanceof Uint8Array)) throw new Error('randomBytes did not return Uint8Array');
if (randomA.length !== 24 || randomB.length !== 24) throw new Error('randomBytes returned wrong length');
if (Buffer.from(randomA).toString('hex') === Buffer.from(randomB).toString('hex')) throw new Error('randomBytes returned repeated output');
if (!(encrypted instanceof Uint8Array)) throw new Error('aesEncrypt did not return Uint8Array');
JSON.stringify({
  digest,
  randomHex: Buffer.from(randomA).toString('hex'),
  encryptedHex: Buffer.from(encrypted).toString('hex')
})
`, "lx-crypto-native-contract.js")
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Digest       string `json:"digest"`
		RandomHex    string `json:"randomHex"`
		EncryptedHex string `json:"encryptedHex"`
	}
	if err := json.Unmarshal([]byte(value), &result); err != nil {
		t.Fatalf("decode native crypto result %q: %v", value, err)
	}
	if result.Digest != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatalf("MD5('abc') = %q", result.Digest)
	}
	randomBytes, err := hex.DecodeString(result.RandomHex)
	if err != nil || len(randomBytes) != 24 {
		t.Fatalf("randomBytes result length/encoding invalid: len=%d err=%v", len(randomBytes), err)
	}
	encrypted, err := hex.DecodeString(result.EncryptedHex)
	if err != nil {
		t.Fatalf("decode AES output: %v", err)
	}
	block, err := aes.NewCipher([]byte{0x2b,0x7e,0x15,0x16,0x28,0xae,0xd2,0xa6,0xab,0xf7,0x15,0x88,0x09,0xcf,0x4f,0x3c})
	if err != nil { t.Fatal(err) }
	if len(encrypted) == 0 || len(encrypted)%block.BlockSize() != 0 {
		t.Fatalf("AES-CBC output length = %d", len(encrypted))
	}
	plain := make([]byte, len(encrypted))
	cipher.NewCBCDecrypter(block, []byte{0,1,2,3,4,5,6,7,8,9,10,11,12,13,14,15}).CryptBlocks(plain, encrypted)
	padding := int(plain[len(plain)-1])
	if padding == 0 || padding > block.BlockSize() || padding > len(plain) {
		t.Fatalf("invalid PKCS#7 padding byte %d", padding)
	}
	for _, b := range plain[len(plain)-padding:] {
		if int(b) != padding { t.Fatalf("invalid PKCS#7 padding: %x", plain) }
	}
	if string(plain[:len(plain)-padding]) != "abc" {
		t.Fatalf("AES-CBC decrypted plaintext = %q", plain[:len(plain)-padding])
	}
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

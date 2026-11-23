//go:build lxquickjs && cgo

package lxruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"
)

// These are the Buffer entry points used by the pinned real LX fixtures:
// Xinglan checks Buffer.isBuffer and converts response bytes with
// Buffer.from(...).toString(...); HelloWorld uses lx.utils.buffer.from and
// bufToString to build base64 request tokens.
func TestLXHostBufferMatchesPinnedSourceUsage(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "pinned-buffer-contract"}); err != nil {
		t.Fatal(err)
	}

	value, err := rt.Eval(context.Background(), `
const payload = JSON.stringify({ source: 'tx', title: '音乐' });
const payloadBytes = lx.utils.buffer.from(payload, 'utf-8');
const payloadBase64 = lx.utils.buffer.bufToString(payloadBytes, 'base64');
const lxBufferFromIsBuffer = Buffer.isBuffer(payloadBytes);

const responseBytes = Buffer.from('{"ok":true,"title":"音乐"}', 'utf-8');
const responseText = Buffer.from(responseBytes).toString('utf-8');
const slicedText = Buffer.from([9, 65, 66, 67, 8]).toString('utf8', 1, 4);
const arrayBufferText = Buffer.from(new Uint8Array([65, 66]).buffer).toString('utf8');
const typedArrayValuesHex = Buffer.from(new Uint16Array([0x1234, 0x00ff])).toString('hex');
const sharedBacking = new ArrayBuffer(4);
const sharedBuffer = Buffer.from(sharedBacking, 1, 2);
new Uint8Array(sharedBacking)[1] = 0x41;
const sharedArrayBufferHex = sharedBuffer.toString('hex');
sharedBuffer[1] = 0x42;
const sharedArrayBufferMutation = new Uint8Array(sharedBacking)[2];
const allocatedHex = Buffer.alloc(4, 0x2a).toString('hex');
const allocatedStringPatternHex = Buffer.alloc(5, 'ab').toString('hex');
const allocatedBufferPatternHex = Buffer.alloc(5, Buffer.from([1, 2])).toString('hex');
const allocatedEncodedPatternHex = Buffer.alloc(5, 'é', 'utf8').toString('hex');
const allocatedEmptyPatternHex = Buffer.alloc(4, '').toString('hex');
const concatenated = Buffer.concat([Buffer.from('LX'), Buffer.from('-Host')]).toString();
JSON.stringify({
  payloadBase64,
  lxBufferFromIsBuffer,
  responseText,
  responseIsBuffer: Buffer.isBuffer(responseBytes),
  typedArrayIsBuffer: Buffer.isBuffer(new Uint8Array([1, 2])),
  slicedText,
  arrayBufferText,
  typedArrayValuesHex,
  sharedArrayBufferHex,
  sharedArrayBufferMutation,
  allocatedHex,
  allocatedStringPatternHex,
  allocatedBufferPatternHex,
  allocatedEncodedPatternHex,
  allocatedEmptyPatternHex,
  concatenated,
  utf8ByteLength: Buffer.byteLength('音乐', 'utf8')
})
`, "lx-pinned-buffer-contract.js")
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		PayloadBase64    string `json:"payloadBase64"`
		LXBufferFromIsBuffer bool `json:"lxBufferFromIsBuffer"`
		ResponseText     string `json:"responseText"`
		ResponseIsBuffer bool   `json:"responseIsBuffer"`
		TypedArrayIsBuffer bool `json:"typedArrayIsBuffer"`
		SlicedText       string `json:"slicedText"`
		ArrayBufferText  string `json:"arrayBufferText"`
		TypedArrayValuesHex string `json:"typedArrayValuesHex"`
		SharedArrayBufferHex string `json:"sharedArrayBufferHex"`
		SharedArrayBufferMutation int `json:"sharedArrayBufferMutation"`
		AllocatedHex     string `json:"allocatedHex"`
		AllocatedStringPatternHex string `json:"allocatedStringPatternHex"`
		AllocatedBufferPatternHex string `json:"allocatedBufferPatternHex"`
		AllocatedEncodedPatternHex string `json:"allocatedEncodedPatternHex"`
		AllocatedEmptyPatternHex string `json:"allocatedEmptyPatternHex"`
		Concatenated     string `json:"concatenated"`
		UTF8ByteLength   int    `json:"utf8ByteLength"`
	}
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("decode Buffer contract result %q: %v", value, err)
	}
	payload, _ := json.Marshal(map[string]string{"source": "tx", "title": "音乐"})
	if !got.LXBufferFromIsBuffer { t.Fatal("lx.utils.buffer.from must return a Node-compatible Buffer") }
	if got.PayloadBase64 != base64.StdEncoding.EncodeToString(payload) {
		t.Fatalf("lx.utils.buffer base64 = %q, want encoding of %s", got.PayloadBase64, payload)
	}
	if got.ResponseText != `{"ok":true,"title":"音乐"}` {
		t.Fatalf("Buffer.from(response).toString() = %q", got.ResponseText)
	}
	if !got.ResponseIsBuffer || got.TypedArrayIsBuffer {
		t.Fatalf("Buffer.isBuffer results: Buffer=%v Uint8Array=%v", got.ResponseIsBuffer, got.TypedArrayIsBuffer)
	}
	if got.SlicedText != "ABC" || got.ArrayBufferText != "AB" {
		t.Fatalf("Buffer conversion results: slice=%q ArrayBuffer=%q", got.SlicedText, got.ArrayBufferText)
	}
	if got.TypedArrayValuesHex != "34ff" {
		t.Fatalf("Buffer.from(Uint16Array) must copy element values, got %q want 34ff", got.TypedArrayValuesHex)
	}
	if got.SharedArrayBufferHex != "4100" || got.SharedArrayBufferMutation != 0x42 {
		t.Fatalf("Buffer.from(ArrayBuffer, offset, length) must share backing storage: hex=%q mutation=%d", got.SharedArrayBufferHex, got.SharedArrayBufferMutation)
	}
	if got.AllocatedHex != "2a2a2a2a" || got.Concatenated != "LX-Host" {
		t.Fatalf("Buffer alloc/concat results: alloc=%q concat=%q", got.AllocatedHex, got.Concatenated)
	}
	if got.AllocatedStringPatternHex != "6162616261" || got.AllocatedBufferPatternHex != "0102010201" {
		t.Fatalf("Buffer.alloc pattern fill: string=%q buffer=%q", got.AllocatedStringPatternHex, got.AllocatedBufferPatternHex)
	}
	if got.AllocatedEncodedPatternHex != "c3a9c3a9c3" || got.AllocatedEmptyPatternHex != "00000000" {
		t.Fatalf("Buffer.alloc encoding/empty pattern: encoded=%q empty=%q", got.AllocatedEncodedPatternHex, got.AllocatedEmptyPatternHex)
	}
	if got.UTF8ByteLength != 6 {
		t.Fatalf("Buffer.byteLength('音乐') = %d, want 6", got.UTF8ByteLength)
	}
}


// TestLXRConfigQuickJSStringBufferAndTimestampCompatibility exercises the JS-facing
// primitives used by V7 signature construction. Go-only vectors cannot detect
// a QuickJS bridge encoding or timestamp mismatch.
func TestLXRConfigQuickJSStringBufferAndTimestampCompatibility(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "rconfig-compat", RawScript: "fixture"}); err != nil {
		t.Fatal(err)
	}

	value, err := rt.Eval(context.Background(), `
const source = '🎵LX';
const utf8 = Buffer.from(source, 'utf8');
const utf16le = Buffer.from(source, 'utf16le');
const bytes = new Uint8Array([0x41, 0x00, 0x3c, 0xd8, 0xb5, 0xdf]);
const now = Date.now();
JSON.stringify({
  utf8Hex: utf8.toString('hex'),
  utf8RoundTrip: utf8.toString('utf8'),
  utf16leHex: utf16le.toString('hex'),
  utf16leRoundTrip: utf16le.toString('utf16le'),
  utf16leByteLength: Buffer.byteLength(source, 'utf16le'),
  md5Unicode: lx.utils.crypto.md5('洛雪音乐'),
  md5Supplementary: lx.utils.crypto.md5('🎵LX'),
  typedArrayUtf16le: Buffer.from(bytes).toString('utf16le'),
  timestampIsFiniteInteger: Number.isFinite(now) && Number.isInteger(now),
  timestampIsSafeInteger: Number.isSafeInteger(now),
  timestampNonNegative: now >= 0
})
`, "lx-rconfig-js-compat.js")
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		UTF8Hex string `json:"utf8Hex"`
		UTF8RoundTrip string `json:"utf8RoundTrip"`
		UTF16LEHex string `json:"utf16leHex"`
		UTF16LERoundTrip string `json:"utf16leRoundTrip"`
		UTF16LEByteLength int `json:"utf16leByteLength"`
		MD5Unicode string `json:"md5Unicode"`
		MD5Supplementary string `json:"md5Supplementary"`
		TypedArrayUTF16LE string `json:"typedArrayUtf16le"`
		TimestampIsFiniteInteger bool `json:"timestampIsFiniteInteger"`
		TimestampIsSafeInteger bool `json:"timestampIsSafeInteger"`
		TimestampNonNegative bool `json:"timestampNonNegative"`
	}
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("decode QuickJS compatibility result %q: %v", value, err)
	}
	if got.UTF8Hex != "f09f8eb54c58" || got.UTF8RoundTrip != "🎵LX" {
		t.Fatalf("QuickJS UTF-8 Buffer conversion mismatch: hex=%q roundtrip=%q", got.UTF8Hex, got.UTF8RoundTrip)
	}
	if got.UTF16LEHex != "3cd8b5df4c005800" || got.UTF16LERoundTrip != "🎵LX" || got.UTF16LEByteLength != 8 {
		t.Fatalf("QuickJS UTF-16LE Buffer conversion mismatch: hex=%q roundtrip=%q byteLength=%d", got.UTF16LEHex, got.UTF16LERoundTrip, got.UTF16LEByteLength)
	}
	if got.MD5Unicode != "6b18adfb3b1acece1c70a9b85aa70572" || got.MD5Supplementary != "c45f574f87faa7305bf804923d803cca" {
		t.Fatalf("QuickJS native MD5 Unicode mismatch: cjk=%q supplementary=%q", got.MD5Unicode, got.MD5Supplementary)
	}
	if got.TypedArrayUTF16LE != "A🎵" {
		t.Fatalf("QuickJS TypedArray UTF-16LE conversion = %q, want A🎵", got.TypedArrayUTF16LE)
	}
	if !got.TimestampIsFiniteInteger || !got.TimestampIsSafeInteger || !got.TimestampNonNegative {
		t.Fatalf("Date.now() is incompatible with V7 millisecond timestamp arithmetic: %+v", got)
	}
}

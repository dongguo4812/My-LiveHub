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

const responseBytes = Buffer.from('{"ok":true,"title":"音乐"}', 'utf-8');
const responseText = Buffer.from(responseBytes).toString('utf-8');
const slicedText = Buffer.from([9, 65, 66, 67, 8]).toString('utf8', 1, 4);
const arrayBufferText = Buffer.from(new Uint8Array([65, 66]).buffer).toString('utf8');
const allocatedHex = Buffer.alloc(4, 0x2a).toString('hex');
const concatenated = Buffer.concat([Buffer.from('LX'), Buffer.from('-Host')]).toString();
JSON.stringify({
  payloadBase64,
  responseText,
  responseIsBuffer: Buffer.isBuffer(responseBytes),
  typedArrayIsBuffer: Buffer.isBuffer(new Uint8Array([1, 2])),
  slicedText,
  arrayBufferText,
  allocatedHex,
  concatenated,
  utf8ByteLength: Buffer.byteLength('音乐', 'utf8')
})
`, "lx-pinned-buffer-contract.js")
	if err != nil {
		t.Fatal(err)
	}

	var got struct {
		PayloadBase64    string `json:"payloadBase64"`
		ResponseText     string `json:"responseText"`
		ResponseIsBuffer bool   `json:"responseIsBuffer"`
		TypedArrayIsBuffer bool `json:"typedArrayIsBuffer"`
		SlicedText       string `json:"slicedText"`
		ArrayBufferText  string `json:"arrayBufferText"`
		AllocatedHex     string `json:"allocatedHex"`
		Concatenated     string `json:"concatenated"`
		UTF8ByteLength   int    `json:"utf8ByteLength"`
	}
	if err := json.Unmarshal([]byte(value), &got); err != nil {
		t.Fatalf("decode Buffer contract result %q: %v", value, err)
	}
	payload, _ := json.Marshal(map[string]string{"source": "tx", "title": "音乐"})
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
	if got.AllocatedHex != "2a2a2a2a" || got.Concatenated != "LX-Host" {
		t.Fatalf("Buffer alloc/concat results: alloc=%q concat=%q", got.AllocatedHex, got.Concatenated)
	}
	if got.UTF8ByteLength != 6 {
		t.Fatalf("Buffer.byteLength('音乐') = %d, want 6", got.UTF8ByteLength)
	}
}

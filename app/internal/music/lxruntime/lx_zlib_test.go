//go:build lxquickjs && cgo

package lxruntime

import (
	"bytes"
	"encoding/base64"
	"compress/zlib"
	"testing"
	"context"
	"time"
)

func TestLXZlibDeflateInflateRoundTrip(t *testing.T) {
	input := bytes.Repeat([]byte("洛雪音源 QuickJS zlib compatibility\n"), 128)
	compressed, err := lxZlibDeflate(input)
	if err != nil { t.Fatal(err) }
	if bytes.Equal(compressed, input) { t.Fatal("deflate returned the original input") }
	restored, err := lxZlibInflate(compressed)
	if err != nil { t.Fatal(err) }
	if !bytes.Equal(restored, input) { t.Fatal("inflate did not restore original input") }
}

func TestLXZlibInflateRejectsInvalidData(t *testing.T) {
	if _, err := lxZlibInflate([]byte("not a zlib stream")); err == nil {
		t.Fatal("expected invalid zlib stream to fail")
	}
}

func TestLXZlibEnforcesInputAndOutputLimits(t *testing.T) {
	if _, err := lxZlibDeflate(bytes.Repeat([]byte("x"), lxMaxZlibInputBytes+1)); err == nil {
		t.Fatal("expected oversized deflate input to fail")
	}
	// Build the oversized stream directly: lxZlibDeflate correctly rejects
	// inputs above its own 8 MiB limit, so it cannot create this fixture.
	var encoded bytes.Buffer
	writer := zlib.NewWriter(&encoded)
	if _, err := writer.Write(bytes.Repeat([]byte("x"), lxMaxZlibOutputBytes+1)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if encoded.Len() >= lxMaxZlibInputBytes {
		t.Fatalf("test fixture unexpectedly exceeds compressed input limit: %d", encoded.Len())
	}
	if _, err := lxZlibInflate(encoded.Bytes()); err == nil {
		t.Fatal("expected oversized inflate output to fail")
	}
}


func TestLXZlibBridgeReturnsNodeCompatibleBuffer(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "zlib-buffer-contract"}); err != nil { t.Fatal(err) }
	if _, err := rt.Eval(context.Background(), `
globalThis.__lxZlibBufferState = 'pending';
lx.utils.zlib.deflate(Buffer.from('zlib-buffer-contract')).then(compressed => {
  globalThis.__lxZlibBufferState = Buffer.isBuffer(compressed) ? 'buffer' : 'not-buffer';
});
'started';
`, "lx-zlib-buffer-contract.js"); err != nil { t.Fatal(err) }
	value, err := rt.Eval(context.Background(), "String(globalThis.__lxZlibBufferState)", "lx-zlib-buffer-result.js")
	if err != nil { t.Fatal(err) }
	if value != "buffer" { t.Fatalf("zlib.deflate must resolve a Node-compatible Buffer, got %q", value) }
}


func TestLXZlibInflateBridgeReturnsNodeCompatibleBuffer(t *testing.T) {
	rt := newTestRuntime(t, 5*time.Second)
	if err := rt.InstallLXHost(context.Background(), ScriptInfo{Name: "zlib-inflate-buffer-contract"}); err != nil {
		t.Fatal(err)
	}
	compressed, err := lxZlibDeflate([]byte("zlib-buffer-contract"))
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(compressed)
	script := `
const compressed = Buffer.from('` + encoded + `', 'base64');
globalThis.__lxZlibInflateBufferState = 'pending';
lx.utils.zlib.inflate(compressed).then(restored => {
  globalThis.__lxZlibInflateBufferState = Buffer.isBuffer(restored) && restored.toString('utf8') === 'zlib-buffer-contract'
    ? 'buffer'
    : 'invalid';
});
'started';
`
	if _, err := rt.Eval(context.Background(), script, "lx-zlib-inflate-buffer-contract.js"); err != nil {
		t.Fatal(err)
	}
	value, err := rt.Eval(context.Background(), "String(globalThis.__lxZlibInflateBufferState)", "lx-zlib-inflate-buffer-result.js")
	if err != nil {
		t.Fatal(err)
	}
	if value != "buffer" {
		t.Fatalf("zlib.inflate must resolve a Node-compatible Buffer with original bytes, got %q", value)
	}
}

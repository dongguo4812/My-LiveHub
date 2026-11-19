//go:build lxquickjs && cgo

package lxruntime

import (
	"bytes"
	"compress/zlib"
	"testing"
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

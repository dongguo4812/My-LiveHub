//go:build lxquickjs && cgo

package lxruntime

import (
	"bytes"
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
	compressed, err := lxZlibDeflate(bytes.Repeat([]byte("x"), lxMaxZlibOutputBytes+1))
	if err != nil { t.Fatal(err) }
	if _, err := lxZlibInflate(compressed); err == nil {
		t.Fatal("expected oversized inflate output to fail")
	}
}

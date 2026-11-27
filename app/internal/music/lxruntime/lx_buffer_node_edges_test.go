//go:build lxquickjs

package lxruntime

import (
	"bytes"
	"testing"
)

func TestLXBufferNodeCompatibleSingleByteAndBase64InputEdges(t *testing.T) {
	tests := []struct {
		name, input, encoding string
		want []byte
	}{
		{"latin1-surrogate-pair", "💩", "latin1", []byte{0x3d, 0xa9}},
		{"binary-alias", "é", "binary", []byte{0xe9}},
		{"ascii-input-keeps-low-byte", "é", "ascii", []byte{0xe9}},
		{"base64-accepts-url-safe-alphabet", "SGVsbG8_", "base64", []byte("Hello?")},
		{"base64url-accepts-standard-alphabet", "SGVsbG8/", "base64url", []byte("Hello?")},
		{"base64-ignores-whitespace", "SGVs\n bG8=", "base64", []byte("Hello")},
		{"base64-ignores-invalid-characters", "SGVsbG8!=", "base64", []byte("Hello")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := lxBufferFromString([]byte(tt.input), tt.encoding)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, tt.want) {
				t.Fatalf("Buffer.from(%q, %q) = %x, want %x", tt.input, tt.encoding, got, tt.want)
			}
		})
	}
}

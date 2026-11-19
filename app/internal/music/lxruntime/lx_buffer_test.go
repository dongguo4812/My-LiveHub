//go:build lxquickjs && cgo

package lxruntime

import (
	"bytes"
	"testing"
)

func TestLXBufferFromNodeStringEncodings(t *testing.T) {
	tests := []struct{name, input, encoding string; want []byte}{
		{"utf8", "音乐", "utf-8", []byte{0xe9,0x9f,0xb3,0xe4,0xb9,0x90}},
		{"hex", "48656c6c6f", "hex", []byte("Hello")},
		{"base64", "SGVsbG8=", "base64", []byte("Hello")},
		{"base64url", "SGVsbG8_", "base64url", []byte("Hello?")},
		{"latin1", "café", "latin1", []byte{0x63,0x61,0x66,0xe9}},
		{"ascii", "é", "ascii", []byte{0xe9}},
		{"utf16le", "A音乐", "utf16le", []byte{0x41,0x00,0xf3,0x97,0x50,0x4e}},
	}
	for _, tt := range tests { t.Run(tt.name, func(t *testing.T) {
		got, err := lxBufferFromString([]byte(tt.input), tt.encoding)
		if err != nil { t.Fatal(err) }
		if !bytes.Equal(got, tt.want) { t.Fatalf("got %x, want %x", got, tt.want) }
	}) }
}

func TestLXBufferToStringNodeEncodings(t *testing.T) {
	tests := []struct{name, encoding, want string; input []byte}{
		{"utf8", "utf8", "音乐", []byte{0xe9,0x9f,0xb3,0xe4,0xb9,0x90}},
		{"hex", "hex", "48656c6c6f", []byte("Hello")},
		{"base64", "base64", "SGVsbG8=", []byte("Hello")},
		{"base64url", "base64url", "SGVsbG8_", []byte("Hello?")},
		{"latin1", "latin1", "café", []byte{0x63,0x61,0x66,0xe9}},
		{"ascii", "ascii", "i", []byte{0xe9}},
		{"utf16le", "utf16le", "A音乐", []byte{0x41,0x00,0xf3,0x97,0x50,0x4e}},
	}
	for _, tt := range tests { t.Run(tt.name, func(t *testing.T) {
		got, err := lxBufferToString(tt.input, tt.encoding)
		if err != nil { t.Fatal(err) }
		if got != tt.want { t.Fatalf("got %q, want %q", got, tt.want) }
	}) }
}

func TestLXBufferRejectsUnknownEncoding(t *testing.T) {
	if _, err := lxBufferFromString([]byte("x"), "made-up"); err == nil { t.Fatal("expected unknown encoding error") }
	if _, err := lxBufferToString([]byte("x"), "made-up"); err == nil { t.Fatal("expected unknown encoding error") }
}

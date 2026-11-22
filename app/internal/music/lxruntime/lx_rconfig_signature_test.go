package lxruntime

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLXRConfigV7ScriptDigest(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil {
		t.Fatal(err)
	}
	const want = "72e3d795b46dbd5672e17bd57e905eb0"
	if got := lxRConfigScriptDigest(string(source)); got != want {
		t.Fatalf("V7 script digest = %q, want %q", got, want)
	}
}

func TestLXRConfigV7DynamicPrefixVectors(t *testing.T) {
	tests := []struct {
		timestamp int64
		want      string
	}{
		{1795334998585, "e00c7004b0f0d818047438947c843028"},
		{1795335000629, "80d040f04040a0a0f03020b010f04060"},
		{1795334598584, "48fd647fd4c4ca3abfe3b25ba11f34b6"},
	}
	for _, tt := range tests {
		if got := lxRConfigDynamicPrefix(tt.timestamp); got != tt.want {
			t.Errorf("prefix(%d) = %q, want %q", tt.timestamp, got, tt.want)
		}
	}
}

func TestLXRConfigV7SignatureVectors(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "sources", "lx.js"))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		timestamp int64
		want      string
	}{
		{1795334998585, "5fe81c354121df0647cd066833d50e83"},
		{1795335000629, "cf3dfb245b6140fcbd091991f4f4bf28"},
		{1795334598584, "2221f04be883c278e5ee587b37cb4c7b"},
	}
	for _, tt := range tests {
		if got := lxRConfigSignature(tt.timestamp, string(source)); got != tt.want {
			t.Errorf("si(%d) = %q, want %q", tt.timestamp, got, tt.want)
		}
	}
}

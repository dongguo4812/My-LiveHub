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

func TestLXRConfigScriptDigestUTF16AndUTF8Cases(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{name: "empty", source: "", want: "d41d8cd98f00b204e9800998ecf8427e"},
		{name: "ascii", source: "abc", want: "cfcfa4e426d3a79a9e55948520468d06"},
		{name: "cjk-utf8", source: "洛雪音乐", want: "fdacac6faa7723832bb146b24c31e558"},
		{name: "mixed-utf8", source: "Aé中", want: "f65812fe5fa2564874703b0652656cdd"},
		{name: "supplementary-surrogate-pair", source: "🎵LX", want: "d28fe07de867bd97737f4b1b3d551a70"},
		{name: "combining-mark-not-normalized", source: "e\u0301", want: "661f1e1c09ee68c75f37106be3826951"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lxRConfigScriptDigest(tt.source); got != tt.want {
				t.Fatalf("script digest(%q) = %q, want %q", tt.source, got, tt.want)
			}
		})
	}
}

func TestLXRConfigSignatureTimestampBoundariesAndInputVariants(t *testing.T) {
	tests := []struct {
		name      string
		timestamp int64
		source    string
		want      string
	}{
		{name: "zero-timestamp", timestamp: 0, source: "洛雪音乐", want: "c4688dca24d4bb56f72881e2ab8e0f52"},
		{name: "one-millisecond", timestamp: 1, source: "洛雪音乐", want: "e7f1b4ca3741cd3baf5ed8d85ff45bd9"},
		{name: "modulus-minus-one", timestamp: lxRConfigPrefixMod - 1, source: "洛雪音乐", want: "8c813a663db53b56fc9b3b0fd7bbc9ab"},
		{name: "modulus", timestamp: lxRConfigPrefixMod, source: "洛雪音乐", want: "c4688dca24d4bb56f72881e2ab8e0f52"},
		{name: "modulus-plus-one", timestamp: lxRConfigPrefixMod + 1, source: "洛雪音乐", want: "e7f1b4ca3741cd3baf5ed8d85ff45bd9"},
		{name: "large-timestamp", timestamp: 9223372036854775807, source: "洛雪音乐", want: "61306b61464bd81685f613b0c977d642"},
		{name: "different-source", timestamp: 1795334998585, source: "🎵LX", want: "75e322baa64c8b5fbabc80e0fc14c859"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lxRConfigSignature(tt.timestamp, tt.source); got != tt.want {
				t.Fatalf("si(%d, %q) = %q, want %q", tt.timestamp, tt.source, got, tt.want)
			}
		})
	}
}

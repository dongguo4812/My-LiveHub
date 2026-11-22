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
		{name: "ascii", source: "abc", want: "9d6f71686b6a21da02f048a7a78de121"},
		{name: "cjk-utf8", source: "洛雪音乐", want: "71c24b2baa9f5f9ccf246f9980034b85"},
		{name: "mixed-utf8", source: "Aé中", want: "f86a6be751142445a280409973f71f25"},
		{name: "supplementary-surrogate-pair", source: "🎵LX", want: "0c5def35e4dbf172e01b50a3ab1984c8"},
		{name: "combining-mark-not-normalized", source: "e\u0301", want: "c15e1937f5e41a2dbf948bbfc2e30f41"},
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
		{name: "zero-timestamp", timestamp: 0, source: "洛雪音乐", want: "840cbf30082a0bb9f14de55c5f811082"},
		{name: "one-millisecond", timestamp: 1, source: "洛雪音乐", want: "b5c7ba3aa50c4ad3dbe1c48d3cac9d77"},
		{name: "modulus-minus-one", timestamp: lxRConfigPrefixMod - 1, source: "洛雪音乐", want: "0af77402b55e8cb872f23f3d9cf4b3b4"},
		{name: "modulus", timestamp: lxRConfigPrefixMod, source: "洛雪音乐", want: "840cbf30082a0bb9f14de55c5f811082"},
		{name: "modulus-plus-one", timestamp: lxRConfigPrefixMod + 1, source: "洛雪音乐", want: "b5c7ba3aa50c4ad3dbe1c48d3cac9d77"},
		{name: "different-source", timestamp: 1795334998585, source: "🎵LX", want: "bcdb96fed1c81ce645aa77ce2aeb678b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lxRConfigSignature(tt.timestamp, tt.source); got != tt.want {
				t.Fatalf("si(%d, %q) = %q, want %q", tt.timestamp, tt.source, got, tt.want)
			}
		})
	}
}

//go:build lxquickjs || lxproduction

package lxruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstrumentLXObfuscationProfile(t *testing.T) {
	source := lxObfuscationLoopStart + "checksum;" + lxObfuscationLoopEnd +
		lxObfuscationDecodeMiss + ";decode();"
	got, ok := instrumentLXObfuscationProfile(source)
	if !ok {
		t.Fatal("expected known obfuscation bootstrap to be instrumented")
	}
	for _, marker := range []string{
		"__myLiveHubLXObfProfile.iterations++",
		"__myLiveHubLXObfProfile.loopDecodeCalls=",
		"__myLiveHubLXObfProfile.loopDurationMs=",
		"__myLiveHubLXObfProfile.decodeCalls++",
	} {
		if !strings.Contains(got, marker) {
			t.Fatalf("instrumented source is missing %q", marker)
		}
	}
}

func TestInstrumentLXObfuscationProfileLeavesUnknownSourceUntouched(t *testing.T) {
	source := "while (true) {}"
	got, ok := instrumentLXObfuscationProfile(source)
	if ok {
		t.Fatal("unknown source unexpectedly matched obfuscation profile")
	}
	if got != source {
		t.Fatal("unknown source was modified")
	}
}

func TestInstrumentLXObfuscationProfileMatchesBundledMolanScript(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "lx-js", "墨澜音乐源v2.3.8.js")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read bundled Molan script: %v", err)
	}
	if _, ok := instrumentLXObfuscationProfile(string(source)); !ok {
		t.Fatal("bundled Molan script no longer matches the diagnostic instrumentation markers")
	}
}

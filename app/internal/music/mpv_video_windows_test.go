//go:build windows

package music

import (
	"strings"
	"testing"
)

func TestMpvAudioEngineRemainsWindowless(t *testing.T) {
	args := strings.Join(buildMpvArgs("\\\\.\\pipe\\audio-test", false), " ")
	for _, required := range []string{"--no-video", "--force-window=no"} {
		if !strings.Contains(args, required) {
			t.Fatalf("audio engine arguments are missing %q: %s", required, args)
		}
	}
	for _, forbidden := range []string{"--force-window=yes", "--no-border", mpvVideoWindowTitle} {
		if strings.Contains(args, forbidden) {
			t.Fatalf("audio engine unexpectedly contains video window option %q: %s", forbidden, args)
		}
	}
}

func TestMpvVideoEngineUsesCleanDedicatedWindow(t *testing.T) {
	args := strings.Join(buildMpvArgs("\\\\.\\pipe\\video-test", true), " ")
	for _, required := range []string{
		"--force-window=yes",
		"--no-border",
		"--title=" + mpvVideoWindowTitle,
		"--geometry=1280x720+100+100",
		"--osd-level=0",
		"--osd-bar=no",
		"--no-osc",
		"--input-default-bindings=no",
		"--input-vo-keyboard=no",
	} {
		if !strings.Contains(args, required) {
			t.Fatalf("video engine arguments are missing %q: %s", required, args)
		}
	}
	if strings.Contains(args, "--no-video") || strings.Contains(args, "--force-window=no") {
		t.Fatalf("video engine unexpectedly disables video output: %s", args)
	}
}

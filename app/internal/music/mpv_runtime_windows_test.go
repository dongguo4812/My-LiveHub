//go:build windows

package music

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFakeMpv(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("test executable"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveMpvExecutableFromPackagedApplicationDirectory(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "My-LiveHub")
	executable := filepath.Join(appDir, "My-LiveHub.exe")
	runtime := filepath.Join(appDir, "runtime", "mpv", "mpv.exe")
	writeFakeMpv(t, runtime)

	got, err := resolveMpvExecutableFrom("", executable, root)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(runtime)
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestResolveMpvExecutableFromDevelopmentWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	appDir := filepath.Join(root, "app")
	runtime := filepath.Join(appDir, "runtime", "mpv", "mpv.exe")
	writeFakeMpv(t, runtime)

	// go run places its temporary executable outside the repository; the
	// development working directory remains the app project directory.
	got, err := resolveMpvExecutableFrom("", filepath.Join(root, "go-build", "app.exe"), appDir)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.Abs(runtime)
	if got != want {
		t.Fatalf("resolved path = %q, want %q", got, want)
	}
}

func TestResolveMpvExecutableUsesExplicitPathAndDoesNotFallback(t *testing.T) {
	root := t.TempDir()
	valid := filepath.Join(root, "runtime", "mpv.exe")
	writeFakeMpv(t, valid)
	missing := filepath.Join(root, "missing", "mpv.exe")

	_, err := resolveMpvExecutableFrom(missing, filepath.Join(root, "app.exe"), root)
	if err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("expected explicit path error, got %v", err)
	}
}

func TestResolveMpvExecutableReportsMissingBundledRuntime(t *testing.T) {
	root := t.TempDir()
	_, err := resolveMpvExecutableFrom("", filepath.Join(root, "app.exe"), root)
	if err == nil || !strings.Contains(err.Error(), "runtime/mpv/mpv.exe") {
		t.Fatalf("expected actionable bundled-runtime error, got %v", err)
	}
}

//go:build windows

package desktop

import (
	"archive/zip"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const updaterFixtureSource = `package main

import (
	"os"
	"path/filepath"
)

var version = "0.0.0"

func main() {
	args := os.Args[1:]
	if len(args) == 2 && args[0] == "--write-version" {
		if err := os.WriteFile(args[1], []byte(version), 0644); err != nil { os.Exit(2) }
		return
	}
	if os.Getenv("MY_LIVEHUB_FIXTURE_MODE") == "installer" {
		if os.Getenv("MY_LIVEHUB_FIXTURE_FAIL") == "1" { os.Exit(9) }
		self, err := os.Executable()
		if err != nil { os.Exit(3) }
		target := os.Getenv("MY_LIVEHUB_FIXTURE_TARGET")
		if err := copyFile(self, target); err != nil { os.Exit(4) }
		return
	}
	if marker := os.Getenv("MY_LIVEHUB_FIXTURE_STARTED"); marker != "" {
		_ = os.WriteFile(marker, []byte(version), 0644)
	}
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil { return err }
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil { return err }
	return os.WriteFile(dst, data, 0755)
}
`

func buildUpdaterFixture(t *testing.T, version string) string {
	t.Helper()
	dir := t.TempDir()
	source := filepath.Join(dir, "main.go")
	if err := os.WriteFile(source, []byte(updaterFixtureSource), 0644); err != nil { t.Fatal(err) }
	output := filepath.Join(dir, "fixture.exe")
	cmd := exec.Command("go", "build", "-ldflags", "-X main.version="+version, "-o", output, source)
	if data, err := cmd.CombinedOutput(); err != nil { t.Fatalf("build updater fixture: %v\n%s", err, data) }
	return output
}

func writeUpdaterTask(t *testing.T, root, temp, exe, packagePath, packageType, targetVersion string) string {
	t.Helper()
	if err := os.MkdirAll(temp, 0755); err != nil { t.Fatal(err) }
	task := map[string]any{
		"pid": 2147483647, "root": root, "exe": exe, "package": packagePath,
		"package_type": packageType, "target_version": targetVersion, "temp_dir": temp,
	}
	data, err := json.Marshal(task)
	if err != nil { t.Fatal(err) }
	taskPath := filepath.Join(temp, "task.json")
	if err := os.WriteFile(taskPath, data, 0600); err != nil { t.Fatal(err) }
	scriptPath := filepath.Join(temp, "apply-update.ps1")
	if err := os.WriteFile(scriptPath, []byte(updatePowerShellScript), 0600); err != nil { t.Fatal(err) }
	return scriptPath
}

func runUpdaterHelper(t *testing.T, scriptPath, taskPath string, env ...string) error {
	t.Helper()
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-Task", taskPath)
	cmd.Env = append(os.Environ(), env...)
	output, err := cmd.CombinedOutput()
	if err != nil { t.Logf("updater PowerShell output: %s", output) }
	return err
}

func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil { return strings.TrimSpace(string(data)) }
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for file %s", path)
	return ""
}

func writePortableArchive(t *testing.T, archivePath, executable string) {
	t.Helper()
	file, err := os.Create(archivePath)
	if err != nil { t.Fatal(err) }
	zw := zip.NewWriter(file)
	binary, err := os.ReadFile(executable)
	if err != nil { t.Fatal(err) }
	entry, err := zw.Create("My-LiveHub/My-LiveHub.exe")
	if err != nil { t.Fatal(err) }
	if _, err := entry.Write(binary); err != nil { t.Fatal(err) }
	entry, err = zw.Create("My-LiveHub/new-release.txt")
	if err != nil { t.Fatal(err) }
	_, _ = entry.Write([]byte("new release"))
	if err := zw.Close(); err != nil { t.Fatal(err) }
	if err := file.Close(); err != nil { t.Fatal(err) }
}

func TestPortableUpdaterIntegrationSuccess(t *testing.T) {
	if runtime.GOOS != "windows" { t.Skip("Windows-only updater integration test") }
	oldExe := buildUpdaterFixture(t, "1.0.0")
	newExe := buildUpdaterFixture(t, "1.0.2")
	root := t.TempDir()
	exe := filepath.Join(root, "My-LiveHub.exe")
	oldBytes, _ := os.ReadFile(oldExe)
	if err := os.WriteFile(exe, oldBytes, 0755); err != nil { t.Fatal(err) }
	for name, value := range map[string]string{
		"config/local/settings.json": "keep-config", "cache/item.bin": "keep-cache",
		"logs/app.log": "keep-log", "secure/token.dat": "keep-secure",
	} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { t.Fatal(err) }
		if err := os.WriteFile(path, []byte(value), 0644); err != nil { t.Fatal(err) }
	}
	archive := filepath.Join(t.TempDir(), "portable.zip")
	writePortableArchive(t, archive, newExe)
	temp := filepath.Join(root, ".my-livehub-update-test")
	script := writeUpdaterTask(t, root, temp, exe, archive, "portable", "1.0.2")
	started := filepath.Join(t.TempDir(), "started.txt")
	if err := runUpdaterHelper(t, script, filepath.Join(temp, "task.json"), "MY_LIVEHUB_FIXTURE_STARTED="+started); err != nil { t.Fatalf("portable update failed: %v", err) }
	if got := waitForFile(t, started); got != "1.0.2" { t.Fatalf("started version = %q, want 1.0.2", got) }
	for name, want := range map[string]string{
		"config/local/settings.json": "keep-config", "cache/item.bin": "keep-cache",
		"logs/app.log": "keep-log", "secure/token.dat": "keep-secure",
		"new-release.txt": "new release",
	} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(data) != want { t.Fatalf("%s = %q, %v; want %q", name, data, err, want) }
	}
}

func TestPortableUpdaterIntegrationRollback(t *testing.T) {
	oldExe := buildUpdaterFixture(t, "1.0.0")
	wrongExe := buildUpdaterFixture(t, "1.0.1")
	root := t.TempDir()
	exe := filepath.Join(root, "My-LiveHub.exe")
	oldBytes, _ := os.ReadFile(oldExe)
	if err := os.WriteFile(exe, oldBytes, 0755); err != nil { t.Fatal(err) }
	archive := filepath.Join(t.TempDir(), "portable.zip")
	writePortableArchive(t, archive, wrongExe)
	temp := filepath.Join(root, ".my-livehub-update-test")
	script := writeUpdaterTask(t, root, temp, exe, archive, "portable", "1.0.2")
	started := filepath.Join(t.TempDir(), "started-old.txt")
	if err := runUpdaterHelper(t, script, filepath.Join(temp, "task.json"), "MY_LIVEHUB_FIXTURE_STARTED="+started); err == nil { t.Fatal("expected version mismatch to fail") }
	if got := waitForFile(t, started); got != "1.0.0" { t.Fatalf("rollback restarted version %q, want 1.0.0", got) }
	data, err := os.ReadFile(exe)
	if err != nil { t.Fatal(err) }
	if len(data) != len(oldBytes) { t.Fatal("old executable was not restored") }
}

func TestInstallerUpdaterIntegrationSuccess(t *testing.T) {
	oldExe := buildUpdaterFixture(t, "1.0.0")
	installer := buildUpdaterFixture(t, "1.0.2")
	root := t.TempDir()
	exe := filepath.Join(root, "My-LiveHub.exe")
	oldBytes, _ := os.ReadFile(oldExe)
	if err := os.WriteFile(exe, oldBytes, 0755); err != nil { t.Fatal(err) }
	for name, value := range map[string]string{"config/local/settings.json":"keep-config","cache/item.bin":"keep-cache","logs/app.log":"keep-log","secure/token.dat":"keep-secure"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil { t.Fatal(err) }
		if err := os.WriteFile(path, []byte(value), 0644); err != nil { t.Fatal(err) }
	}
	temp := filepath.Join(root, ".my-livehub-update-test")
	script := writeUpdaterTask(t, root, temp, exe, installer, "installer", "1.0.2")
	started := filepath.Join(t.TempDir(), "started.txt")
	if err := runUpdaterHelper(t, script, filepath.Join(temp, "task.json"),
		"MY_LIVEHUB_FIXTURE_MODE=installer", "MY_LIVEHUB_FIXTURE_TARGET="+exe,
		"MY_LIVEHUB_FIXTURE_STARTED="+started); err != nil { t.Fatalf("installer update failed: %v", err) }
	if got := waitForFile(t, started); got != "1.0.2" { t.Fatalf("started version = %q, want 1.0.2", got) }
	for name, want := range map[string]string{"config/local/settings.json":"keep-config","cache/item.bin":"keep-cache","logs/app.log":"keep-log","secure/token.dat":"keep-secure"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(data) != want { t.Fatalf("%s = %q, %v; want %q", name, data, err, want) }
	}
}

func TestInstallerUpdaterIntegrationRollback(t *testing.T) {
	oldExe := buildUpdaterFixture(t, "1.0.0")
	installer := buildUpdaterFixture(t, "1.0.2")
	root := t.TempDir()
	exe := filepath.Join(root, "My-LiveHub.exe")
	oldBytes, _ := os.ReadFile(oldExe)
	if err := os.WriteFile(exe, oldBytes, 0755); err != nil { t.Fatal(err) }
	temp := filepath.Join(root, ".my-livehub-update-test")
	script := writeUpdaterTask(t, root, temp, exe, installer, "installer", "1.0.2")
	started := filepath.Join(t.TempDir(), "started-old.txt")
	if err := runUpdaterHelper(t, script, filepath.Join(temp, "task.json"),
		"MY_LIVEHUB_FIXTURE_MODE=installer", "MY_LIVEHUB_FIXTURE_TARGET="+exe,
		"MY_LIVEHUB_FIXTURE_FAIL=1", "MY_LIVEHUB_FIXTURE_STARTED="+started); err == nil { t.Fatal("expected installer failure") }
	if got := waitForFile(t, started); got != "1.0.0" { t.Fatalf("rollback restarted version %q, want 1.0.0", got) }
	data, err := os.ReadFile(exe)
	if err != nil { t.Fatal(err) }
	if len(data) != len(oldBytes) { t.Fatal("old executable was not restored") }
}

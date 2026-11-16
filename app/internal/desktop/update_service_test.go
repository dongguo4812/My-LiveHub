package desktop

import (
	"os"
	"path/filepath"
	"testing"
	"strings"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		left, right string
		want int
	}{
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "1.0.1", -1},
		{"1.0.0", "1.0.0", 0},
		{"v1.2.0", "1.1.9", 1},
		{"1.2.0", "v1.1.9", 1},
		{"V1.2.0", "1.1.9", 1},
		{"  v1.2.0  ", "1.2.0", 0},
	}
	for _, tc := range cases {
		if got := compareVersions(tc.left, tc.right); got != tc.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", tc.left, tc.right, got, tc.want)
		}
	}
}

func TestDetectUpdatePackageType(t *testing.T) {
	root := t.TempDir()
	if got := detectUpdatePackageType(root); got != "portable" {
		t.Fatalf("default package type = %q, want portable", got)
	}
	if err := os.WriteFile(filepath.Join(root, ".my-livehub-portable"), []byte("portable"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectUpdatePackageType(root); got != "portable" {
		t.Fatalf("portable marker package type = %q, want portable", got)
	}
	if err := os.Remove(filepath.Join(root, ".my-livehub-portable")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".my-livehub-installed"), []byte("installed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := detectUpdatePackageType(root); got != "installer" {
		t.Fatalf("installed marker package type = %q, want installer", got)
	}
}

func TestUpdaterVerifiesTargetExecutableVersion(t *testing.T) {
	if !strings.Contains(updatePowerShellScript, "Assert-ExecutableVersion $exe") {
		t.Fatal("updater does not verify the installed executable version")
	}
	if !strings.Contains(updatePowerShellScript, "Wait-Process -Id $pidToWait -Timeout 180") {
		t.Fatal("updater does not wait for the old application process")
	}
}

func TestUpdateWorkspaceIsCreatedBesideApplication(t *testing.T) {
	root := t.TempDir()
	dir, err := createUpdateWorkspace(root)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	relative, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		t.Fatalf("update workspace %q is not inside application directory %q", dir, root)
	}
}


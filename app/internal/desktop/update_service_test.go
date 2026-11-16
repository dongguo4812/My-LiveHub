package desktop

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
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

func TestInstallerRollbackExcludesActiveUpdateWorkspace(t *testing.T) {
	if !strings.Contains(updatePowerShellScript, "$oldFiles = @(Get-ChildItem -LiteralPath $root -File -Recurse -Force | Where-Object") {
		t.Fatal("installer backup must exclude the active update workspace")
	}
	if !strings.Contains(updatePowerShellScript, "Where-Object { !$_.FullName.StartsWith(($temp.TrimEnd([char]92)") {
		t.Fatal("installer rollback cleanup must not delete the active update workspace")
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



func makeTestPortableZIP(t *testing.T) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("My-LiveHub/My-LiveHub.exe")
	if err != nil { t.Fatal(err) }
	if _, err := entry.Write([]byte("test executable")); err != nil { t.Fatal(err) }
	if err := writer.Close(); err != nil { t.Fatal(err) }
	return buffer.Bytes()
}

func TestDownloadAndVerify(t *testing.T) {
	validZIP := makeTestPortableZIP(t)
	tests := []struct {
		name string
		payload []byte
		declaredSize int64
		checksum string
		wantError string
	}{
		{name: "valid portable ZIP", payload: validZIP, declaredSize: int64(len(validZIP)), checksum: fmt.Sprintf("%x", sha256.Sum256(validZIP))},
		{name: "zero-byte response", payload: nil, declaredSize: 10, checksum: strings.Repeat("0", 64), wantError: "空更新包"},
		{name: "truncated response", payload: validZIP[:len(validZIP)-1], declaredSize: int64(len(validZIP)), checksum: fmt.Sprintf("%x", sha256.Sum256(validZIP)), wantError: "大小不匹配"},
		{name: "oversized chunked response", payload: append(append([]byte{}, validZIP...), 'x'), declaredSize: int64(len(validZIP)), checksum: fmt.Sprintf("%x", sha256.Sum256(validZIP)), wantError: "大小不匹配"},
		{name: "SHA256 mismatch", payload: validZIP, declaredSize: int64(len(validZIP)), checksum: strings.Repeat("0", 64), wantError: "SHA256 校验失败"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/SHA256SUMS":
					_, _ = fmt.Fprintf(w, "%s  My-LiveHub-Portable.zip\n", tc.checksum)
				case "/asset":
					// Flush headers first so net/http uses chunked transfer encoding;
					// this exercises responses where Content-Length is unavailable.
					w.(http.Flusher).Flush()
					_, _ = w.Write(tc.payload)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			service := &UpdateService{
				applicationDir: t.TempDir(),
				packageType: "portable",
				httpClient: server.Client(),
				latest: &githubRelease{Assets: []struct {
					Name string `json:"name"`
					Size int64 `json:"size"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{{Name: "SHA256SUMS", BrowserDownloadURL: server.URL + "/SHA256SUMS"}}},
				asset: &struct {
					Name string `json:"name"`
					Size int64 `json:"size"`
					BrowserDownloadURL string `json:"browser_download_url"`
				}{Name: "My-LiveHub-Portable.zip", Size: tc.declaredSize, BrowserDownloadURL: server.URL + "/asset"},
			}
			destination := filepath.Join(service.applicationDir, "update.zip")
			err := service.downloadAndVerify(destination)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("downloadAndVerify() error = %v, want substring %q", err, tc.wantError)
				}
				if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
					t.Fatalf("failed download published destination: stat error = %v", statErr)
				}
				if _, statErr := os.Stat(destination + ".part"); !os.IsNotExist(statErr) {
					t.Fatalf("failed download left partial file: stat error = %v", statErr)
				}
				return
			}
			if err != nil { t.Fatalf("downloadAndVerify() error = %v", err) }
			reader, err := zip.OpenReader(destination)
			if err != nil { t.Fatalf("verified ZIP cannot be opened: %v", err) }
			_ = reader.Close()
		})
	}
}

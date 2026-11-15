package desktop

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const updateRepository = "dongguo4812/My-LiveHub"

type UpdateInfo struct {
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	ReleaseURL     string `json:"release_url"`
	ReleaseNotes   string `json:"release_notes"`
	PackageType    string `json:"package_type"`
	AssetName      string `json:"asset_name"`
	AssetSize      int64  `json:"asset_size"`
	Available      bool   `json:"available"`
}

type githubRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Body    string `json:"body"`
	Assets  []struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

type UpdateService struct {
	currentVersion string
	executablePath string
	applicationDir string
	packageType    string
	httpClient     *http.Client
	latest         *githubRelease
	asset          *struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}
	quit func()
}

func NewUpdateService(version, executablePath string, quit func()) *UpdateService {
	absolute, _ := filepath.Abs(executablePath)
	root := filepath.Dir(absolute)
	return &UpdateService{
		currentVersion: strings.TrimPrefix(strings.TrimSpace(version), "v"),
		executablePath: absolute,
		applicationDir: root,
		packageType:    detectUpdatePackageType(root),
		quit:           quit,
		httpClient:     &http.Client{Timeout: 30 * time.Second},
	}
}

func detectUpdatePackageType(root string) string {
	if _, err := os.Stat(filepath.Join(root, ".my-livehub-portable")); err == nil {
		return "portable"
	}
	if _, err := os.Stat(filepath.Join(root, ".my-livehub-installed")); err == nil {
		return "installer"
	}
	matches, _ := filepath.Glob(filepath.Join(root, "unins*.dat"))
	if len(matches) > 0 {
		return "installer"
	}
	// Older portable releases did not contain a package marker.
	return "portable"
}

func (s *UpdateService) CheckForUpdates() (UpdateInfo, error) {
	if s == nil || runtime.GOOS != "windows" || runtime.GOARCH != "amd64" {
		return UpdateInfo{}, errors.New("当前更新器仅支持 Windows x64")
	}
	request, err := http.NewRequest(http.MethodGet, "https://api.github.com/repos/"+updateRepository+"/releases/latest", nil)
	if err != nil {
		return UpdateInfo{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return UpdateInfo{}, fmt.Errorf("获取 GitHub 最新版本失败：%w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return UpdateInfo{}, fmt.Errorf("获取 GitHub 最新版本失败：HTTP %d", response.StatusCode)
	}
	var release githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&release); err != nil {
		return UpdateInfo{}, fmt.Errorf("解析 GitHub Release 信息失败：%w", err)
	}
	assetName := "My-LiveHub-Portable.zip"
	if s.packageType == "installer" {
		assetName = "My-LiveHub-Setup.exe"
	}
	var selected *struct {
		Name               string `json:"name"`
		Size               int64  `json:"size"`
		BrowserDownloadURL string `json:"browser_download_url"`
	}
	for i := range release.Assets {
		if release.Assets[i].Name == assetName {
			selected = &release.Assets[i]
			break
		}
	}
	if selected == nil {
		return UpdateInfo{}, fmt.Errorf("最新版本缺少更新文件 %s", assetName)
	}
	s.latest = &release
	s.asset = selected
	latestVersion := strings.TrimPrefix(release.TagName, "v")
	available := compareVersions(latestVersion, s.currentVersion) > 0
	return UpdateInfo{
		CurrentVersion: s.currentVersion,
		LatestVersion:  latestVersion,
		ReleaseURL:     release.HTMLURL,
		ReleaseNotes:   release.Body,
		PackageType:    s.packageType,
		AssetName:      selected.Name,
		AssetSize:      selected.Size,
		Available:      available,
	}, nil
}

func compareVersions(left, right string) int {
	parse := func(value string) []int {
		value = strings.SplitN(value, "-", 2)[0]
		parts := strings.Split(value, ".")
		result := make([]int, len(parts))
		for i, part := range parts {
			result[i], _ = strconv.Atoi(part)
		}
		return result
	}
	a, b := parse(left), parse(right)
	for i := 0; i < len(a) || i < len(b); i++ {
		x, y := 0, 0
		if i < len(a) { x = a[i] }
		if i < len(b) { y = b[i] }
		if x > y { return 1 }
		if x < y { return -1 }
	}
	return 0
}

func (s *UpdateService) InstallUpdate() error {
	if s == nil || s.latest == nil || s.asset == nil {
		return errors.New("请先检查更新")
	}
	if compareVersions(strings.TrimPrefix(s.latest.TagName, "v"), s.currentVersion) <= 0 {
		return errors.New("当前已经是最新版本")
	}
	tempDir, err := os.MkdirTemp("", "my-livehub-update-*")
	if err != nil {
		return fmt.Errorf("创建更新临时目录失败：%w", err)
	}
	packagePath := filepath.Join(tempDir, s.asset.Name)
	if err := s.downloadAndVerify(packagePath); err != nil {
		_ = os.RemoveAll(tempDir)
		return err
	}
	task := map[string]any{
		"pid": os.Getpid(),
		"root": s.applicationDir,
		"exe": s.executablePath,
		"package": packagePath,
		"package_type": s.packageType,
		"temp_dir": tempDir,
	}
	taskBytes, err := json.Marshal(task)
	if err != nil {
		_ = os.RemoveAll(tempDir)
		return fmt.Errorf("生成更新任务失败：%w", err)
	}
	taskPath := filepath.Join(tempDir, "update-task.json")
	if err := os.WriteFile(taskPath, taskBytes, 0600); err != nil {
		_ = os.RemoveAll(tempDir)
		return fmt.Errorf("写入更新任务失败：%w", err)
	}
	scriptPath := filepath.Join(tempDir, "apply-update.ps1")
	if err := os.WriteFile(scriptPath, []byte(updatePowerShellScript), 0600); err != nil {
		_ = os.RemoveAll(tempDir)
		return fmt.Errorf("写入更新脚本失败：%w", err)
	}
	command := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-Task", taskPath)
	if err := command.Start(); err != nil {
		_ = os.RemoveAll(tempDir)
		return fmt.Errorf("启动独立更新进程失败：%w", err)
	}
	if s.quit != nil {
		go s.quit()
	}
	return nil
}

func (s *UpdateService) downloadAndVerify(destination string) error {
	checksumsURL := ""
	for _, asset := range s.latest.Assets {
		if asset.Name == "SHA256SUMS" {
			checksumsURL = asset.BrowserDownloadURL
			break
		}
	}
	if checksumsURL == "" {
		return errors.New("Release 缺少 SHA256SUMS，已取消更新")
	}
	checksumRequest, _ := http.NewRequest(http.MethodGet, checksumsURL, nil)
	checksumResponse, err := s.httpClient.Do(checksumRequest)
	if err != nil {
		return fmt.Errorf("下载校验清单失败：%w", err)
	}
	checksumData, readErr := io.ReadAll(io.LimitReader(checksumResponse.Body, 1<<20))
	checksumResponse.Body.Close()
	if readErr != nil { return fmt.Errorf("读取校验清单失败：%w", readErr) }
	if checksumResponse.StatusCode != http.StatusOK { return fmt.Errorf("下载校验清单失败：HTTP %d", checksumResponse.StatusCode) }
	expected := ""
	for _, line := range strings.Split(string(checksumData), "
") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == s.asset.Name {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if len(expected) != 64 {
		return fmt.Errorf("校验清单中没有 %s 的有效 SHA256", s.asset.Name)
	}
	request, _ := http.NewRequest(http.MethodGet, s.asset.BrowserDownloadURL, nil)
	response, err := (&http.Client{Timeout: 15 * time.Minute}).Do(request)
	if err != nil { return fmt.Errorf("下载更新包失败：%w", err) }
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK { return fmt.Errorf("下载更新包失败：HTTP %d", response.StatusCode) }
	file, err := os.Create(destination)
	if err != nil { return err }
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, 1<<30))
	closeErr := file.Close()
	if copyErr != nil { return fmt.Errorf("保存更新包失败：%w", copyErr) }
	if closeErr != nil { return closeErr }
	if s.asset.Size > 0 && written != s.asset.Size {
		return fmt.Errorf("更新包大小不匹配：期望 %d，实际 %d", s.asset.Size, written)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("更新包 SHA256 校验失败：期望 %s，实际 %s", expected, actual)
	}
	if s.packageType == "portable" {
		reader, err := zip.OpenReader(destination)
		if err != nil { return fmt.Errorf("免安装更新包不是有效 ZIP：%w", err) }
		reader.Close()
	}
	return nil
}

const updatePowerShellScript = `param([Parameter(Mandatory=$true)][string]$Task)
$ErrorActionPreference = 'Stop'
$taskData = Get-Content -LiteralPath $Task -Raw | ConvertFrom-Json
$pidToWait = [int]$taskData.pid
$root = [IO.Path]::GetFullPath([string]$taskData.root)
$exe = [IO.Path]::GetFullPath([string]$taskData.exe)
$package = [string]$taskData.package
$temp = [string]$taskData.temp_dir
try {
  Wait-Process -Id $pidToWait -ErrorAction SilentlyContinue
  if ($taskData.package_type -eq 'installer') {
    $arguments = @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',('/DIR="' + $root + '"'))
    $installer = Start-Process -FilePath $package -ArgumentList $arguments -Wait -PassThru
    if ($installer.ExitCode -ne 0) { throw "Installer exited with code $($installer.ExitCode)" }
  } else {
    $stage = Join-Path $temp 'stage'
    $backup = Join-Path $temp 'backup'
    Expand-Archive -LiteralPath $package -DestinationPath $stage -Force
    $source = Join-Path $stage 'My-LiveHub'
    if (!(Test-Path -LiteralPath (Join-Path $source 'My-LiveHub.exe'))) { throw 'Portable archive layout is invalid' }
    New-Item -ItemType Directory -Force -Path $backup | Out-Null
    function Is-Preserved([string]$relative) {
      $p = $relative.Replace([string][char]92,'/').ToLowerInvariant()
      return $p -eq 'config/local' -or $p.StartsWith('config/local/') -or
        $p -eq 'cache' -or $p.StartsWith('cache/') -or
        $p -eq 'logs' -or $p.StartsWith('logs/') -or
        $p -eq 'secure' -or $p.StartsWith('secure/') -or
        $p -eq 'extensions/lol_hex_display/data' -or $p.StartsWith('extensions/lol_hex_display/data/') -or
        $p -eq 'extensions/lol_hex_display/screenshots' -or $p.StartsWith('extensions/lol_hex_display/screenshots/') -or
        $p -eq 'extensions/lol_hex_display/logs' -or $p.StartsWith('extensions/lol_hex_display/logs/') -or
        $p -eq 'extensions/lol_hex_display/dist/lol_hex_display/logs' -or $p.StartsWith('extensions/lol_hex_display/dist/lol_hex_display/logs/')
    }
    $newFiles = @(Get-ChildItem -LiteralPath $source -File -Recurse -Force)
    $backupFiles = @{}
    foreach ($file in $newFiles) {
      $relative = $file.FullName.Substring($source.Length).TrimStart([char]92)
      if (Is-Preserved $relative) { continue }
      $destination = Join-Path $root $relative
      if (Test-Path -LiteralPath $destination -PathType Leaf) {
        $backupFile = Join-Path $backup $relative
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $backupFile) | Out-Null
        Copy-Item -LiteralPath $destination -Destination $backupFile -Force
        $backupFiles[$relative] = $true
      }
    }
    try {
      foreach ($file in $newFiles) {
        $relative = $file.FullName.Substring($source.Length).TrimStart([char]92)
        if (Is-Preserved $relative) { continue }
        $destination = Join-Path $root $relative
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
        Copy-Item -LiteralPath $file.FullName -Destination $destination -Force
      }
    } catch {
      foreach ($file in $newFiles) {
        $relative = $file.FullName.Substring($source.Length).TrimStart([char]92)
        if (Is-Preserved $relative) { continue }
        $destination = Join-Path $root $relative
        if ($backupFiles.ContainsKey($relative)) {
          $backupFile = Join-Path $backup $relative
          Copy-Item -LiteralPath $backupFile -Destination $destination -Force
        } elseif (Test-Path -LiteralPath $destination -PathType Leaf) {
          Remove-Item -LiteralPath $destination -Force -ErrorAction SilentlyContinue
        }
      }
      throw
    }
  }
  Start-Process -FilePath $exe
  Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
} catch {
  $failure = Join-Path $temp 'update-failed.txt'
  $_ | Out-String | Set-Content -LiteralPath $failure -Encoding UTF8
  Start-Process -FilePath $exe
  exit 1
}
`


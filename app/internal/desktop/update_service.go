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
	"path/filepath"
	"runtime"
	"strconv"
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
		value = strings.TrimSpace(value)
		value = strings.TrimPrefix(strings.TrimPrefix(value, "v"), "V")
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
		if i < len(a) {
			x = a[i]
		}
		if i < len(b) {
			y = b[i]
		}
		if x > y {
			return 1
		}
		if x < y {
			return -1
		}
	}
	return 0
}

func createUpdateWorkspace(applicationDir string) (string, error) {
	return os.MkdirTemp(applicationDir, ".my-livehub-update-*")
}

func (s *UpdateService) InstallUpdate() error {
	if s == nil || s.latest == nil || s.asset == nil {
		return errors.New("请先检查更新")
	}
	if compareVersions(strings.TrimPrefix(s.latest.TagName, "v"), s.currentVersion) <= 0 {
		return errors.New("当前已经是最新版本")
	}
	// Keep the update workspace beside the running application instead of
	// relying on os.TempDir(). On Windows, TEMP/TMP may point inside the
	// portable application directory (or to an unexpected custom location),
	// which can create confusing directories such as "veHub" and make the
	// helper operate on an unintended path. The application directory is
	// already required to be writable for portable updates.
	tempDir, err := createUpdateWorkspace(s.applicationDir)
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
		"target_version": strings.TrimPrefix(s.latest.TagName, "v"),
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
	if err := launchUpdateHelper(scriptPath, taskPath); err != nil {
		_ = os.RemoveAll(tempDir)
		return fmt.Errorf("启动独立更新进程失败：%w", err)
	}
	if s.quit != nil {
		// The helper independently waits for this process to exit before replacing the executable.
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
	for _, line := range strings.Split(string(checksumData), "\n") {
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
$targetVersion = [string]$taskData.target_version
$helperLog = Join-Path $temp 'update-helper.log'
function Write-UpdateLog([string]$message) {
  Add-Content -LiteralPath $helperLog -Value ("[{0}] {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $message) -Encoding UTF8
}
function Assert-ExecutableVersion([string]$path) {
  $versionFile = Join-Path $temp 'verified-version.txt'
  Remove-Item -LiteralPath $versionFile -Force -ErrorAction SilentlyContinue
  $probe = Start-Process -FilePath $path -ArgumentList @('--write-version', ('"' + $versionFile + '"')) -Wait -PassThru -WindowStyle Hidden
  if ($probe.ExitCode -ne 0 -or !(Test-Path -LiteralPath $versionFile)) {
    throw "Unable to read version from updated executable (exit code $($probe.ExitCode))"
  }
  $actualVersion = (Get-Content -LiteralPath $versionFile -Raw).Trim()
  if ($actualVersion -ne $targetVersion) {
    throw "Updated executable version mismatch: expected $targetVersion, got $actualVersion"
  }
  Write-UpdateLog "Verified executable version $actualVersion"
}
try {
  Write-UpdateLog "Starting update: target=$targetVersion root=$root exe=$exe"
  Wait-Process -Id $pidToWait -Timeout 180 -ErrorAction SilentlyContinue
  $stillRunning = Get-Process -Id $pidToWait -ErrorAction SilentlyContinue
  if ($null -ne $stillRunning) { throw "Application process $pidToWait did not exit within 180 seconds" }
  Write-UpdateLog 'Application process exited; applying files.'
  if ($taskData.package_type -eq 'installer') {
    $backup = Join-Path $temp 'installer-backup'
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
    $oldFiles = @(Get-ChildItem -LiteralPath $root -File -Recurse -Force | Where-Object { !$_.FullName.StartsWith(($temp.TrimEnd([char]92) + [IO.Path]::DirectorySeparatorChar), [StringComparison]::OrdinalIgnoreCase) })
    foreach ($file in $oldFiles) {
      $relative = $file.FullName.Substring($root.Length).TrimStart([char]92)
      if (Is-Preserved $relative) { continue }
      $backupFile = Join-Path $backup $relative
      New-Item -ItemType Directory -Force -Path (Split-Path -Parent $backupFile) | Out-Null
      Copy-Item -LiteralPath $file.FullName -Destination $backupFile -Force
    }
    # Preserve the running executable explicitly; do not rely only on recursive enumeration.
    $exeRelative = $exe.Substring($root.Length).TrimStart([char]92)
    $backupExe = Join-Path $backup $exeRelative
    if (!(Test-Path -LiteralPath $exe -PathType Leaf)) {
      throw "Installer target executable is missing before update: $exe"
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $backupExe) | Out-Null
    Copy-Item -LiteralPath $exe -Destination $backupExe -Force
    if (!(Test-Path -LiteralPath $backupExe -PathType Leaf)) {
      throw "Could not back up installer target executable: $backupExe"
    }
    Write-UpdateLog "Installer executable backed up to $backupExe"
    try {
      $arguments = @('/VERYSILENT','/SUPPRESSMSGBOXES','/NORESTART',('/DIR="' + $root + '"'))
      $installer = Start-Process -FilePath $package -ArgumentList $arguments -Wait -PassThru
      if ($installer.ExitCode -ne 0) { throw "Installer exited with code $($installer.ExitCode)" }
      Assert-ExecutableVersion $exe
    } catch {
      Get-ChildItem -LiteralPath $root -File -Recurse -Force | Where-Object { !$_.FullName.StartsWith(($temp.TrimEnd([char]92) + [IO.Path]::DirectorySeparatorChar), [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object {
        $relative = $_.FullName.Substring($root.Length).TrimStart([char]92)
        if (!(Is-Preserved $relative)) { Remove-Item -LiteralPath $_.FullName -Force -ErrorAction SilentlyContinue }
      }
      Get-ChildItem -LiteralPath $backup -File -Recurse -Force | ForEach-Object {
        $relative = $_.FullName.Substring($backup.Length).TrimStart([char]92)
        $destination = Join-Path $root $relative
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $destination) | Out-Null
        Copy-Item -LiteralPath $_.FullName -Destination $destination -Force
      }
      if (!(Test-Path -LiteralPath $backupExe -PathType Leaf)) {
        throw "Installer rollback backup is missing executable: $backupExe"
      }
      Copy-Item -LiteralPath $backupExe -Destination $exe -Force
      if (!(Test-Path -LiteralPath $exe -PathType Leaf)) {
        throw "Installer rollback could not restore executable: $exe"
      }
      Write-UpdateLog "Installer rollback restored executable from $backupExe"
      throw
    }
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
        if (Test-Path -LiteralPath $destination -PathType Leaf) {
          Remove-Item -LiteralPath $destination -Force
        }
        Copy-Item -LiteralPath $file.FullName -Destination $destination -Force
        if (!(Test-Path -LiteralPath $destination -PathType Leaf)) {
          throw "Portable update did not create destination file: $destination"
        }
        Write-UpdateLog "Portable file replaced: $relative"
      }
      Assert-ExecutableVersion $exe
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
  Write-UpdateLog "Update completed; launching $exe"
  Start-Process -FilePath $exe
  Remove-Item -LiteralPath $temp -Recurse -Force -ErrorAction SilentlyContinue
} catch {
  $errorText = ($_ | Out-String)
  try {
    Write-UpdateLog "UPDATE FAILED: $errorText"
  } catch {}
  $failureDir = Join-Path $root 'logs'
  try {
    New-Item -ItemType Directory -Force -Path $failureDir | Out-Null
    $failure = Join-Path $failureDir 'update-failed.txt'
    $errorText | Set-Content -LiteralPath $failure -Encoding UTF8
  } catch {
    try { $errorText | Set-Content -LiteralPath (Join-Path $temp 'update-failed.txt') -Encoding UTF8 } catch {}
  }
  try { Start-Process -FilePath $exe } catch { Write-UpdateLog "Could not restart application: $_" }
  exit 1
}
`


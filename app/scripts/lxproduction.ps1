param(
    [ValidateSet('dev', 'build')]
    [string]$Mode = 'dev',
    [int]$Port = 8769
)

$ErrorActionPreference = 'Stop'
$runtimeDirectory = Join-Path $PSScriptRoot '..\bin'
$runtimePath = Join-Path $runtimeDirectory 'lxqjs.dll'
$releaseTag = 'quickjs-ng-v0.18.0-lxabi2'
$assetBase = "https://github.com/dongguo4812/My-LiveHub/releases/download/$releaseTag"
$archivePath = Join-Path $runtimeDirectory 'lxqjs-windows-amd64.zip'
$checksumPath = "$archivePath.sha256"

Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
public static class LXQuickJSNativeExports {
    [DllImport("kernel32", SetLastError = true, CharSet = CharSet.Unicode)]
    private static extern IntPtr LoadLibrary(string path);
    [DllImport("kernel32", SetLastError = true, CharSet = CharSet.Ansi)]
    private static extern IntPtr GetProcAddress(IntPtr module, string name);
    [DllImport("kernel32", SetLastError = true)]
    private static extern bool FreeLibrary(IntPtr module);
    public static bool HasExport(string path, string name) {
        IntPtr module = LoadLibrary(path);
        if (module == IntPtr.Zero) return false;
        try { return GetProcAddress(module, name) != IntPtr.Zero; }
        finally { FreeLibrary(module); }
    }
}
'@

function Ensure-LXQuickJSRuntime {
    New-Item -ItemType Directory -Force -Path $runtimeDirectory | Out-Null
    if ((Test-Path -LiteralPath $runtimePath -PathType Leaf) -and
        [LXQuickJSNativeExports]::HasExport($runtimePath, 'lxqjs_eval_timed')) {
        Write-Host "Using compatible QuickJS-NG runtime: $runtimePath" -ForegroundColor Green
        return
    }
    if (Test-Path -LiteralPath $runtimePath -PathType Leaf) {
        Write-Host 'Existing QuickJS-NG DLL is outdated (missing lxqjs_eval_timed); replacing it with the pinned runtime.' -ForegroundColor Yellow
    }

    Write-Host "Preparing pinned QuickJS-NG runtime $releaseTag..." -ForegroundColor Cyan
    try {
        Invoke-WebRequest -Uri "$assetBase/lxqjs-windows-amd64.zip" -OutFile $archivePath -UseBasicParsing
        Invoke-WebRequest -Uri "$assetBase/lxqjs-windows-amd64.zip.sha256" -OutFile $checksumPath -UseBasicParsing
    } catch {
        throw "Unable to download the pinned QuickJS-NG runtime release asset $releaseTag. Details: $($_.Exception.Message)"
    }

    $expected = ((Get-Content -LiteralPath $checksumPath -Raw).Trim() -split '\s+')[0].ToLowerInvariant()
    $actual = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($expected -notmatch '^[0-9a-f]{64}$' -or $actual -ne $expected) {
        Remove-Item -LiteralPath $archivePath, $checksumPath -Force -ErrorAction SilentlyContinue
        throw 'QuickJS-NG runtime archive SHA256 verification failed.'
    }

    $extractDirectory = Join-Path $runtimeDirectory 'quickjs-extract'
    Remove-Item -LiteralPath $extractDirectory -Recurse -Force -ErrorAction SilentlyContinue
    Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDirectory -Force
    $dll = Get-ChildItem -LiteralPath $extractDirectory -Filter 'lxqjs.dll' -File -Recurse | Select-Object -First 1
    if ($null -eq $dll) { throw 'Pinned QuickJS-NG package does not contain lxqjs.dll.' }
    if (![LXQuickJSNativeExports]::HasExport($dll.FullName, 'lxqjs_eval_timed')) {
        throw 'Pinned QuickJS-NG DLL is missing the required lxqjs_eval_timed export.'
    }
    Copy-Item -LiteralPath $dll.FullName -Destination $runtimePath -Force
    Remove-Item -LiteralPath $extractDirectory -Recurse -Force
    Write-Host "QuickJS-NG runtime ready: $runtimePath" -ForegroundColor Green
}

function Ensure-WindowsApplicationManifest {
    $appRoot = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
    $manifestPath = Join-Path $appRoot 'build\windows\My-LiveHub.manifest'
    $iconPath = Join-Path $appRoot 'build\icon.ico'
    $resourcePath = Join-Path $appRoot 'rsrc_windows_amd64.syso'

    if (!(Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
        throw "Windows application manifest is missing: $manifestPath"
    }
    if (!(Test-Path -LiteralPath $iconPath -PathType Leaf)) {
        throw "Windows application icon is missing: $iconPath"
    }

    Push-Location $appRoot
    try {
        if (Get-Command wails3 -ErrorAction SilentlyContinue) {
            & wails3 generate syso -manifest $manifestPath -icon $iconPath -arch amd64 -out $resourcePath
        } else {
            & go run github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0 generate syso -manifest $manifestPath -icon $iconPath -arch amd64 -out $resourcePath
        }
        if ($LASTEXITCODE -ne 0) {
            throw "Embedding the Windows application manifest failed with exit code $LASTEXITCODE"
        }
        if (!(Test-Path -LiteralPath $resourcePath -PathType Leaf)) {
            throw "Windows application resource was not generated: $resourcePath"
        }
    } finally {
        Pop-Location
    }

    Write-Host 'Embedded the shared asInvoker Windows application manifest.' -ForegroundColor Green
}

Ensure-LXQuickJSRuntime
Ensure-WindowsApplicationManifest
$env:MYLIVEHUB_QUICKJS_LIB = (Resolve-Path -LiteralPath $runtimePath).Path
$env:CGO_ENABLED = '0'

if ($Mode -eq 'dev') {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'dev-preflight.ps1') -Port $Port
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    & wails3 generate bindings -ts
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    & wails3 dev -config ./build/config.yml -port $Port
    exit $LASTEXITCODE
}

$env:GOFLAGS = '-tags=lxproduction'
New-Item -ItemType Directory -Force -Path (Join-Path $PSScriptRoot '..\bin') | Out-Null
$executablePath = Join-Path $PSScriptRoot '..\bin\My-LiveHub.exe'
Write-Host 'Building the production desktop executable directly with Go (avoids recursively invoking the Taskfile build task)...' -ForegroundColor Cyan
& go build -o $executablePath .
if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
Write-Host "Build completed: $executablePath" -ForegroundColor Green
exit 0

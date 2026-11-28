param(
    [ValidateSet('dev', 'build')]
    [string]$Mode = 'dev',
    [int]$Port = 8769
)

$ErrorActionPreference = 'Stop'
$runtimeDirectory = Join-Path $PSScriptRoot '..\bin'
$runtimePath = Join-Path $runtimeDirectory 'lxqjs.dll'
$releaseApiUrl = 'https://api.github.com/repos/dongguo4812/My-LiveHub/releases/latest'

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
        Write-Host 'Existing QuickJS-NG DLL is incompatible (missing lxqjs_eval_timed); replacing it with the latest official My-LiveHub release runtime.' -ForegroundColor Yellow
    }

    $downloadPath = Join-Path $runtimeDirectory ("lxqjs-" + [guid]::NewGuid().ToString('N') + '.dll')
    try {
        Write-Host 'Resolving the latest official My-LiveHub release runtime...' -ForegroundColor Cyan
        $release = Invoke-RestMethod -Uri $releaseApiUrl -Headers @{
            Accept = 'application/vnd.github+json'
            'X-GitHub-Api-Version' = '2022-11-28'
            'User-Agent' = 'My-LiveHub-runtime-bootstrap'
        }
        $asset = @($release.assets | Where-Object { $_.name -eq 'lxqjs.dll' } | Select-Object -First 1)
        if ($asset.Count -ne 1) {
            throw "Latest official release $($release.tag_name) does not provide exactly one lxqjs.dll asset."
        }
        $digestMatch = [regex]::Match([string]$asset[0].digest, '^sha256:([0-9a-fA-F]{64})
        Write-Host "Downloading lxqjs.dll from official release $($release.tag_name)..." -ForegroundColor Cyan
        Invoke-WebRequest -Uri $asset[0].browser_download_url -OutFile $downloadPath -UseBasicParsing

        $actual = (Get-FileHash -LiteralPath $downloadPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            throw "Downloaded lxqjs.dll SHA256 verification failed for release $($release.tag_name)."
        }
        if (![LXQuickJSNativeExports]::HasExport($downloadPath, 'lxqjs_eval_timed')) {
            throw "Downloaded lxqjs.dll from release $($release.tag_name) is missing the required lxqjs_eval_timed export."
        }

        Move-Item -LiteralPath $downloadPath -Destination $runtimePath -Force
        Write-Host "QuickJS-NG runtime ready from release $($release.tag_name): $runtimePath" -ForegroundColor Green
    } catch {
        Remove-Item -LiteralPath $downloadPath -Force -ErrorAction SilentlyContinue
        throw "Unable to prepare lxqjs.dll from the latest official My-LiveHub release. Details: $($_.Exception.Message)"
    }
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
)
        if (!$digestMatch.Success) {
            throw "Latest official release $($release.tag_name) does not provide lxqjs.dll with a valid SHA256 digest."
        }

        $expected = $digestMatch.Groups[1].Value.ToLowerInvariant()
        Write-Host "Downloading lxqjs.dll from official release $($release.tag_name)..." -ForegroundColor Cyan
        Invoke-WebRequest -Uri $asset[0].browser_download_url -OutFile $downloadPath -UseBasicParsing

        $actual = (Get-FileHash -LiteralPath $downloadPath -Algorithm SHA256).Hash.ToLowerInvariant()
        if ($actual -ne $expected) {
            throw "Downloaded lxqjs.dll SHA256 verification failed for release $($release.tag_name)."
        }
        if (![LXQuickJSNativeExports]::HasExport($downloadPath, 'lxqjs_eval_timed')) {
            throw "Downloaded lxqjs.dll from release $($release.tag_name) is missing the required lxqjs_eval_timed export."
        }

        Move-Item -LiteralPath $downloadPath -Destination $runtimePath -Force
        Write-Host "QuickJS-NG runtime ready from release $($release.tag_name): $runtimePath" -ForegroundColor Green
    } catch {
        Remove-Item -LiteralPath $downloadPath -Force -ErrorAction SilentlyContinue
        throw "Unable to prepare lxqjs.dll from the latest official My-LiveHub release. Details: $($_.Exception.Message)"
    }
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

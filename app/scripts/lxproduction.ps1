param(
    [ValidateSet('dev', 'build')]
    [string]$Mode = 'dev',
    [int]$Port = 8769
)

$ErrorActionPreference = 'Stop'
$runtimeDirectory = Join-Path $PSScriptRoot '..\bin'
$runtimePath = Join-Path $runtimeDirectory 'lxqjs.dll'
$releaseTag = 'quickjs-ng-v0.18.0-lxabi1'
$assetBase = "https://github.com/dongguo4812/My-LiveHub/releases/download/$releaseTag"
$archivePath = Join-Path $runtimeDirectory 'lxqjs-windows-amd64.zip'
$checksumPath = "$archivePath.sha256"

function Ensure-LXQuickJSRuntime {
    New-Item -ItemType Directory -Force -Path $runtimeDirectory | Out-Null
    if (Test-Path -LiteralPath $runtimePath -PathType Leaf) {
        Write-Host "Using bundled QuickJS-NG runtime: $runtimePath" -ForegroundColor Green
        return
    }

    Write-Host 'Preparing the pinned QuickJS-NG runtime (no local C compiler required)...' -ForegroundColor Cyan
    try {
        Invoke-WebRequest -Uri "$assetBase/lxqjs-windows-amd64.zip" -OutFile $archivePath -UseBasicParsing
        Invoke-WebRequest -Uri "$assetBase/lxqjs-windows-amd64.zip.sha256" -OutFile $checksumPath -UseBasicParsing
    } catch {
        throw "Unable to download the prebuilt QuickJS-NG runtime. Ensure the repository QuickJS-NG workflow has published release $releaseTag. Details: $($_.Exception.Message)"
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
    if ($null -eq $dll) { throw 'Downloaded QuickJS-NG package does not contain lxqjs.dll.' }
    Copy-Item -LiteralPath $dll.FullName -Destination $runtimePath -Force
    Remove-Item -LiteralPath $extractDirectory -Recurse -Force
    Write-Host "QuickJS-NG runtime ready: $runtimePath" -ForegroundColor Green
}

Ensure-LXQuickJSRuntime
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

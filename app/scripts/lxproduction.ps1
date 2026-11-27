param(
    [ValidateSet('dev', 'build')]
    [string]$Mode = 'dev',
    [int]$Port = 8769
)

$ErrorActionPreference = 'Stop'
$runtimeDirectory = Join-Path $PSScriptRoot '..\bin'
$runtimePath = Join-Path $runtimeDirectory 'lxqjs.dll'
$quickjsSourceDirectory = Join-Path $runtimeDirectory 'quickjs-ng-v0.18.0'
$quickjsBuildDirectory = Join-Path $runtimeDirectory 'quickjs-ng-build'

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

function Find-QuickJSBuildTool([string]$Name, [string[]]$FallbackPaths) {
    $command = Get-Command $Name -ErrorAction SilentlyContinue
    if ($null -ne $command) { return $command.Source }
    foreach ($candidate in $FallbackPaths) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) { return $candidate }
    }
    return $null
}

function Ensure-LXQuickJSRuntime {
    New-Item -ItemType Directory -Force -Path $runtimeDirectory | Out-Null
    if ((Test-Path -LiteralPath $runtimePath -PathType Leaf) -and
        [LXQuickJSNativeExports]::HasExport($runtimePath, 'lxqjs_eval_timed')) {
        Write-Host "Using compatible QuickJS-NG runtime: $runtimePath" -ForegroundColor Green
        return
    }

    $ucrtBin = 'C:\msys64\ucrt64\bin'
    $cmake = Find-QuickJSBuildTool 'cmake' @("$ucrtBin\cmake.exe")
    $ninja = Find-QuickJSBuildTool 'ninja' @("$ucrtBin\ninja.exe")
    $gcc = Find-QuickJSBuildTool 'gcc' @("$ucrtBin\gcc.exe")
    if (!$cmake -or !$ninja -or !$gcc) {
        throw 'A compatible QuickJS-NG DLL is missing and the local build toolchain was not found. Install MSYS2 UCRT64 packages mingw-w64-ucrt-x86_64-gcc, mingw-w64-ucrt-x86_64-cmake and mingw-w64-ucrt-x86_64-ninja, then rerun task dev.'
    }

    $env:PATH = "$ucrtBin;$([System.IO.Path]::GetDirectoryName($gcc));$env:PATH"
    $git = Get-Command git -ErrorAction SilentlyContinue
    $gitPath = if ($null -ne $git) { $git.Source } elseif (Test-Path 'C:\msys64\usr\bin\git.exe') { 'C:\msys64\usr\bin\git.exe' } else { $null }
    if (!$gitPath) {
        throw 'Git is required to fetch the pinned QuickJS-NG v0.18.0 source.'
    }

    if (!(Test-Path -LiteralPath (Join-Path $quickjsSourceDirectory 'CMakeLists.txt') -PathType Leaf)) {
        Remove-Item -LiteralPath $quickjsSourceDirectory -Recurse -Force -ErrorAction SilentlyContinue
        Write-Host 'Fetching pinned QuickJS-NG v0.18.0 source...' -ForegroundColor Cyan
        & $gitPath clone --depth 1 --branch v0.18.0 https://github.com/quickjs-ng/quickjs.git $quickjsSourceDirectory
        if ($LASTEXITCODE -ne 0) { throw 'Unable to fetch pinned QuickJS-NG v0.18.0 source.' }
    }

    Write-Host 'Building the pinned QuickJS-NG runtime locally...' -ForegroundColor Cyan
    & $cmake -S (Join-Path $PSScriptRoot '..\internal\music\lxruntime\native') -B $quickjsBuildDirectory -G Ninja "-DCMAKE_MAKE_PROGRAM=$ninja" "-DCMAKE_C_COMPILER=$gcc" -DCMAKE_BUILD_TYPE=Release "-DQUICKJS_NG_SOURCE_DIR=$quickjsSourceDirectory"
    if ($LASTEXITCODE -ne 0) { throw 'QuickJS-NG CMake configuration failed.' }
    & $cmake --build $quickjsBuildDirectory --config Release
    if ($LASTEXITCODE -ne 0) { throw 'QuickJS-NG runtime compilation failed.' }

    $builtDll = Join-Path $quickjsBuildDirectory 'runtime\lxqjs.dll'
    if (!(Test-Path -LiteralPath $builtDll -PathType Leaf)) {
        $builtDll = Get-ChildItem -LiteralPath $quickjsBuildDirectory -Filter 'lxqjs.dll' -File -Recurse |
            Select-Object -First 1 -ExpandProperty FullName
    }
    if (!$builtDll) { throw 'QuickJS-NG build completed without producing lxqjs.dll.' }
    if (![LXQuickJSNativeExports]::HasExport($builtDll, 'lxqjs_eval_timed')) {
        throw 'Locally built QuickJS-NG DLL is missing the required lxqjs_eval_timed export.'
    }

    Copy-Item -LiteralPath $builtDll -Destination $runtimePath -Force
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

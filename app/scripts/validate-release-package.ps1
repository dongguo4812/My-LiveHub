$ErrorActionPreference = "Stop"

$packageRoot = (Resolve-Path "bin/package").Path
$installerPath = (Resolve-Path "bin/My-LiveHub-Setup.exe").Path
$portableZip = (Resolve-Path "bin/My-LiveHub-Portable.zip").Path
$standaloneExe = (Resolve-Path "bin/My-LiveHub-windows-amd64.exe").Path
$standaloneDLL = (Resolve-Path "bin/lxqjs.dll").Path
$checksumFile = (Resolve-Path "bin/SHA256SUMS").Path

function Assert-RequiredFiles {
    param(
        [string]$Root,
        [string[]]$RelativePaths,
        [string]$PackageName
    )

    foreach ($relativePath in $RelativePaths) {
        $path = Join-Path $Root $relativePath
        if (!(Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "$PackageName is missing required file: $relativePath"
        }
        if ((Get-Item -LiteralPath $path).Length -le 0) {
            throw "$PackageName contains an empty required file: $relativePath"
        }
    }
}

$requiredPackageFiles = @(
    "My-LiveHub.exe",
    "lxqjs.dll",
    "modules/lol_hex_display/dist/lol_hex_display/lol_hex_display.exe",
    "modules/lol_hex_display/dist/lol_hex_display/_internal/base_library.zip",
    "modules/lol_hex_display/dist/lol_hex_display/templates/manager.html",
    "modules/lol_hex_display/dist/lol_hex_display/static/style.css"
)

Assert-RequiredFiles -Root $packageRoot -RelativePaths $requiredPackageFiles -PackageName "Assembled package"

if ((Get-FileHash (Join-Path $packageRoot "lxqjs.dll") -Algorithm SHA256).Hash -ne
    (Get-FileHash $standaloneDLL -Algorithm SHA256).Hash) {
    throw "Standalone lxqjs.dll does not match the packaged runtime"
}
if ((Get-FileHash (Join-Path $packageRoot "My-LiveHub.exe") -Algorithm SHA256).Hash -ne
    (Get-FileHash $standaloneExe -Algorithm SHA256).Hash) {
    throw "Standalone executable does not match the packaged executable"
}

$expectedAssets = @(
    "My-LiveHub-Portable.zip",
    "My-LiveHub-Setup.exe",
    "My-LiveHub-windows-amd64.exe",
    "lxqjs.dll"
)
$checksumLines = Get-Content -LiteralPath $checksumFile
if ($checksumLines.Count -ne $expectedAssets.Count) {
    throw "SHA256SUMS must contain exactly $($expectedAssets.Count) asset entries"
}
foreach ($assetName in $expectedAssets) {
    $matches = @($checksumLines | Where-Object { $_ -match ("^[0-9a-fA-F]{64}\s+" + [regex]::Escape($assetName) + "$") })
    if ($matches.Count -ne 1) {
        throw "SHA256SUMS must contain exactly one valid entry for $assetName"
    }
    $expectedHash = ($matches[0] -split "\s+")[0].ToLowerInvariant()
    $actualHash = (Get-FileHash (Join-Path "bin" $assetName) -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actualHash -ne $expectedHash) {
        throw "SHA256 mismatch for $assetName"
    }
}

$tempRoot = Join-Path $env:RUNNER_TEMP ("mylivehub-package-validation-" + [guid]::NewGuid().ToString("N"))
$portableRoot = Join-Path $tempRoot "portable"
$installRoot = Join-Path $tempRoot "installed"
New-Item -ItemType Directory -Force -Path $portableRoot, $installRoot | Out-Null

try {
    Expand-Archive -LiteralPath $portableZip -DestinationPath $portableRoot -Force
    $portableAppRoot = Join-Path $portableRoot "My-LiveHub"
    if (!(Test-Path -LiteralPath (Join-Path $portableAppRoot ".my-livehub-portable") -PathType Leaf)) {
        throw "Portable ZIP is missing its package marker or has an unexpected root directory"
    }
    Assert-RequiredFiles -Root $portableAppRoot -RelativePaths $requiredPackageFiles -PackageName "Portable ZIP"

    $installProcess = Start-Process -FilePath $installerPath -ArgumentList @(
        "/VERYSILENT",
        "/SUPPRESSMSGBOXES",
        "/NORESTART",
        "/SP-",
        "/DIR=$installRoot"
    ) -Wait -PassThru
    if ($installProcess.ExitCode -ne 0) {
        throw "Silent installer validation failed with exit code $($installProcess.ExitCode)"
    }

    Assert-RequiredFiles -Root $installRoot -RelativePaths $requiredPackageFiles -PackageName "Installed application"
    if (!(Test-Path -LiteralPath (Join-Path $installRoot ".my-livehub-installed") -PathType Leaf)) {
        throw "Installer did not complete its post-install marker step"
    }

    Write-Host "Release package validation passed: assembled directory, portable ZIP, installer contents, and SHA256SUMS."
}
finally {
    Remove-Item -LiteralPath $tempRoot -Recurse -Force -ErrorAction SilentlyContinue
}

[CmdletBinding()]
param(
    [string]$Destination = ""
)

$ErrorActionPreference = "Stop"

if ([string]::IsNullOrWhiteSpace($Destination)) {
    $Destination = Join-Path $PSScriptRoot "..\\runtime\\mpv"
}

$version = "0.42.0"
$assetName = "mpv-v$version-x86_64-w64-mingw32-full.zip"
$downloadUrl = "https://github.com/mpv-player/mpv/releases/download/v$version/$assetName"
$expectedSha256 = "c7d4ff6a67dd33659f71d81efa56335fea2213c7522fd67235f3abf0d9ea1dbd"

$cacheDirectory = Join-Path ([System.IO.Path]::GetTempPath()) "My-LiveHub-mpv"
$archivePath = Join-Path $cacheDirectory $assetName
$extractDirectory = Join-Path $cacheDirectory "extract"
$destinationPath = [System.IO.Path]::GetFullPath($Destination)

New-Item -ItemType Directory -Path $cacheDirectory -Force | Out-Null

$download = $true
if (Test-Path -LiteralPath $archivePath) {
    $cachedHash = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
    $download = ($cachedHash -ne $expectedSha256)
    if ($download) {
        Remove-Item -LiteralPath $archivePath -Force
    }
}

if ($download) {
    Write-Host "Downloading mpv $version Windows x64 full runtime..."
    Invoke-WebRequest -Uri $downloadUrl -OutFile $archivePath
}

$actualSha256 = (Get-FileHash -LiteralPath $archivePath -Algorithm SHA256).Hash.ToLowerInvariant()
if ($actualSha256 -ne $expectedSha256) {
    Remove-Item -LiteralPath $archivePath -Force
    throw "mpv archive SHA-256 mismatch. Expected $expectedSha256, got $actualSha256"
}

if (Test-Path -LiteralPath $extractDirectory) {
    Remove-Item -LiteralPath $extractDirectory -Recurse -Force
}
New-Item -ItemType Directory -Path $extractDirectory -Force | Out-Null
Expand-Archive -LiteralPath $archivePath -DestinationPath $extractDirectory -Force

$mpvExecutable = Get-ChildItem -LiteralPath $extractDirectory -Filter "mpv.exe" -File -Recurse | Select-Object -First 1
if ($null -eq $mpvExecutable) {
    throw "The verified mpv archive does not contain mpv.exe"
}

$licenseFile = Get-ChildItem -LiteralPath $extractDirectory -Filter "LICENSE.GPL" -File -Recurse | Select-Object -First 1
if ($null -eq $licenseFile) {
    throw "The verified mpv archive does not contain LICENSE.GPL"
}

if (Test-Path -LiteralPath $destinationPath) {
    Remove-Item -LiteralPath $destinationPath -Recurse -Force
}
New-Item -ItemType Directory -Path $destinationPath -Force | Out-Null

# Keep every file adjacent to mpv.exe so the full build's runtime resources
# and any accompanying DLLs/configuration are preserved.
Get-ChildItem -LiteralPath $mpvExecutable.Directory.FullName -Force |
    Copy-Item -Destination $destinationPath -Recurse -Force

$installedExecutable = Join-Path $destinationPath "mpv.exe"
if (!(Test-Path -LiteralPath $installedExecutable -PathType Leaf)) {
    throw "mpv.exe was not installed at the expected destination: $installedExecutable"
}
$installedLicense = Join-Path $destinationPath $licenseFile.Name
if (!(Test-Path -LiteralPath $installedLicense -PathType Leaf)) {
    Copy-Item -LiteralPath $licenseFile.FullName -Destination $installedLicense -Force
}

Write-Host "mpv $version runtime installed locally."
Write-Host "Executable: $installedExecutable"
Write-Host "SHA-256: $actualSha256"
Write-Host "License notice: $(Join-Path $destinationPath $licenseFile.Name)"
Write-Host "Note: this pinned candidate is for local/internal validation only; redistribution remains blocked by SOURCE_BUNDLE_AUDIT.md."

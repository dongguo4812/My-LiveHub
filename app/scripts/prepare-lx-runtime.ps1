param()

$ErrorActionPreference = 'Stop'
$runtimeRoot = Join-Path $PSScriptRoot '..\lx-runtime'
$esbuildCommand = Join-Path $runtimeRoot 'node_modules\.bin\esbuild.cmd'
$electronExecutable = Join-Path $runtimeRoot 'node_modules\electron\dist\electron.exe'

if (-not (Test-Path $esbuildCommand) -or -not (Test-Path $electronExecutable)) {
    Write-Host 'Installing LX Electron runtime dependencies...'
    npm --prefix $runtimeRoot install --no-audit --no-fund
    if ($LASTEXITCODE -ne 0) {
        throw "LX runtime dependency installation failed with exit code $LASTEXITCODE."
    }
} else {
    Write-Host 'LX Electron runtime dependencies are already installed.'
}

Write-Host 'Building the original LX preload runtime...'
npm --prefix $runtimeRoot run build
if ($LASTEXITCODE -ne 0) {
    throw "LX runtime build failed with exit code $LASTEXITCODE."
}

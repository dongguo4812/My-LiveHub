param()

$ErrorActionPreference = 'Stop'
$runtimeRoot = Join-Path $PSScriptRoot '..\lx-runtime'
$esbuildCommand = Join-Path $runtimeRoot 'node_modules\.bin\esbuild.cmd'
$electronExecutable = Join-Path $runtimeRoot 'node_modules\electron\dist\electron.exe'
$electronInstallCommand = Join-Path $runtimeRoot 'node_modules\.bin\install-electron.cmd'
$needleRoot = Join-Path $runtimeRoot 'vendor\needle'
$needleCommit = '93299ac841b7e9a9f82ca7279b88aaaeda404060'

function Invoke-Git {
    param([string[]]$GitArguments)

    & git @GitArguments
    if ($LASTEXITCODE -ne 0) {
        throw "Git command failed: git $($GitArguments -join ' ') (exit code $LASTEXITCODE)."
    }
}

if (-not (Test-Path (Join-Path $needleRoot '.git'))) {
    if (Test-Path $needleRoot) {
        throw "Needle vendor directory exists but is not a Git checkout: $needleRoot"
    }

    New-Item -ItemType Directory -Force -Path (Split-Path $needleRoot -Parent) | Out-Null
    New-Item -ItemType Directory -Force -Path $needleRoot | Out-Null
    Invoke-Git -GitArguments @('-C', $needleRoot, 'init')
    Invoke-Git -GitArguments @('-C', $needleRoot, 'remote', 'add', 'origin', 'https://github.com/lyswhut/needle.git')
}

Write-Host "Fetching pinned LX Needle source from GitHub ($needleCommit)..."
Invoke-Git -GitArguments @('-C', $needleRoot, 'fetch', '--depth', '1', 'origin', $needleCommit)
Invoke-Git -GitArguments @('-C', $needleRoot, 'checkout', '--force', '--detach', 'FETCH_HEAD')
$actualNeedleCommit = (& git -C $needleRoot rev-parse HEAD).Trim()
if ($LASTEXITCODE -ne 0 -or $actualNeedleCommit -ne $needleCommit) {
    throw "Needle source revision mismatch. Expected $needleCommit, got $actualNeedleCommit."
}

if (-not (Test-Path $esbuildCommand) -or -not (Test-Path $electronInstallCommand)) {
    Write-Host 'Installing LX Electron runtime dependencies...'
    & npm.cmd --prefix $runtimeRoot install --no-audit --no-fund
    if ($LASTEXITCODE -ne 0) {
        throw "LX runtime dependency installation failed with exit code $LASTEXITCODE."
    }
} else {
    Write-Host 'LX Electron runtime dependencies are already installed.'
}

if (-not (Test-Path $electronExecutable)) {
    Write-Host 'Downloading the Electron 42 runtime binary...'
    if (-not (Test-Path $electronInstallCommand)) {
        throw "Electron install command was not found: $electronInstallCommand"
    }

    & $electronInstallCommand --no
    if ($LASTEXITCODE -ne 0) {
        throw "Electron binary download failed with exit code $LASTEXITCODE."
    }
}

if (-not (Test-Path $electronExecutable)) {
    throw "Electron binary download reported success, but the executable is still missing: $electronExecutable"
}

Write-Host 'Building the original LX preload runtime...'
& npm.cmd --prefix $runtimeRoot run build
if ($LASTEXITCODE -ne 0) {
    throw "LX runtime build failed with exit code $LASTEXITCODE."
}

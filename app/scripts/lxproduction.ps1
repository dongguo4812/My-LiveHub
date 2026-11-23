param(
    [ValidateSet('dev', 'build')]
    [string]$Mode = 'dev',
    [int]$Port = 8769
)

$ErrorActionPreference = 'Stop'

function Resolve-LXProductionCompiler {
    $configured = @()
    if (-not [string]::IsNullOrWhiteSpace($env:CC)) {
        $configured += $env:CC.Trim('"')
    }

    $gccCommand = Get-Command gcc.exe -ErrorAction SilentlyContinue
    if ($null -ne $gccCommand) {
        $configured += $gccCommand.Source
    }

    $candidateDirectories = @(
        'C:\msys64\ucrt64\bin',
        'C:\msys64\mingw64\bin',
        'C:\tools\msys64\ucrt64\bin',
        'C:\mingw64\bin',
        'C:\tools\mingw64\bin'
    )
    if (-not [string]::IsNullOrWhiteSpace($env:MSYS2_ROOT)) {
        $candidateDirectories += (Join-Path $env:MSYS2_ROOT 'ucrt64\bin')
        $candidateDirectories += (Join-Path $env:MSYS2_ROOT 'mingw64\bin')
    }

    foreach ($directory in $candidateDirectories) {
        $configured += (Join-Path $directory 'gcc.exe')
    }

    foreach ($candidate in ($configured | Select-Object -Unique)) {
        if ([string]::IsNullOrWhiteSpace($candidate)) { continue }

        $resolved = $null
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            $resolved = (Resolve-Path -LiteralPath $candidate).Path
        } else {
            $command = Get-Command $candidate -ErrorAction SilentlyContinue
            if ($null -ne $command) { $resolved = $command.Source }
        }
        if ($null -eq $resolved) { continue }

        $directory = Split-Path -Parent $resolved
        if (($env:PATH -split ';') -notcontains $directory) {
            $env:PATH = "$directory;$env:PATH"
        }
        $env:CC = $resolved

        & $resolved --version *> $null
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Using CGO C compiler: $resolved" -ForegroundColor Green
            return
        }
    }

    Write-Host ''
    Write-Host 'My-LiveHub production LX runtime requires CGO and a Windows GCC toolchain.' -ForegroundColor Red
    Write-Host 'No usable gcc.exe was found. Install MSYS2 and its UCRT64 GCC package:' -ForegroundColor Yellow
    Write-Host '  1. winget install --id MSYS2.MSYS2 -e'
    Write-Host '  2. Open "MSYS2 UCRT64" from the Start menu and run:'
    Write-Host '     pacman -S --needed mingw-w64-ucrt-x86_64-gcc'
    Write-Host '  3. Restart PowerShell and run: task dev'
    Write-Host 'If GCC is installed elsewhere, add its bin directory to PATH or set CC to the full gcc.exe path.'
    throw 'Missing CGO C compiler (gcc.exe); Wails was not started.'
}

Resolve-LXProductionCompiler

if ($Mode -eq 'dev') {
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'dev-preflight.ps1') -Port $Port
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    & wails3 generate bindings -ts
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    & wails3 dev -config ./build/config.yml -port $Port
    exit $LASTEXITCODE
}

& wails3 build
exit $LASTEXITCODE

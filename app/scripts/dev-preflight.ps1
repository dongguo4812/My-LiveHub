param(
    [int]$Port = 8769
)

$ErrorActionPreference = 'Stop'

$port = $Port
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path.TrimEnd('\')
$projectRootPattern = [regex]::Escape($projectRoot)

$listeners = @(Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort $port -State Listen -ErrorAction SilentlyContinue)
if ($listeners.Count -eq 0) {
    Write-Host "Development port 127.0.0.1:$port is available."
    exit 0
}

foreach ($listener in $listeners) {
    $ownerProcessId = [int]$listener.OwningProcess
    $process = Get-CimInstance Win32_Process -Filter "ProcessId=$ownerProcessId" -ErrorAction SilentlyContinue

    if ($null -eq $process) {
        throw "Port $port is occupied by PID $ownerProcessId, but process details cannot be read."
    }

    $commandLine = [string]$process.CommandLine
    $isProjectVite = ($process.Name -ieq 'node.exe') -and
        ($commandLine -match 'vite[\\/]+bin[\\/]+vite\.js') -and
        ($commandLine -match $projectRootPattern)

    if (-not $isProjectVite) {
        Write-Host "Development port 127.0.0.1:$port is occupied by another program:" -ForegroundColor Yellow
        Write-Host "PID: $ownerProcessId"
        Write-Host "Process: $($process.Name)"
        Write-Host "Command: $commandLine"
        throw "Will not terminate a non-My-LiveHub process. Free the port or set WAILS_VITE_PORT."
    }

    $parentId = [int]$process.ParentProcessId
    $parent = Get-CimInstance Win32_Process -Filter "ProcessId=$parentId" -ErrorAction SilentlyContinue

    if ($null -ne $parent) {
        Write-Host "A My-LiveHub Vite process is still managed by a parent process; refusing duplicate startup." -ForegroundColor Yellow
        Write-Host "Vite PID: $ownerProcessId; parent PID: $parentId; parent process: $($parent.Name)"
        throw "A My-LiveHub development environment may already be running. Close it normally first."
    }

    Write-Host "Found an orphaned My-LiveHub Vite process (PID $ownerProcessId; parent PID $parentId has exited). Cleaning it up..." -ForegroundColor Yellow
    Stop-Process -Id $ownerProcessId -Force

    $released = $false
    for ($attempt = 0; $attempt -lt 30; $attempt++) {
        Start-Sleep -Milliseconds 200
        $remaining = @(Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort $port -State Listen -ErrorAction SilentlyContinue)
        if ($remaining.Count -eq 0) {
            $released = $true
            break
        }
    }

    if (-not $released) {
        throw "The orphaned Vite process was stopped, but port $port is still occupied."
    }
}

Write-Host "Development port 127.0.0.1:$port is free. Wails can start." -ForegroundColor Green

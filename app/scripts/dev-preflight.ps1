param(
    [int]$Port = 8769
)

$ErrorActionPreference = 'Stop'

$port = $Port
$projectRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path.TrimEnd('\')
$projectRootPattern = [regex]::Escape($projectRoot)

$listeners = @(Get-NetTCPConnection -LocalAddress '127.0.0.1' -LocalPort $port -State Listen -ErrorAction SilentlyContinue)
if ($listeners.Count -eq 0) {
    Write-Host "开发端口 127.0.0.1:$port 可用。"
    exit 0
}

foreach ($listener in $listeners) {
    $processId = [int]$listener.OwningProcess
    $process = Get-CimInstance Win32_Process -Filter "ProcessId=$processId" -ErrorAction SilentlyContinue

    if ($null -eq $process) {
        throw "端口 $port 被 PID $processId 占用，但无法读取进程信息。请先确认占用者。"
    }

    $commandLine = [string]$process.CommandLine
    $isProjectVite = ($process.Name -ieq 'node.exe') -and
        ($commandLine -match 'vite[\\/]+bin[\\/]+vite\.js') -and
        ($commandLine -match $projectRootPattern)

    if (-not $isProjectVite) {
        Write-Host "开发端口 127.0.0.1:$port 已被其他程序占用：" -ForegroundColor Yellow
        Write-Host "PID: $processId"
        Write-Host "进程: $($process.Name)"
        Write-Host "命令: $commandLine"
        throw "不会自动结束非 My-LiveHub 进程。请释放端口，或使用 WAILS_VITE_PORT 指定其他端口。"
    }

    $parentId = [int]$process.ParentProcessId
    $parent = Get-CimInstance Win32_Process -Filter "ProcessId=$parentId" -ErrorAction SilentlyContinue

    if ($null -ne $parent) {
        Write-Host "检测到 My-LiveHub 的 Vite 进程仍有父进程管理，拒绝重复启动。" -ForegroundColor Yellow
        Write-Host "Vite PID: $processId；父进程 PID: $parentId；父进程: $($parent.Name)"
        throw "可能已有一个 My-LiveHub 开发环境正在运行。请先正常关闭它。"
    }

    Write-Host "发现 My-LiveHub 遗留的 Vite 进程（PID $processId，父进程 $parentId 已退出），正在清理……" -ForegroundColor Yellow
    Stop-Process -Id $processId -Force

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
        throw "已结束遗留 Vite 进程，但端口 $port 仍被占用。请重新检查端口占用者。"
    }
}

Write-Host "开发端口 127.0.0.1:$port 已释放，可以启动 Wails。" -ForegroundColor Green

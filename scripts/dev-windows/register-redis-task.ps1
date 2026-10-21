<#
.SYNOPSIS
    在当前 Windows 机器上注册 Redis 的开机自启计划任务。

.DESCRIPTION
    这个脚本把本项目在开发机上踩过的 5 个坑一次性处理好：

      1) 【Host 解析】用 VBS 静默启动，避免每次登录弹黑窗口；
         而且黑窗口一旦被关掉 Redis 就会死。
      2) 【电池】DisallowStartIfOnBatteries / StopIfGoingOnBatteries 默认为 True，
         笔记本用电池时不启动、一拔电源就停 —— 必须关掉。
      3) 【执行时限】默认 ExecutionTimeLimit 是 72 小时，
         Redis 会被任务计划每 3 天强杀一次 —— 改成 PT0S（无限制）。
      4) 【路径】redis.conf 必须用相对路径 + 工作目录，
         绝对 Windows 路径会被 msys2 版 redis 误读为 Unix 路径而启动失败。
      5) 【触发器】原任务触发器是空的（注册时漏了），
         导致重启后 Redis 不会自动起来 —— 显式加 AtLogOn 触发器。

.PARAMETER RedisDir
    Redis 解压目录，默认 C:\Users\<你>\redis

.PARAMETER TaskName
    计划任务名，默认 Redis-IM

.EXAMPLE
    powershell -ExecutionPolicy Bypass -File .\register-redis-task.ps1
#>
[CmdletBinding()]
param(
    [string]$RedisDir = "$env:USERPROFILE\redis",
    [string]$TaskName = 'Redis-IM'
)

$ErrorActionPreference = 'Stop'

function Write-Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Write-Ok($msg)   { Write-Host "    OK  $msg" -ForegroundColor Green }

# ---------- 前置检查 ----------
Write-Step "检查 Redis 安装"

$serverExe = Join-Path $RedisDir 'redis-server.exe'
$cliExe    = Join-Path $RedisDir 'redis-cli.exe'
$confFile  = Join-Path $RedisDir 'redis.conf'

foreach ($f in @($serverExe, $cliExe, $confFile)) {
    if (-not (Test-Path $f)) {
        throw "缺少文件: $f`n请先从 https://github.com/redis-windows/redis-windows/releases 下载并解压到 $RedisDir"
    }
}
Write-Ok "redis-server.exe / redis-cli.exe / redis.conf 都在"

# ---------- 1) 生成静默启动器（规避坑 1 与坑 4）----------
Write-Step "生成静默启动器 start-redis-hidden.vbs"

$vbsPath = Join-Path $RedisDir 'start-redis-hidden.vbs'
$vbs = @"
Dim sh
Set sh = CreateObject("WScript.Shell")
sh.CurrentDirectory = "$RedisDir"
sh.Run """$serverExe"" redis.conf", 0, False
"@
# 必须是 ASCII/ANSI，不能带 BOM（wscript 按 ANSI 读取）
[System.IO.File]::WriteAllText($vbsPath, $vbs, (New-Object System.Text.ASCIIEncoding))
Write-Ok "已写入 $vbsPath"

# ---------- 2) 创建计划任务 ----------
Write-Step "注册计划任务 '$TaskName'"

$action = New-ScheduledTaskAction -Execute 'wscript.exe' -Argument "`"$vbsPath`""

# 坑 5：显式加登录触发器（延迟 20 秒，等系统服务先起来）
$trigger = New-ScheduledTaskTrigger -AtLogOn -User "$env:COMPUTERNAME\$env:USERNAME"
$trigger.Delay = 'PT20S'

$settings = New-ScheduledTaskSettingsSet `
    -AllowStartIfOnBatteries `
    -DontStopIfGoingOnBatteries `
    -ExecutionTimeLimit ([TimeSpan]::Zero) `
    -MultipleInstances IgnoreNew `
    -StartWhenAvailable
$settings.Hidden = $true   # 不在任务计划程序 UI 里碍眼

# 已存在就先删掉（幂等，可重复执行）
if (Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue) {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
    Write-Ok "已移除同名旧任务"
}

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger -Settings $settings | Out-Null
Write-Ok "注册完成"

# ---------- 3) 启动并验证 ----------
Write-Step "启动任务并验证"

# ⚠️ 如果已有 Redis 在跑，必须先停掉，否则这个「验证」是**假的**：
# 新实例会因为 6379 端口被占而启动失败并立即退出，
# 而 redis-cli ping 拿到的是**旧进程**的 PONG —— 脚本显示通过，实际什么都没验证。
# （这个坑是写这个脚本时自己踩到的：改完复跑发现 PID 没变才察觉。）
$existing = @(Get-Process redis-server -ErrorAction SilentlyContinue)
if ($existing.Count -gt 0) {
    Write-Host "    检测到已有 Redis 进程 (PID $($existing[0].Id))，先停掉以确保验证有效" -ForegroundColor Yellow
    Stop-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
    $existing | Stop-Process -Force -ErrorAction SilentlyContinue
    Start-Sleep -Seconds 2
    if (Get-Process redis-server -ErrorAction SilentlyContinue) {
        throw "无法停止已有 Redis 进程，请手动结束后重试"
    }
}

Start-ScheduledTask -TaskName $TaskName
Start-Sleep -Seconds 4

$ping = & $cliExe ping 2>&1
if ($ping -notmatch 'PONG') {
    throw "Redis 未能启动（redis-cli ping 返回: $ping）。请检查 $RedisDir\redis.log"
}
$newPid = (Get-Process redis-server -ErrorAction SilentlyContinue | Select-Object -First 1).Id
Write-Ok "redis-cli ping -> PONG（新进程 PID $newPid）"

# ---------- 4) 打印最终配置（便于核对上面 5 个坑都已规避）----------
Write-Step "最终配置"
$t = Get-ScheduledTask -TaskName $TaskName
$s = $t.Settings
$portLine = "$(& $cliExe info server 2>&1 | Select-String 'tcp_port')"
$port = if ($portLine -match 'tcp_port:(\d+)') { $Matches[1] } else { '?' }

[PSCustomObject]@{
    '动作'              = "$($t.Actions[0].Execute) $($t.Actions[0].Arguments)"
    '触发器'            = ($t.Triggers | ForEach-Object { $_.CimClass.CimClassName }) -join ', '
    'Hidden'            = $s.Hidden
    '用电池也启动'      = -not $s.DisallowStartIfOnBatteries
    '拔电源不停止'      = -not $s.StopIfGoingOnBatteries
    '执行时限'          = $s.ExecutionTimeLimit
    '端口'              = $port
} | Format-List

Write-Host ""
Write-Host "完成。Redis 会在每次登录后 20 秒自动静默启动。" -ForegroundColor Green
Write-Host "请注意：任务触发器是「登录时」，若机器设置开机自动登录则开机即生效；" -ForegroundColor Yellow
Write-Host "      若需开机即启（不登录也启动），必须改成 Windows 服务（需要管理员权限）。" -ForegroundColor Yellow

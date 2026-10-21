# ============================================================================
# 在没有 Linux 服务器的情况下，用本机 Windows 验证 deploy/nginx/im-chat.conf
#
# 为什么需要这个脚本：
#   nginx 配置里那三个 WebSocket 配置项（Upgrade 头 / proxy_read_timeout /
#   proxy_buffering off）光看文档是不够的 —— 必须真的让一条 WS 连接穿过去。
#   这个脚本在本机起一个真 nginx 反代真后端，把三个坑逐个验一遍。
#
# 两个 Windows 特有的坑（脚本已处理）：
#   1) nginx for Windows 用 ANSI 码页处理路径，**不支持中文路径**
#      -> 用目录联接把纯 ASCII 路径 D:\imngx 指向工作区里的真实目录
#   2) nginx 在 Windows 上不自动后台化，前台会阻塞
#      -> 用 Start-Process 启动
#
# 用法（仓库根目录）：
#   powershell -ExecutionPolicy Bypass -File deploy\local\verify-nginx-local.ps1
#   powershell -ExecutionPolicy Bypass -File deploy\local\verify-nginx-local.ps1 -IdleSeconds 70
#
# 前置：
#   - 后端已在 127.0.0.1:8080 运行
#   - nginx for Windows 已解压在 $env:IM_NGINX_DIR（默认 build\tools\nginx-1.26.2）
#
# 注意：这是**本机验证**，证明的是「nginx 配置正确、WS 能穿过去」，
#       不证明云服务器部署（systemd / 域名 / 稳定性）。
# ============================================================================
[CmdletBinding()]
param(
    [string]$NginxDir = '',
    [string]$LinkDir = 'D:\imngx',
    [int]$Port = 80,
    [int]$IdleSeconds = 70,
    [string]$Username = 'qiuxiaoman',
    [string]$Password = 'Test1234'
)

$ErrorActionPreference = 'Stop'
$pass = 0; $fail = 0

# 调用原生命令的辅助函数。
# 为什么需要：本脚本 $ErrorActionPreference = 'Stop'，而原生命令（nginx / cmd）
# 往 stderr 写东西时 PowerShell 会把它当成终止错误。这里的 `2>&1` 合并流
# 会让脚本直接退出。所以调用原生命令时必须临时切回 Continue，
# 再用 $LASTEXITCODE 判断结果。
function Invoke-Native {
    param([scriptblock]$Block)
    $prev = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { $out = & $Block 2>&1 } finally { $ErrorActionPreference = $prev }
    return @{ exit = $LASTEXITCODE; output = $out }
}
function Check($name, $cond, $detail) {
    if ($cond) { $script:pass++; Write-Host "  [PASS] $name" -ForegroundColor Green }
    else { $script:fail++; Write-Host "  [FAIL] $name -> $detail" -ForegroundColor Red }
}

$scriptDir = Split-Path -Parent $PSCommandPath
$repo = Resolve-Path (Join-Path $scriptDir '..\..')
# 本机 nginx-for-Windows 的位置因机器而异，按优先级探测几个常见位置：
#   1) 显式传入 -NginxDir
#   2) 仓库旁的 build\tools（本项目开发时的约定）
#   3) 仓库内的 build\tools
if (-not $NginxDir) {
    $candidates = @(
        (Join-Path (Split-Path $repo -Parent) 'build\tools\nginx-1.26.2'),
        (Join-Path $repo 'build\tools\nginx-1.26.2'),
        'C:\nginx',
        'D:\nginx'
    )
    foreach ($cand in $candidates) {
        if (Test-Path (Join-Path $cand 'nginx.exe')) { $NginxDir = $cand; break }
    }
    if (-not $NginxDir) { $NginxDir = $candidates[0] }
}

Write-Host "`n=== 0) 前置检查 ===" -ForegroundColor Cyan
Check "nginx 存在" (Test-Path (Join-Path $NginxDir 'nginx.exe')) $NginxDir
Check "仓库 nginx 配置存在" (Test-Path (Join-Path $repo 'deploy\nginx\im-chat.conf')) 'missing'
Check "前端产物存在" (Test-Path (Join-Path $repo 'web\dist\index.html')) 'run vite build first'
$backendUp = Test-NetConnection -ComputerName 127.0.0.1 -Port 8080 -InformationLevel Quiet -WarningAction SilentlyContinue
Check "后端 8080 在线" $backendUp 'start the Go server first'
if ($fail -gt 0) { Write-Host "`n前置条件不满足，退出" -ForegroundColor Red; exit 1 }

$nginxExe = Join-Path $NginxDir 'nginx.exe'

# ---------------------------------------------------------------------------
Write-Host "`n=== 1) 准备 ASCII 路径（nginx 不支持中文路径）===" -ForegroundColor Cyan
$realDir = Join-Path $repo 'build\nginx-run'
if (Test-Path $realDir) { Remove-Item $realDir -Recurse -Force }
foreach ($d in @('conf', 'logs', 'temp', 'web')) {
    New-Item -ItemType Directory -Force -Path (Join-Path $realDir $d) | Out-Null
}
Copy-Item (Join-Path $repo 'web\dist\*') (Join-Path $realDir 'web') -Recurse -Force
Copy-Item (Join-Path $NginxDir 'conf\mime.types') (Join-Path $realDir 'conf\mime.types') -Force
Check "前端已拷到 ASCII 目录" (Test-Path (Join-Path $realDir 'web\index.html')) 'copy failed'

if (Test-Path $LinkDir) { $null = Invoke-Native { cmd /c rmdir $LinkDir } }
$mkRes = Invoke-Native { cmd /c mklink /J $LinkDir $realDir }; $mk = ($mkRes.output -join ' ')
Check "目录联接创建成功" ($mk -match 'Junction created') $mk

# ---------------------------------------------------------------------------
Write-Host "`n=== 2) 用仓库里的真配置生成 nginx.conf（只替换 root 与 listen）===" -ForegroundColor Cyan
$site = [System.IO.File]::ReadAllText((Join-Path $repo 'deploy\nginx\im-chat.conf'), [System.Text.Encoding]::UTF8)
# 只改两处：静态根目录、监听端口。其余（含 WebSocket 三件套）保持原样，这才是被测对象。
$site = $site.Replace('root /opt/im-chat/web;', 'root ' + ($LinkDir -replace '\\', '/') + '/web;')
$site = $site.Replace('listen 80;', "listen $Port;")
$site = $site.Replace('    listen [::]:80;' + "`r`n", '').Replace('    listen [::]:80;' + "`n", '')
[System.IO.File]::WriteAllText((Join-Path $realDir 'conf\im-chat.conf'), $site, (New-Object System.Text.UTF8Encoding($false)))

$main = @"
worker_processes 1;
error_log logs/error.log warn;
pid logs/nginx.pid;
events { worker_connections 1024; }
http {
    include mime.types;
    default_type application/octet-stream;
    access_log logs/access.log;
    sendfile on;
    keepalive_timeout 65;
    client_body_temp_path temp/client_body;
    proxy_temp_path temp/proxy;
    fastcgi_temp_path temp/fastcgi;
    uwsgi_temp_path temp/uwsgi;
    scgi_temp_path temp/scgi;
    include im-chat.conf;
}
"@
[System.IO.File]::WriteAllText((Join-Path $realDir 'conf\nginx.conf'), $main, (New-Object System.Text.ASCIIEncoding))

$tRes = Invoke-Native { & $nginxExe -t -p $LinkDir -c "$LinkDir\conf\nginx.conf" }
Check "nginx -t 配置语法通过（退出码 0）" ($tRes.exit -eq 0) ($tRes.output -join ' | ')

# ---------------------------------------------------------------------------
Write-Host "`n=== 3) 启动 nginx ===" -ForegroundColor Cyan
$pidFile = Join-Path $realDir 'logs\nginx.pid'
if (Test-Path $pidFile) {
    $ownPid = (Get-Content $pidFile -ErrorAction SilentlyContinue | Select-Object -First 1)
    if ($ownPid -and $ownPid.Trim() -match '^\d+$') {
        Stop-Process -Id ([int]$ownPid.Trim()) -Force -ErrorAction SilentlyContinue
    }
}
# 本脚本只记录并停止自己启动的那个（通过 nginx.pid）。
$nginxProc = Start-Process -FilePath $nginxExe -ArgumentList '-p', $LinkDir, '-c', "$LinkDir\conf\nginx.conf" -WindowStyle Hidden -PassThru
Start-Sleep -Seconds 2
Check "端口 $Port 监听中" (Test-NetConnection -ComputerName 127.0.0.1 -Port $Port -InformationLevel Quiet -WarningAction SilentlyContinue) 'not listening'

$base = if ($Port -eq 80) { 'http://127.0.0.1' } else { "http://127.0.0.1:$Port" }

# ---------------------------------------------------------------------------
Write-Host "`n=== 4) HTTP：静态文件 / API 代理 / SPA 回落 ===" -ForegroundColor Cyan
try {
    $r = Invoke-WebRequest -Uri "$base/" -TimeoutSec 10 -UseBasicParsing
    Check "GET / 返回前端页面" ($r.StatusCode -eq 200 -and $r.Content -match '<div id="app">') "status=$($r.StatusCode)"
} catch { Check "GET / 返回前端页面" $false $_.Exception.Message }

$tok = $null
try {
    $lr = Invoke-RestMethod -Uri "$base/api/v1/auth/login" -Method Post `
        -Body ([System.Text.Encoding]::UTF8.GetBytes((@{ username = $Username; password = $Password } | ConvertTo-Json -Compress))) `
        -ContentType 'application/json' -TimeoutSec 15
    Check "POST /api/v1/auth/login 代理生效" ($lr.code -eq 0) "code=$($lr.code)"
    $tok = $lr.data.accessToken
} catch { Check "POST /api/v1/auth/login 代理生效" $false $_.Exception.Message }

try {
    $h = Invoke-RestMethod -Uri "$base/healthz" -TimeoutSec 8
    Check "GET /healthz 代理生效" ($h.data.status -eq 'ok') "status=$($h.data.status)"
} catch { Check "GET /healthz 代理生效" $false $_.Exception.Message }

try {
    $r2 = Invoke-WebRequest -Uri "$base/chat" -TimeoutSec 10 -UseBasicParsing
    Check "GET /chat SPA 回落（try_files）" ($r2.StatusCode -eq 200) "status=$($r2.StatusCode)"
} catch { Check "GET /chat SPA 回落（try_files）" $false $_.Exception.Message }

# ---------------------------------------------------------------------------
Write-Host "`n=== 5) 坑 #1：WebSocket 握手穿过 nginx（Upgrade 头）===" -ForegroundColor Cyan
$ws = New-Object System.Net.WebSockets.ClientWebSocket
$ct = [System.Threading.CancellationToken]::None
$wsOk = $false
if ($tok) {
    try {
        $ws.ConnectAsync([Uri]"ws://127.0.0.1:$Port/ws?token=$tok", $ct).Wait(12000) | Out-Null
        $wsOk = ($ws.State -eq 'Open')
    } catch { $wsOk = $false }
}
Check "WS 握手返回 Open（缺 Upgrade 头会 400）" $wsOk "state=$($ws.State)"

function SendRecvAck($conn, $tk, $convId, $tag) {
    $cid = [guid]::NewGuid().ToString()
    $payload = '{"type":"send","reqId":"' + $cid + '","data":{"clientMsgId":"' + $cid + '","conversationId":' + $convId + ',"contentType":1,"content":"nginx-verify ' + $tag + '"}}'
    $b = [System.Text.Encoding]::UTF8.GetBytes($payload)
    $conn.SendAsync([System.ArraySegment[byte]]::new($b), [System.Net.WebSockets.WebSocketMessageType]::Text, $true, $tk).Wait(10000) | Out-Null
    $buf = New-Object byte[] 16384
    for ($i = 0; $i -lt 30; $i++) {
        $rt = $conn.ReceiveAsync([System.ArraySegment[byte]]::new($buf), $tk)
        if (-not $rt.Wait(10000)) { return $null }
        if ($rt.IsFaulted) { return $null }
        try { $env = [System.Text.Encoding]::UTF8.GetString($buf, 0, $rt.Result.Count) | ConvertFrom-Json } catch { continue }
        if ($env.type -eq 'send_ack') { return $env }
    }
    return $null
}

# 取一个可发消息的会话
$convId = 0
if ($tok) {
    try {
        $c = Invoke-RestMethod -Uri "$base/api/v1/conversations" -Headers @{ Authorization = "Bearer $tok" } -TimeoutSec 10
        if ($c.data.items.Count -gt 0) { $convId = $c.data.items[0].id }
    } catch {}
}
Check "拿到一个会话用于发消息" ($convId -gt 0) 'no conversation'

# ---------------------------------------------------------------------------
Write-Host "`n=== 6) 坑 #3：消息收发（缺 proxy_buffering off 会明显延迟）===" -ForegroundColor Cyan
if ($wsOk -and $convId -gt 0) {
    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    $ack = SendRecvAck $ws $ct $convId 'first'
    $sw.Stop()
    Check "send/ack 往返成功（$($sw.ElapsedMilliseconds) ms）" ($null -ne $ack) 'timeout'
} else {
    Check "send/ack 往返成功" $false 'ws or conversation unavailable'
}

# ---------------------------------------------------------------------------
Write-Host "`n=== 7) 坑 #2：空闲 $IdleSeconds 秒不断（默认 60s 会静默断开）===" -ForegroundColor Cyan
if ($wsOk) {
    Write-Host "  保持空闲 $IdleSeconds 秒（这是验证 proxy_read_timeout 的关键）..."
    Start-Sleep -Seconds $IdleSeconds
    Check "空闲 ${IdleSeconds}s 后连接仍 Open" ($ws.State -eq 'Open') "state=$($ws.State)"
    $ack2 = SendRecvAck $ws $ct $convId 'after-idle'
    Check "空闲后仍能收发" ($null -ne $ack2) 'no ack after idle'
}

try { $ws.CloseAsync([System.Net.WebSockets.WebSocketCloseStatus]::NormalClosure, 'done', $ct).Wait(3000) | Out-Null } catch {}

# ---------------------------------------------------------------------------
Write-Host "`n=== 8) nginx 访问日志里的 101 证据 ===" -ForegroundColor Cyan
$log = Join-Path $realDir 'logs\access.log'
if (Test-Path $log) {
    $line101 = Get-Content $log | Where-Object { $_ -match '" 101 ' } | Select-Object -Last 1
    Check "access.log 里有 101 Switching Protocols" ($null -ne $line101) 'no 101 in log'
    if ($line101) { Write-Host ("    " + ($line101 -replace 'token=[^& ]+', 'token=***')) -ForegroundColor DarkGray }
}

# ---------------------------------------------------------------------------
Write-Host "`n=== 9) 停止 nginx 并清理 ===" -ForegroundColor Cyan
$pidFile = Join-Path $realDir 'logs\nginx.pid'
if (Test-Path $pidFile) {
    $ownPid = (Get-Content $pidFile -ErrorAction SilentlyContinue | Select-Object -First 1)
    if ($ownPid -and $ownPid.Trim() -match '^\d+$') {
        Stop-Process -Id ([int]$ownPid.Trim()) -Force -ErrorAction SilentlyContinue
    }
}
# 本脚本只记录并停止自己启动的那个（通过 nginx.pid）。
Write-Host "  已停止 nginx，已删除联接 $LinkDir（日志留在 build\nginx-run\logs）" -ForegroundColor DarkGray

Write-Host ""
Write-Host ("=" * 64) -ForegroundColor Cyan
Write-Host ("  PASS: $pass    FAIL: $fail") -ForegroundColor $(if ($fail -eq 0) { 'Green' } else { 'Red' })
Write-Host ("=" * 64) -ForegroundColor Cyan
Write-Host "  说明：这验证的是『nginx 配置正确、WS 能穿过去』。" -ForegroundColor Yellow
Write-Host "        不证明云服务器部署（systemd / 域名 / 稳定性）。" -ForegroundColor Yellow

exit $fail

# ============================================================================
# Local release builder: cross-compile the server for Linux, build the frontend,
# and package everything into one tarball ready to scp to the server.
#
# Usage (from the repo root, on Windows):
#   .\deploy\local\release.ps1
#   .\deploy\local\release.ps1 -SkipFrontend      # backend only (faster)
#
# Output: build/release/im-chat-<version>.tar.gz
#
# NOTE: keep this file 100% ASCII. Windows PowerShell reads a BOM-less UTF-8
# script as ANSI; non-ASCII bytes in comments have silently broken scripts in
# this project before.
# ============================================================================
[CmdletBinding()]
param(
    [switch]$SkipFrontend,
    [string]$OutputDir = "build\release"
)

$ErrorActionPreference = 'Stop'

function Step($msg) { Write-Host "`n==> $msg" -ForegroundColor Cyan }
function Ok($msg)   { Write-Host "  OK  $msg" -ForegroundColor Green }
function Die($msg)  { Write-Host "  ERR $msg" -ForegroundColor Red; exit 1 }

# ---------------------------------------------------------------------------
Step "0) locate repo root"
$scriptDir = Split-Path -Parent $PSCommandPath
$repoRoot = Resolve-Path (Join-Path $scriptDir '..\..')
Set-Location $repoRoot
Ok "repo root: $repoRoot"

$serverDir = Join-Path $repoRoot 'server'
$webDir = Join-Path $repoRoot 'web'
$outDir = Join-Path $repoRoot $OutputDir
New-Item -ItemType Directory -Force -Path $outDir | Out-Null

# ---------------------------------------------------------------------------
Step "1) sanity checks"
if (git status --porcelain) {
    Write-Host "  WARN working tree is not clean - release will include uncommitted changes" -ForegroundColor Yellow
}
$version = (git describe --tags --always 2>$null)
if (-not $version) { $version = 'dev' }
Ok "version: $version"

# ---------------------------------------------------------------------------
Step "2) cross-compile server for linux/amd64 (static, no glibc dependency)"
$goBin = 'C:\Users\qm\go-sdk\go\bin'
if (Test-Path $goBin) { $env:Path = "$goBin;$env:Path" }

Push-Location $serverDir
try {
    $oldGoos = $env:GOOS; $oldGoarch = $env:GOARCH; $oldCgo = $env:CGO_ENABLED
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'

    # -trimpath  : strip local paths (reproducible build)
    # -s -w      : drop debug symbols (~30% smaller)
    # -X main.version : bake the git tag into the binary so /healthz reports it
    $ldflags = "-s -w -X main.version=$version"
    & go build -trimpath -ldflags $ldflags -o bin/im-server-linux ./cmd/server
    if ($LASTEXITCODE -ne 0) { Die "go build failed" }
    $env:GOOS = $oldGoos; $env:GOARCH = $oldGoarch; $env:CGO_ENABLED = $oldCgo
} finally {
    Pop-Location
}

$binPath = Join-Path $serverDir 'bin\im-server-linux'
if (-not (Test-Path $binPath)) { Die "binary not produced" }
$binSizeMB = [math]::Round((Get-Item $binPath).Length / 1MB, 2)
Ok "im-server-linux  $binSizeMB MB"

# Verify it is really a Linux ELF binary, not a Windows PE.
$bytes = [System.IO.File]::ReadAllBytes($binPath)
# ELF magic is 0x7F 'E' 'L' 'F'. Compare raw bytes: PowerShell 5.1 does not
# support the `u{7F} escape (that is PowerShell 6+), so string comparison here
# would silently never match.
if ($bytes[0] -ne 0x7F -or $bytes[1] -ne 0x45 -or $bytes[2] -ne 0x4C -or $bytes[3] -ne 0x46) {
    Die ("not an ELF binary (first bytes = " + ($bytes[0..3] -join ',') + ")")
}
$machine = [BitConverter]::ToUInt16($bytes, 18)
$machineName = switch ($machine) { 0x3E { 'x86-64' } 0xB7 { 'aarch64' } default { "0x$($machine.ToString('X'))" } }
Ok "ELF magic OK, machine = $machineName"
if ($machine -ne 0x3E) { Die "expected x86-64 (0x3E), got $machineName" }

# ---------------------------------------------------------------------------
Step "3) build frontend"
$webDist = Join-Path $webDir 'dist'
if ($SkipFrontend) {
    Write-Host "  skipped (-SkipFrontend)" -ForegroundColor Yellow
    if (-not (Test-Path $webDist)) { Die "web/dist not found and -SkipFrontend was used" }
} else {
    Push-Location $webDir
    try {
        # esbuild cannot delete files in the system temp dir on this machine
        # ("Access is denied"); pointing TEMP into the repo fixes the build.
        $localTmp = Join-Path $repoRoot '.tmp'
        New-Item -ItemType Directory -Force -Path $localTmp | Out-Null
        $oldT = $env:TEMP; $oldTmp = $env:TMP
        $env:TEMP = $localTmp; $env:TMP = $localTmp
        try {
            & npx --no-install vue-tsc --noEmit
            if ($LASTEXITCODE -ne 0) { Die "type-check failed" }
            Ok "type-check passed"
            & npx --no-install vite build
            if ($LASTEXITCODE -ne 0) { Die "vite build failed" }
        } finally {
            $env:TEMP = $oldT; $env:TMP = $oldTmp
        }
    } finally {
        Pop-Location
    }
    Ok "web/dist built"
}
if (-not (Test-Path (Join-Path $webDist 'index.html'))) { Die "web/dist/index.html missing" }

# ---------------------------------------------------------------------------
Step "4) stage the release"
$stage = Join-Path $outDir 'stage'
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
New-Item -ItemType Directory -Force -Path $stage | Out-Null

Copy-Item $binPath (Join-Path $stage 'im-server-linux')
Copy-Item -Recurse (Join-Path $webDist '*') (Join-Path $stage 'web')
Copy-Item -Recurse (Join-Path $repoRoot 'deploy\systemd') (Join-Path $stage 'systemd')
Copy-Item -Recurse (Join-Path $repoRoot 'deploy\nginx')   (Join-Path $stage 'nginx')
# SECURITY: only top-level .sql files. server/migrations/local/ holds the real
# DB password for local dev (it is gitignored) and must NEVER ship to a server.
$migSrc = Join-Path $repoRoot 'server\migrations'
$migDst = Join-Path $stage 'migrations'
New-Item -ItemType Directory -Force -Path $migDst | Out-Null
Get-ChildItem $migSrc -File -Filter '*.sql' | ForEach-Object { Copy-Item $_.FullName (Join-Path $migDst $_.Name) }
Copy-Item -Recurse (Join-Path $repoRoot 'ai') (Join-Path $stage 'ai') -Exclude @('.venv','__pycache__','.pytest_cache')
Copy-Item (Join-Path $repoRoot 'deploy\scripts\deploy.sh') (Join-Path $stage 'deploy.sh')

# do not ship the local venv / caches if -Exclude above missed a nested one
Get-ChildItem $stage -Recurse -Directory -Force |
    Where-Object { $_.Name -in @('.venv', '__pycache__', '.pytest_cache', 'node_modules') } |
    ForEach-Object { Remove-Item $_.FullName -Recurse -Force }

$fileCount = (Get-ChildItem $stage -Recurse -File).Count
Ok "staged $fileCount files"

# SECURITY GATE: refuse to package anything that looks like a real secret.
# This has already caught one real leak (migrations/local/*.sql contains the
# actual local DB password and is only protected by .gitignore).
Step "4.5) secret scan"
$textExt = @('.sql', '.env', '.md', '.txt', '.json', '.ps1', '.sh', '.conf', '.service', '.example', '.py')
$leaks = @()
foreach ($f in Get-ChildItem $stage -Recurse -File) {
    if ($f.Length -gt 2MB) { continue }
    if ($f.Name -match '\.local\.sql$') { $leaks += "$($f.Name) (local-only migration)"; continue }
    if ($textExt -notcontains $f.Extension -and $f.Extension -ne '.example') { continue }
    $t = [System.IO.File]::ReadAllText($f.FullName)
    if ($t -match "IDENTIFIED BY\s+'[^']{5,}'" -and $t -notmatch 'CHANGE_ME') {
        $leaks += "$($f.Name) (contains a hard-coded password)"
    }
    if ($t -match 'LLM_API_KEY\s*=\s*sk-') {
        $leaks += "$($f.Name) (contains an LLM API key)"
    }
}
if ($leaks.Count -gt 0) {
    foreach ($l in $leaks) { Write-Host "  LEAK $l" -ForegroundColor Red }
    Die "refusing to package: staged files contain secrets"
}
Ok "no hard-coded secrets in the package"

# ---------------------------------------------------------------------------
Step "5) package"
$tarName = "im-chat-$version.tar.gz"
$tarPath = Join-Path $outDir $tarName
if (Test-Path $tarPath) { Remove-Item $tarPath -Force }
Push-Location $stage
try {
    & tar -czf $tarPath .
    if ($LASTEXITCODE -ne 0) { Die "tar failed (is bsdtar available? Windows 10+ ships it)" }
} finally {
    Pop-Location
}
$tarSizeMB = [math]::Round((Get-Item $tarPath).Length / 1MB, 2)
Ok "$tarPath  $tarSizeMB MB"

# ---------------------------------------------------------------------------
Step "6) verify the tarball actually contains what we expect"
$listing = & tar -tzf $tarPath
$required = @('./im-server-linux', './web/index.html', './systemd/im-chat.service', './nginx/im-chat.conf', './deploy.sh')
foreach ($r in $required) {
    if ($listing -contains $r) { Ok "contains $r" } else { Die "tarball missing $r" }
}
if ($listing | Where-Object { $_ -match '\.venv|node_modules|__pycache__|\.env$|migrations/local|\.local\.sql' }) {
    Die "tarball contains files that must not ship (venv / node_modules / .env / local migrations)"
}
Ok "no .env, no venv, no node_modules inside"

Write-Host ""
Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "  Release ready" -ForegroundColor Green
Write-Host "============================================================" -ForegroundColor Cyan
Write-Host "  version : $version"
Write-Host "  package : $tarPath ($tarSizeMB MB)"
Write-Host ""
Write-Host "  Next:"
Write-Host "    scp `"$tarPath`" deploy@<SERVER_IP>:/tmp/"
Write-Host "    ssh deploy@<SERVER_IP> `"sudo bash /opt/im-chat/deploy.sh /tmp/$tarName`""
Write-Host "============================================================" -ForegroundColor Cyan

exit 0

[CmdletBinding()]
param(
    [ValidatePattern('^[A-Za-z]:$')]
    [string]$Mount = "S:",
    [string]$Pipe = "\\.\pipe\huadian-drive-release",
    [int]$MountTimeoutSeconds = 15
)

$ErrorActionPreference = "Stop"

$Release = Split-Path -Parent $PSScriptRoot
$Bin = Join-Path $Release "bin"
$Logs = Join-Path $Release "logs"
$Hddctl = Join-Path $Bin "hddctl.exe"
$Daemon = Join-Path $Bin "hddsyncd.exe"
$Hddfs = Join-Path $Bin "hddfs.exe"

foreach ($file in @($Hddctl, $Daemon, $Hddfs)) {
    if (-not (Test-Path -LiteralPath $file -PathType Leaf)) {
        throw "Missing executable: $file"
    }
}

if (Test-Path "$Mount\") {
    throw "Drive $Mount is already in use."
}

$winfspPaths = @(
    "$env:ProgramFiles\WinFsp\bin\winfsp-x64.dll",
    "${env:ProgramFiles(x86)}\WinFsp\bin\winfsp-x64.dll"
)
if (-not ($winfspPaths | Where-Object { Test-Path -LiteralPath $_ })) {
    throw "WinFsp was not detected. Install WinFsp before continuing."
}

New-Item -ItemType Directory -Force -Path $Logs | Out-Null

Write-Host "Checking Huadian Drive authentication..." -ForegroundColor Cyan
& $Hddctl remote ls /
if ($LASTEXITCODE -ne 0) {
    Write-Host "Authentication is unavailable. Starting login..." -ForegroundColor Yellow
    & $Hddctl login
    if ($LASTEXITCODE -ne 0) {
        throw "hddctl login failed."
    }

    Write-Host "Checking authentication again..." -ForegroundColor Cyan
    & $Hddctl remote ls /
    if ($LASTEXITCODE -ne 0) {
        throw "remote ls / still failed after login."
    }
}

Write-Host "Authentication check passed." -ForegroundColor Green

$daemonWindow = Start-Process `
    -FilePath $Daemon `
    -ArgumentList @("run", "--provider", "huadian", "--pipe", $Pipe, "--mkdir-rename-move-file-rename-move-remove-copy-upload-only", "--no-background") `
    -WorkingDirectory $Release `
    -RedirectStandardOutput (Join-Path $Logs "daemon.stdout.log") `
    -RedirectStandardError (Join-Path $Logs "daemon.stderr.log") `
    -WindowStyle Hidden `
    -PassThru

Start-Sleep -Seconds 2
if ($daemonWindow.HasExited) {
    Get-Content -LiteralPath (Join-Path $Logs "daemon.stderr.log") -ErrorAction SilentlyContinue
    throw "hddsyncd exited unexpectedly."
}

$debugLog = Join-Path $Logs "hddfs.debug.log"
$mountWindow = Start-Process `
    -FilePath $Hddfs `
    -ArgumentList @("mount", "--daemon", "--pipe", $Pipe, "--mount", $Mount, "--mkdir-rename-move-file-rename-move-remove-copy-upload-only", "--debug-log", $debugLog) `
    -WorkingDirectory $Release `
    -RedirectStandardOutput (Join-Path $Logs "hddfs.stdout.log") `
    -RedirectStandardError (Join-Path $Logs "hddfs.stderr.log") `
    -WindowStyle Hidden `
    -PassThru

$deadline = (Get-Date).AddSeconds($MountTimeoutSeconds)
while (-not (Test-Path "$Mount\") -and (Get-Date) -lt $deadline) {
    if ($mountWindow.HasExited) {
        break
    }
    Start-Sleep -Milliseconds 250
}

if (-not (Test-Path "$Mount\")) {
    Get-Content -LiteralPath (Join-Path $Logs "hddfs.stderr.log") -ErrorAction SilentlyContinue
    throw "Mount failed or timed out after $MountTimeoutSeconds seconds. Check the files in release\logs."
}

Write-Host "Mounted successfully: $Mount" -ForegroundColor Green
Start-Process -FilePath "explorer.exe" -ArgumentList "$Mount\"

$stopCommand = "Get-Process hddfs,hddsyncd -ErrorAction SilentlyContinue | Stop-Process -Force"
Write-Host ""
Write-Host $stopCommand -ForegroundColor Cyan

while ($true) {
    $inputCommand = Read-Host
    if ([string]::IsNullOrWhiteSpace($inputCommand)) {
        Write-Host $stopCommand -ForegroundColor Cyan
        continue
    }
    if ($inputCommand.Trim() -eq $stopCommand) {
        Get-Process hddfs,hddsyncd -ErrorAction SilentlyContinue | Stop-Process -Force
        break
    }
    Write-Host $stopCommand -ForegroundColor Cyan
}

while ($true) {
    $closeCommand = Read-Host "Processes stopped. Type EXIT to close this controller"
    if ($closeCommand.Trim() -ieq "EXIT") {
        break
    }
}

# Stop test command (run manually after all file operations have finished):
# Get-Process hddfs,hddsyncd -ErrorAction SilentlyContinue | Stop-Process -Force

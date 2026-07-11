# mount-memfs.ps1
# Build and mount the Huadian Drive MemFS demo.
#
# Usage:
#   .\scripts\mount-memfs.ps1              (mounts at H:)
#   .\scripts\mount-memfs.ps1 -Drive Z:    (mounts at Z:)
#
# Prerequisites:
#   - WinFsp must be installed (https://winfsp.dev)
#   - Go 1.24+ must be available
#   - MinGW gcc must be in PATH (or set $env:CC)

param(
    [string]$Drive = "H:"
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot\..

# Environment for CGo
$env:PATH = "C:\Go\bin;$PSScriptRoot\..\.tools\mingw64\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
if (-not $env:CC) { $env:CC = "gcc" }
$env:CGO_CFLAGS = "-I$PSScriptRoot\..\.tools\mingw64\mingw64\usr\local\include\winfsp"

Write-Host "=== Building hddfs ==="
go build -o hddfs.exe ./cmd/hddfs
Write-Host "Build OK"

Write-Host "`n=== Mounting MemFS at ${Drive} ==="
.\hddfs.exe memfs --mount $Drive

# Build script for Huadian Drive.
param(
    [switch]$Release
)

$ErrorActionPreference = "Stop"
$projectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location (Split-Path -Parent $projectRoot)

$go = "C:\Go\bin\go.exe"
if (-not (Test-Path $go)) {
    $go = (Get-Command go -ErrorAction SilentlyContinue).Source
}
if (-not $go) {
    Write-Error "go not found"
    exit 1
}

$ldflags = ""
if ($Release) {
    $ldflags = "-s -w"
}

# Detect C compiler for race detector.
$gcc = $null
$gccCandidates = @(
    "D:\ncepupan\.tools\mingw64\mingw64\bin\gcc.exe",
    (Get-Command gcc -ErrorAction SilentlyContinue).Source
)
foreach ($c in $gccCandidates) {
    if ($c -and (Test-Path $c)) {
        $gcc = $c
        break
    }
}

$raceOk = $false
if ($gcc) {
    $tmpC = "$env:TEMP\_hdd_race_check.c"
    "int main(){return 0;}" | Out-File -FilePath $tmpC -Encoding ASCII
    & $gcc -x c -o "$env:TEMP\_hdd_race_check.exe" $tmpC 2>&1 | Out-Null
    if ($LASTEXITCODE -eq 0) {
        $raceOk = $true
    }
    Remove-Item $tmpC, "$env:TEMP\_hdd_race_check.exe" -ErrorAction SilentlyContinue
}

Write-Host "=== gofmt ==="
& $go fmt ./... 2>&1

Write-Host "=== go vet ==="
& $go vet ./... 2>&1

Write-Host "=== go test ==="
& $go test ./... 2>&1

if ($raceOk) {
    Write-Host "=== go test -race ==="
    $env:CGO_ENABLED = "1"
    & $go test -race ./... 2>&1
} else {
    Write-Host "=== go test -race SKIPPED (no working C compiler) ==="
}

$outDir = "bin"
if (-not (Test-Path $outDir)) {
    New-Item -ItemType Directory -Path $outDir | Out-Null
}

Write-Host "=== go build hddctl ==="
& $go build -ldflags $ldflags -o "$outDir\hddctl.exe" ./cmd/hddctl 2>&1

Write-Host "=== go build hddsyncd ==="
& $go build -ldflags $ldflags -o "$outDir\hddsyncd.exe" ./cmd/hddsyncd 2>&1

Write-Host "Build complete."

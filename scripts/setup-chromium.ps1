# setup-chromium.ps1
# Downloads Chromium Portable to .tools/chromium/
#
# Sources (try in order):
#   1. https://download-chromium.appspot.com/ (official, may need proxy)
#   2. https://npmmirror.com (Chinese mirror for chromium-browser-snapshots)
#   3. Manual: download from https://github.com/Hibbiki/chromium-win64
#
# After download, extract chrome-win.zip to .tools\chromium\chrome-win\

param(
    [switch]$Force
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot\..

$toolsDir = ".tools\chromium"
$zipFile = "$toolsDir\chrome-win.zip"
$chromeDir = "$toolsDir\chrome-win"

if (Test-Path "$chromeDir\chrome.exe" -and -not $Force) {
    Write-Host "Chromium already installed at $chromeDir"
    Write-Host "Use -Force to re-download."
    exit 0
}

New-Item -ItemType Directory -Force $toolsDir | Out-Null

# Fetch latest revision from API
Write-Host "Fetching latest Chromium revision..."
try {
    $rev = Invoke-WebRequest -Uri "https://www.googleapis.com/download/storage/v1/b/chromium-browser-snapshots/o/Win_x64%2FLAST_CHANGE?alt=media" -UseBasicParsing -TimeoutSec 30
    $rev = $rev.Content.Trim()
    Write-Host "Latest revision: $rev"
} catch {
    Write-Host "ERROR: Cannot reach Chromium API. Please check network."
    Write-Host ""
    Write-Host "Manual download:"
    Write-Host "  1. Visit https://github.com/Hibbiki/chromium-win64/releases"
    Write-Host "  2. Download chrome-win.zip"
    Write-Host "  3. Extract to: $chromeDir"
    exit 1
}

$downloadURL = "https://www.googleapis.com/download/storage/v1/b/chromium-browser-snapshots/o/Win_x64%2F${rev}%2Fchrome-win.zip?alt=media"

Write-Host "Downloading Chromium (~150MB)..."
Write-Host "URL: $downloadURL"

try {
    Invoke-WebRequest -Uri $downloadURL -OutFile $zipFile -UseBasicParsing -TimeoutSec 600
} catch {
    Write-Host "ERROR: Download failed. $_"
    Write-Host "Try manual download from https://github.com/Hibbiki/chromium-win64/releases"
    exit 1
}

Write-Host "Extracting..."
Expand-Archive -Path $zipFile -DestinationPath $toolsDir -Force

if (Test-Path "$chromeDir\chrome.exe") {
    Write-Host "Chromium installed at $chromeDir"
    Remove-Item $zipFile
} else {
    Write-Host "WARNING: Extraction complete but chrome.exe not found."
    Write-Host "Check: $chromeDir"
}

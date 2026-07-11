# unmount.ps1
# Unmount the Huadian Drive MemFS.
#
# Usage:
#   .\scripts\unmount.ps1              (unmounts H:)
#   .\scripts\unmount.ps1 -Drive Z:    (unmounts Z:)

param(
    [string]$Drive = "H:"
)

$ErrorActionPreference = "Stop"

Write-Host "Unmounting $Drive ..."
try {
    $result = & fsptool-x64.exe unmount $Drive 2>&1
    Write-Host $result
    Write-Host "Unmount OK"
} catch {
    Write-Host "Unmount failed: $_"
    Write-Host "You can also unmount via:"
    Write-Host "  net use $Drive /delete"
    Write-Host "  or press Ctrl+C in the hddfs process"
    exit 1
}

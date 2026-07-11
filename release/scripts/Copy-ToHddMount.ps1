param(
    [Parameter(Mandatory=$true)][string]$Source,
    [Parameter(Mandatory=$true)][string]$Destination,
    [ValidateSet("Prompt","Fail","Overwrite","AutoRename")]
    [string]$OnConflict = "Prompt"
)

$ErrorActionPreference = "Stop"
if (-not (Test-Path -LiteralPath $Source -PathType Leaf)) {
    throw "Source must be a regular file: $Source"
}

if (Test-Path -LiteralPath $Destination) {
    if ($OnConflict -eq "Overwrite") {
        throw "Overwrite is not supported by this release."
    }
    if ($OnConflict -eq "Fail") {
        throw "Destination already exists: $Destination"
    }
    if ($OnConflict -eq "Prompt") {
        $choice = Read-Host "Destination exists. Enter S to skip or A to auto-rename"
        if ($choice -notmatch '^[Aa]$') {
            Write-Host "Skipped."
            return
        }
    }
    $dir = Split-Path $Destination -Parent
    $base = [IO.Path]::GetFileNameWithoutExtension($Destination)
    $ext = [IO.Path]::GetExtension($Destination)
    $n = 1
    do {
        $Destination = Join-Path $dir "$base ($n)$ext"
        $n++
    } while (Test-Path -LiteralPath $Destination)
}

Copy-Item -LiteralPath $Source -Destination $Destination -ErrorAction Stop
$sourceHash = (Get-FileHash -LiteralPath $Source -Algorithm SHA256).Hash
$targetHash = $null
for ($i = 0; $i -lt 50; $i++) {
    try {
        if (Test-Path -LiteralPath $Destination -PathType Leaf) {
            $targetHash = (Get-FileHash -LiteralPath $Destination -Algorithm SHA256).Hash
            if ($targetHash -eq $sourceHash) { break }
        }
    } catch {}
    Start-Sleep -Milliseconds 100
}
if ($targetHash -ne $sourceHash) {
    throw "Upload verification failed: $Destination"
}
Write-Host "Uploaded: $Destination"

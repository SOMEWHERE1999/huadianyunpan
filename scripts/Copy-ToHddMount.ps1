param(
    [Parameter(Mandatory=$true)][string]$Source,
    [Parameter(Mandatory=$true)][string]$Destination,
    [ValidateSet("Prompt","Fail","Overwrite","AutoRename")]
    [string]$OnConflict = "Prompt"
)

$ErrorActionPreference = "Stop"

if (-not (Test-Path $Source -PathType Leaf)) {
    throw "Source must be a regular file: $Source"
}

$destExists = Test-Path $Destination

if ($destExists) {
    switch ($OnConflict) {
        "Fail" {
            throw "Destination already exists: $Destination"
        }
        "Prompt" {
            Write-Host "Target file already exists:" -ForegroundColor Yellow
            Write-Host "  $Destination"
            Write-Host "[1] Cancel/Skip"
            Write-Host "[2] Replace"
            Write-Host "[3] Keep both, auto-rename"
            $choice = Read-Host "Enter choice"
            switch ($choice) {
                "1" { Write-Host "Skipped."; exit 0 }
                "2" { }
                "3" {
                    $dir = Split-Path $Destination -Parent
                    $base = [System.IO.Path]::GetFileNameWithoutExtension($Destination)
                    $ext = [System.IO.Path]::GetExtension($Destination)
                    $n = 1
                    do {
                        $newName = "$base ($n)$ext"
                        $Destination = Join-Path $dir $newName
                        $n++
                    } while (Test-Path $Destination)
                    Write-Host "Auto-rename: $Destination"
                }
                default { Write-Host "Invalid choice, skipping."; exit 0 }
            }
        }
        "Overwrite" {
            Write-Host "Overwriting: $Destination"
        }
        "AutoRename" {
            $dir = Split-Path $Destination -Parent
            $base = [System.IO.Path]::GetFileNameWithoutExtension($Destination)
            $ext = [System.IO.Path]::GetExtension($Destination)
            $n = 1
            do {
                $newName = "$base ($n)$ext"
                $Destination = Join-Path $dir $newName
                $n++
            } while (Test-Path $Destination)
            Write-Host "Auto-rename: $Destination"
        }
    }
}

$parentDir = Split-Path $Destination -Parent
if (-not (Test-Path $parentDir)) {
    throw "Destination parent directory does not exist: $parentDir"
}

try {
    if ($OnConflict -eq "Overwrite" -or ($destExists -and $OnConflict -eq "Prompt" -and $choice -eq "2")) {
        Copy-Item -LiteralPath $Source -Destination $Destination -Force -ErrorAction Stop
    } else {
        Copy-Item -LiteralPath $Source -Destination $Destination -ErrorAction Stop
    }
} catch {
    throw "Copy-Item failed: $_"
}

# Verify destination appeared with the expected content. Directory visibility
# and reads can lag the upload acknowledgement, so retry both for up to 5s.
$sourceSha = (Get-FileHash -Algorithm SHA256 -LiteralPath $Source).Hash
$maxWait = 50; $waited = 0
$sha = $null
while ($waited -lt $maxWait) {
    if (Test-Path $Destination -PathType Leaf) {
        try {
            $sha = (Get-FileHash -Algorithm SHA256 -LiteralPath $Destination -ErrorAction Stop).Hash
            if ($sha -eq $sourceSha) { break }
        } catch {
            $sha = $null
        }
    }
    Start-Sleep -Milliseconds 100
    $waited++
}
if ($sha -ne $sourceSha) {
    throw "Upload verification failed after $($waited*100)ms: destination missing or SHA256 mismatch: $Destination"
}

$size = (Get-Item $Destination).Length

Write-Host "Uploaded: $Destination"
Write-Host "SHA256: $sha"
Write-Host "Size: $size bytes"

[PSCustomObject]@{
    Source      = $Source
    Destination = $Destination
    SHA256      = $sha
    Size        = $size
    Conflict    = if ($destExists) { $OnConflict } else { "new" }
}

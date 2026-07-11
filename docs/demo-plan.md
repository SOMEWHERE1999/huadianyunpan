# Demo Plan - WinFsp Environment Verification

## Purpose

Verify that WinFsp and cgofuse are correctly installed and functional
in the current Windows environment before proceeding to AnyShare cloud
integration.

## Prerequisites

1. WinFsp 2.1+ installed with Developer files
   - Installer: https://github.com/winfsp/winfsp/releases
   - During installation, check "Developer files"
   - Verify: `"C:\Program Files (x86)\WinFsp\bin\fsptool-x64.exe" ver`

2. Go 1.24+
   - Verify: `go version`

3. MinGW GCC (for CGo)
   - Included at `.tools\mingw64\mingw64\bin\gcc.exe`
   - Verify: `gcc --version`

## Verification Steps

### 1. Build

```powershell
$env:PATH = "C:\Go\bin;.\.tools\mingw64\mingw64\bin;$env:PATH"
$env:CGO_ENABLED = "1"
$env:CC = "gcc"
$env:CGO_CFLAGS = "-I.\.tools\mingw64\mingw64\usr\local\include\winfsp"
go build -o hddfs.exe ./cmd/hddfs
```

### 2. Mount MemFS

```powershell
.\hddfs.exe memfs --mount H:
```

This mounts a read-only in-memory filesystem at drive H:.

The root directory contains:
  - hello.txt ("Hello from Huadian Drive!")
  - README.txt (demo description)

Supported operations:
  - Dir listing (Readdir)
  - File attributes (Getattr)
  - File open/read (Open, Read)

### 3. Verify

```powershell
# List directory
dir H:\

# Read files
type H:\hello.txt
type H:\README.txt
```

### 4. Unmount

Press Ctrl+C in the hddfs process, or:

```powershell
.\scripts\unmount.ps1
```

## Expected Results

- Mount succeeds without errors
- `dir H:\` shows hello.txt and README.txt
- `type H:\hello.txt` prints "Hello from Huadian Drive!"
- `type H:\README.txt` prints the README content
- Unmount is clean, drive letter is released


---

## CloudFS Mount (Read-Only Cloud-Backed)

### Prerequisites

- WinFsp installed
- Mock provider filesystem root prepared

### Setup Mock Provider Root

`powershell
mkdir C:\Temp\hddfs-demo\subdir -Force
"Hello from CloudFS!" | Out-File -Encoding UTF8 C:\Temp\hddfs-demo\hello.txt
"CloudFS read-only demo." | Out-File -Encoding UTF8 C:\Temp\hddfs-demo\README.txt
"subdir content" | Out-File -Encoding UTF8 C:\Temp\hddfs-demo\subdir\file.txt
"Chinese: 你好世界" | Out-File -Encoding UTF8 C:\Temp\hddfs-demo\中文文件.txt
"file with spaces" | Out-File -Encoding UTF8 "C:\Temp\hddfs-demo\file with spaces.txt"
`

### Build and Mount

`powershell
go build -o bin\hddfs.exe .\cmd\hddfs
.\bin\hddfs.exe mount --provider mock --root C:\Temp\hddfs-demo --mount H:
`

### Verify

`powershell
# List root directory
dir H:\

# Read files
type H:\hello.txt
type H:\README.txt

# Subdirectory
dir H:\subdir
type H:\subdir\file.txt

# Unicode filename
type H:\中文文件.txt

# Filename with spaces
type "H:\file with spaces.txt"
`

### Unmount

`powershell
taskkill /IM hddfs.exe /F
`

### Expected Results

- Mount succeeds
- File listing matches the source directory
- File content reads correctly
- Unicode and space-containing filenames work
- Write attempts fail with "read-only" error

---

## IPC Daemon Mode

### Prerequisites

- WinFsp installed
- hddsyncd built and running
- Mock provider data prepared

### Terminal 1: Start hddsyncd

```powershell
go build -o bin\hddsyncd.exe .\cmd\hddsyncd
.\bin\hddsyncd.exe run
```

Expected: npipe server started, hddsyncd ready.

### Terminal 2: Mount via IPC

```powershell
go build -o bin\hddfs.exe .\cmd\hddfs
.\bin\hddfs.exe mount --daemon --mount H:
```

### Terminal 3: Verify

```powershell
dir H:\
dir H:\test
type H:\test\hello.txt
```

Expected: hello world.

### Cleanup

Ctrl+C in each terminal.

---

## Write-Back Cache Mode

### Prerequisites

- hddsyncd running (Terminal 1)
- hddfs mounted via daemon (Terminal 2)

### Terminal 3: Verify Write Operations

```powershell
echo hello > H:\a.txt
type H:\a.txt
mkdir H:\dir
ren H:\a.txt H:\b.txt
type H:\dir\b.txt
del H:\b.txt
rmdir H:\dir
```

### Expected

- echo creates file and writes content
- type reads back content
- mkdir creates directory
- ren renames file
- del removes file
- rmdir removes directory

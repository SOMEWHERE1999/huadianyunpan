# Huadian Drive Manual Test Checklist

Use this checklist for hands-on verification before a demo or release.

## Prerequisites

- [ ] Windows 10/11 x64
- [ ] Go 1.24+ installed (`go version`)
- [ ] MinGW GCC in PATH (`gcc --version`)
- [ ] WinFsp 2025 installed (`C:\Program Files (x86)\WinFsp`)
- [ ] Built binaries: `bin/hddctl.exe`, `bin/hddsyncd.exe`, `bin/hddfs.exe`

## Quick Build

```powershell
$env:PATH = "C:\mingw\mingw64\bin;" + $env:PATH
$env:CGO_ENABLED = "1"
$env:CGO_CFLAGS = "-IC:\PROGRA~2\WinFsp\inc\fuse"
go build -o bin/hddctl.exe ./cmd/hddctl/
go build -o bin/hddsyncd.exe ./cmd/hddsyncd/
go build -o bin/hddfs.exe ./cmd/hddfs/
```

## 1. CLI Basics

- [ ] `bin/hddctl.exe version` → prints version
- [ ] `bin/hddctl.exe help` → prints usage
- [ ] `bin/hddsyncd.exe version` → prints version
- [ ] `bin/hddfs.exe version` → prints version
- [ ] `bin/hddfs.exe help` → prints usage

## 2. Authentication

- [ ] `bin/hddctl.exe auth status` → "Not authenticated"
- [ ] `bin/hddctl.exe logout` → "Not currently authenticated"
- [ ] After login: check `%LOCALAPPDATA%\HuadianDrive\auth.json` does NOT contain plaintext passwords/tokens
- [ ] After login: `bin/hddctl.exe auth status` → "Authenticated: true"

## 3. Remote Operations (Mock)

```powershell
# All commands use ephemeral temp dirs — data does NOT persist between commands.
bin/hddctl.exe remote mkdir /test
bin/hddctl.exe remote ls /
bin/hddctl.exe remote upload ./README.md /test/readme.md
bin/hddctl.exe remote stat /test/readme.md
bin/hddctl.exe remote download /test/readme.md
bin/hddctl.exe remote mv /test/readme.md /test/renamed.md
bin/hddctl.exe remote rm /test/renamed.md
```

- [ ] mkdir → no error
- [ ] ls → shows entries
- [ ] upload → no error
- [ ] stat → shows metadata
- [ ] download → prints content to stdout or saves to file
- [ ] mv → no error
- [ ] rm → no error

## 4. Sync Management

```powershell
$dir = Join-Path $env:TEMP "hdd-sync-demo"
New-Item -ItemType Directory -Path $dir -Force
bin/hddctl.exe sync add $dir /remote
bin/hddctl.exe sync status
```

- [ ] sync add → prints "sync root added: id=1 ..."
- [ ] sync status → lists sync root with enabled flag

## 5. Daemon

```powershell
# Terminal 1: start daemon
bin/hddsyncd.exe run

# Terminal 2: check status
bin/hddctl.exe sync status
```

- [ ] daemon starts without crash
- [ ] daemon outputs initialization log
- [ ] Ctrl+C shuts down cleanly

## 6. WinFsp Mount

### 6.1 MemFS (read-only in-memory demo)

```powershell
# Pick a free drive letter (check with: Get-PSDrive)
bin/hddfs.exe memfs --mount H:
```

- [ ] Mounts successfully ("The service hddfs has been started")
- [ ] H:\ appears in File Explorer
- [ ] Contains demo files
- [ ] Ctrl+C unmounts cleanly

### 6.2 CloudFS (mock provider, read-only)

```powershell
$root = Join-Path $env:TEMP "hdd-cloudfs-root"
New-Item -ItemType Directory -Path $root -Force
"hello" | Set-Content (Join-Path $root "test.txt")
bin/hddfs.exe mount --provider mock --root $root --mount H:
```

- [ ] Mounts with files from root directory
- [ ] Read operations succeed
- [ ] Write operations return "read-only" error
- [ ] Ctrl+C unmounts

### 6.3 Daemon mode (IPC write-back)

```powershell
# Terminal 1: start daemon
bin/hddsyncd.exe run

# Terminal 2: mount via IPC
bin/hddfs.exe mount --daemon --mount H:
```

- [ ] Mounts successfully
- [ ] File operations routed through daemon IPC
- [ ] Ctrl+C unmounts, daemon continues running

## 7. File Safety

- [ ] Create a file with known content. Run a download to that path with a non-existent remote file. Verify original content is preserved.
- [ ] Try `hddctl remote upload ../../etc/passwd /bad` → rejected
- [ ] Try `hddctl remote download ../../etc/passwd` → rejected

## 8. Unicode and Special Paths

- [ ] `hddctl remote upload ./测试文件.txt /中文/文件.txt` → succeeds
- [ ] `hddctl remote ls /中文/` → shows file
- [ ] `hddctl remote upload "./my docs/file name.txt" "/my docs/file name.txt"` → succeeds
- [ ] Long paths (> 260 chars) handled correctly

## 9. Error Handling

- [ ] `hddctl remote stat /nonexistent` → clear error message
- [ ] `hddctl remote rm /nonexistent` → clear error message
- [ ] `hddctl remote ls /nonexistent` → empty list or clear error
- [ ] Invalid subcommand → usage help
- [ ] Missing required argument → usage help with specific error

## 10. Cleanup

- [ ] Unmount any remaining WinFsp drives: `bin/hddfs.exe` (kill process)
- [ ] Stop daemon: Ctrl+C in daemon terminal
- [ ] Remove test directories: `Remove-Item -Recurse $env:TEMP\hdd-*`
- [ ] No leftover files in `%LOCALAPPDATA%\HuadianDrive\` (except auth.json if logged in)

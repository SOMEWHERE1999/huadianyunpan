# Windows-specific Design

## Process Model

- `hddctl.exe` is a transient CLI process. It connects to the daemon via
  named pipe, sends a command, reads the response, and exits.
- `hddsyncd.exe` is a long-running daemon. It creates the named pipe server,
  runs the sync engine, and listens for CLI requests.
- `hddfs.exe` (future) is a WinFsp service that mounts a virtual drive.

## Named Pipe IPC

- Pipe name: `\\.\pipe\hddsyncd`
- Protocol: length-prefixed JSON frames.
- The daemon exposes commands: status, sync-now, add-root, remove-root,
  shutdown.

## Filesystem Paths

| Purpose       | Path                               |
|---------------|------------------------------------|
| Config        | `%APPDATA%\hdd\config.json`       |
| Database      | `%LOCALAPPDATA%\hdd\store.db`     |
| Logs          | `%LOCALAPPDATA%\hdd\hddsyncd.log` |
| Cache         | `%LOCALAPPDATA%\hdd\cache\`       |

## Windows Service

The daemon will register as a Windows service using `golang.org/x/sys/windows/svc`.
This allows automatic startup and standard service lifecycle management.

## WinFsp Integration (Future)

`hddfs.exe` will use the `cgofuse` or WinFsp native API to present cloud files
as a local drive. The filesystem layer translates WinFsp callbacks into
`cloud.Provider` operations.

## Concurrency

- Worker pools use `semaphore.Weighted` for bounded concurrency.
- Per-path serialization uses a mutex map keyed by canonical path.
- File watchers use `fsnotify` with a debounce window.

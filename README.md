# Huadian Drive

A native Windows command-line cloud-drive client for the Huadian
cloud storage service.

## Status

Engineering skeleton — mock provider only.

## Build

```powershell
.\scripts\build.ps1
```

## Usage

```powershell
# CLI client
.\bin\hddctl.exe version
.\bin\hddctl.exe help

# Synchronization daemon (mock mode)
.\bin\hddsyncd.exe version
.\bin\hddsyncd.exe run
```

Press `Ctrl+C` to stop the daemon gracefully.

## Directories

| Purpose  | Path                               |
|----------|------------------------------------|
| Config   | `%APPDATA%\hdd\config.json`        |
| Database | `%LOCALAPPDATA%\hdd\store.db`      |
| Logs     | `%LOCALAPPDATA%\hdd\hddsyncd.log`   |
| Cache    | `%LOCALAPPDATA%\hdd\cache\`         |

## License

Internal project — no public license.

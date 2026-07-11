# Huadian Drive Milestones

## Milestone 1: Engineering Skeleton (current)

- [x] Go module and directory layout
- [x] `hddctl.exe` with `version` and `help`
- [x] `hddsyncd.exe` with `version` and `run` (mock provider, graceful exit)
- [x] `cloud.Provider` interface and mock implementation
- [x] Domain types, config, logging packages
- [x] Documentation and build script

## Milestone 2: IPC and Metadata

- [ ] Named-pipe IPC between hddctl and hddsyncd
- [ ] daemon status, sync-now, add-root, remove-root commands
- [ ] SQLite metadata store (file index, sync state)
- [ ] Config file loading from %APPDATA%

## Milestone 3: Sync Core

- [ ] Upload and download worker pools
- [ ] Directory watching with debounced change detection
- [ ] Bidirectional synchronization engine
- [ ] Conflict detection and resolution

## Milestone 4: Production Backend

- [ ] Real Huadian cloud provider (AnyShare API)
- [ ] Windows service registration
- [ ] Installer script

## Milestone 5: Virtual Filesystem

- [ ] WinFsp-based `hddfs.exe`
- [ ] On-demand file download
- [ ] Write-through caching

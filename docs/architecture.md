# Huadian Drive Architecture

## High-level Design

```
+-----------------+       named pipe       +-----------------+
|    hddctl.exe   | <--------------------> |   hddsyncd.exe  |
|  (CLI client)   |       (IPC)           |   (daemon)       |
+-----------------+                        +-----------------+
        |                                       |
        |  start / stop                         |  cloud API
        |  status / config                      |
        v                                       v
                                    +-----------------+
                                    |  cloud.Provider |
                                    |  (interface)     |
                                    +-----------------+
                                            |
                               +------------+------------+
                               |                         |
                        +-------------+          +-------------+
                        | mock        |          | huadian     |
                        | (in-memory) |          | (AnyShare)  |
                        +-------------+          +-------------+

+-----------------+      +-----------------+
|   hddfs.exe     |      | SQLite store    |
| (WinFsp FS)     |      | (metadata)      |
+-----------------+      +-----------------+
```

## Package Layout

```
cmd/
  hddctl/          CLI entry point (arguments only)
  hddsyncd/        Daemon entry point (arguments only)
  hddfs/            WinFsp FS entry point (future)
internal/
  app/             Bootstrap: version, logging init
  cloud/           Provider interface + real/mock backends
  config/          Configuration parsing
  domain/          Shared domain types
  ipc/             Named-pipe IPC (future)
  logging/         Structured logging setup
  platform/        OS-specific adapters (future)
  store/           Metadata persistence (future)
  sync/            Synchronization engine (future)
  watch/           Directory watching (future)
```

## Design Decisions

1. **Provider interface**: Cloud backends satisfy `cloud.Provider`. The sync
   engine never depends on a concrete implementation.
2. **IPC over named pipes**: The CLI communicates with the daemon through
   Windows named pipes. Protocol structures stay platform-independent.
3. **Worker pools**: Upload and download operations use bounded goroutine pools
   with serialized tasks per canonical path.
4. **Debounced watching**: Repeated filesystem events are debounced to avoid
   redundant work.
5. **Exponential backoff**: Failed tasks retry with bounded exponential backoff.

# Huadian Drive Requirements

## Overview

A native Windows command-line cloud-drive client for the Huadian cloud storage
service. The project delivers three executables: a CLI controller (`hddctl`),
a synchronization daemon (`hddsyncd`), and later a WinFsp-based userspace
filesystem (`hddfs`).

## Functional Requirements

| ID     | Requirement                                         | Phase |
|--------|-----------------------------------------------------|-------|
| FR-01  | Command-line interface with version and help        | 1     |
| FR-02  | Mock cloud provider for offline development         | 1     |
| FR-03  | Windows named-pipe IPC between CLI and daemon       | 2     |
| FR-04  | SQLite metadata store                               | 2     |
| FR-05  | Upload and download with bounded worker pools       | 3     |
| FR-06  | Directory watching with debounced change detection  | 3     |
| FR-07  | Automatic bidirectional synchronization             | 3     |
| FR-08  | Real Huadian cloud provider (AnyShare API)          | 4     |
| FR-09  | Windows service integration                         | 4     |
| FR-10  | WinFsp virtual filesystem                           | 5     |

## Non-functional Requirements

- Use `%APPDATA%` for configuration, `%LOCALAPPDATA%` for runtime data.
- Never log passwords, tokens, cookies, or authorization headers.
- Never disable TLS certificate validation.
- Pending tasks survive process restarts.
- The daemon shuts down cleanly on Ctrl+C or service stop.
- Unicode and Chinese filenames are supported throughout.

# Huadian Drive Known Limitations

Last updated: 2026-06-28

## Unresolved Issues from Audit

### P1-12: SQLite Store Uses Text Files, Not Real SQLite

**Impact**: The `internal/store/sqlite` package stores data as per-row text files, not in a real SQLite database. This means:
- No atomic multi-row transactions
- No WAL mode or concurrent reader support
- No foreign key constraints or schema migrations
- Crash during write may produce partial rows

**Mitigation**: Individual row operations are atomic (single `os.WriteFile`). For single-user demo use with < 1000 tasks, this is adequate.

**Fix plan**: Replace with `modernc.org/sqlite` (pure Go, no CGO), add WAL mode, migrations, and proper transactions.

---

## Design Limitations (by Development Phase)

### Mock Provider Is Ephemeral

- Each `hddctl remote` command creates a new temp directory as the mock root.
- Data does NOT persist between commands.
- This is by design for the current development phase. Real persistence will come with the Huadian provider.

### AnyShare Provider Requires Real Credentials

- `internal/cloud/huadian/provider.go` has complete implementation but has only been tested against `httptest.Server` mocks.
- Path → docid resolution architecture exists but actual docid values must be confirmed against the real AnyShare API.
- All 7 Provider operations (List/Stat/Upload/Download/Mkdir/Rename/Remove) are implemented and testable with fake servers.
- Real API endpoints have been confirmed via browser HAR capture but full integration testing requires valid university credentials.

### CDP Login Not Production-Ready

- `hddctl login` uses Chrome DevTools Protocol (port 9222) to automate browser login.
- Requires Chrome/Edge installed in standard locations.
- Cookie extraction and storage is encrypted at rest (AES-256-GCM) but the CDP protocol itself is intended for debugging, not production authentication.
- The WebView2 embedded browser login is planned but only has a stub implementation.

### WinFsp Requires CGO and DLL

- `hddfs.exe` requires WinFsp DLL installed on the system.
- Build requires CGO + MinGW GCC + WinFsp headers.
- `CGO_ENABLED=0` will correctly exclude hddfs from build (via `//go:build windows && cgo` tag).
- Mount/unmount behavior not verified on all Windows versions.

### Watcher Uses Polling, Not Filesystem Events

- `internal/watch/watcher.go` uses `filepath.WalkDir` polling at configurable intervals.
- Does not use `ReadDirectoryChangesW` (Windows filesystem change notifications).
- Deletion and rename detection works via diff-based approach (compare current scan with last seen state).
- Debounce is time-based (default 500ms), not content-stability-based.

### No Real Cloud Integration

- The daemon (`hddsyncd`) currently only runs with mock provider.
- No connection to the actual Huadian AnyShare server has been established.
- Sync engine, worker pools, and IPC are fully functional with mock provider.

---

## Environment Dependencies

| Component | Required | Notes |
|---|---|---|
| Windows 10/11 x64 | Yes | Named pipes, WinFsp, service management |
| Go 1.24+ | Yes | Build and development |
| MinGW GCC | For hddfs | CGO compilation |
| WinFsp 2025 | For hddfs | Userspace filesystem driver |
| Chrome/Edge | For login | CDP browser automation |

---

## Known Test Gaps

### Not Tested (Requires Real Environment)

- WinFsp mount/unmount lifecycle with actual file I/O
- WinFsp concurrent callback behavior under load
- Daemon + WinFsp integrated write-back flow
- Real AnyShare API integration
- Windows service install/start/stop lifecycle
- CDP login with actual university authentication
- Network timeout and disconnection recovery
- Large file (>1GB) upload/download
- Concurrent multi-client IPC

### Covered by Unit Tests

- All Provider operations (httptest.Server)
- IPC protocol encoding/decoding
- Named pipe server/client lifecycle
- Path traversal rejection
- Credential encryption
- Task queue (dedup, claim, retry, dead state, stale recovery)
- Watcher (new file, modify, debounce, deletion)
- Syncer diff logic
- Download safety (temp file + atomic rename)
- Build tags (correct exclusion on CGO_ENABLED=0)

---

## Performance Limitations

- Upload reads entire file into memory (for mock provider; AnyShare provider uses streaming MD5)
- Large directory listings are not paginated in mock provider
- Watcher polling may miss rapid changes between polls (mitigated by persistent task queue)
- No cache size limits or eviction in CloudFS
- No compression or deduplication in transfer layer

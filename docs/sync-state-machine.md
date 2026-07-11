# Sync State Machine

## Write-Back Cache Flow

```
User App (hddfs)
     │
     │ Create(path="/a.txt")
     ▼
┌─────────────┐     IPC: fs.create     ┌──────────────┐
│  IPCFileSystem │ ──────────────────→ │   hddsyncd    │
│  (WinFsp)    │ ←── cache_path ────── │               │
└──────┬──────┘                        │ creates empty │
       │ Write(data)                   │ cache file    │
       ▼                               └──────────────┘
┌─────────────┐
│ local cache │  Write writes to local cache file only
│ file write  │  marks handle.dirty = true
└──────┬──────┘
       │ Release / Flush
       ▼
┌─────────────┐     IPC: fs.close      ┌──────────────┐
│  dirty=true  │ ─── {path, dirty} ──→ │   hddsyncd    │
└─────────────┘                        │ enqueueUpload │
                                       │ async via     │
                                       │ goroutine     │
                                       └──────┬───────┘
                                              │ Provider.Upload
                                              ▼
                                       ┌──────────────┐
                                       │  Cloud        │
                                       └──────────────┘
```

## State Transitions

```
  NONE ──Create──→ DIRTY ──Write──→ DIRTY ──Flush/Release──→ UPLOAD_QUEUED
   │                  │                                         │
   │                  │ Release(dirty=false)                    │ async upload
   │                  ↓                                         ↓
   │              REMOVED                                 UPLOADING
   │                                                        │    │
   │                                                  OK    │    │ FAIL
   │                                                        ↓    ↓
   │                                                    SYNCED  FAILED
   │                                                              │
   │                                                        retry later
   │                                                              │
   └──────────────────────────────────────────────────────────────┘
```

## Key Rules

1. **Write only to local cache**: Write operations never call cloud.Upload directly.
2. **Dirty flag**: Set to true on first Write after Create/Open.
3. **Flush/Release trigger upload**: Calls ipcMarkDirty or ipcCloseDirty to notify daemon.
4. **Path locking**: Create/Write/Flush/Release/Rename/Unlink acquire per-path mutex.
5. **Failure preservation**: Upload failure keeps cache file + task in SQLite for retry.
6. **Conflict detection**: Daemon checks ETag before upload; if mismatch, creates conflict record.

## Failure Recovery

- SQLite tasks table stores `pending`/`failed` states
- Worker pool (`ListPendingTasks`) recovers `failed` → `pending` on restart
- Bounded exponential backoff (max 10 min, 8 retries)
- Cache files persist across process restarts

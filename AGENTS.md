# Huadian Drive Windows Project Instructions

## Project Overview

This repository implements a Windows command-line cloud-drive client for the
Huadian cloud storage service.

The project is written in Go.

Current executables:

* `hddctl.exe`: command-line client
* `hddsyncd.exe`: background synchronization daemon

A third executable will be added later:

* `hddfs.exe`: WinFsp-based userspace filesystem

## Current Development Target

The current target is a native Windows terminal client.

Implement the project in this order:

1. Command-line interface
2. Mock cloud provider
3. Windows named-pipe IPC
4. SQLite metadata store
5. Upload and download worker pools
6. Directory watching and filtering
7. Automatic synchronization
8. Real Huadian cloud provider
9. Windows service
10. WinFsp filesystem

Do not implement Linux FUSE during the current phase.

## Architecture Rules

1. Files under `cmd/` only parse arguments, construct dependencies, and start
   applications.
2. Business logic must not be placed in `main.go`.
3. Cloud-specific behavior must stay under `internal/cloud/`.
4. Every cloud implementation must satisfy `cloud.Provider`.
5. The synchronization engine must not depend directly on AnyShare.
6. Windows-specific code must stay under `internal/platform/windows/`.
7. IPC must use Windows named pipes.
8. IPC protocol structures must stay platform independent.
9. SQL must stay under `internal/store/sqlite/`.
10. Runtime files must not be created inside the source repository.
11. Use `%APPDATA%` for configuration.
12. Use `%LOCALAPPDATA%` for database, cache, logs, and temporary files.
13. Never log passwords, tokens, cookies, or authorization headers.
14. Never disable TLS certificate validation.
15. Do not invent undocumented cloud API endpoints.
16. Do not automate or bypass university authentication controls.
17. Do not use browser cookies as permanent credentials.
18. Do not add a GUI unless explicitly requested.
19. Do not add WinFsp before the terminal and synchronization functions pass
    their tests.
20. Do not add Linux support during the Windows-first phase.

## Concurrency Rules

1. Every background operation must accept a `context.Context`.
2. Every goroutine must have a shutdown path.
3. Shared mutable state must be protected explicitly.
4. Upload and download operations must use bounded worker pools.
5. Tasks for the same canonical path must be serialized.
6. Repeated file events must be debounced.
7. Failed tasks must use bounded exponential backoff.
8. Pending tasks must survive process restarts.
9. The daemon must shut down cleanly.

## Path Rules

1. Use `filepath` functions for local Windows paths.
2. Normalize local paths before comparing them.
3. Reject paths that escape a configured synchronization root.
4. Treat remote paths as slash-separated logical paths.
5. Do not concatenate paths with raw string operations.
6. Tests must include Unicode and Chinese filenames.
7. Tests must include long paths, spaces, and mixed-case names.

## Testing Rules

Before declaring a task complete, run:

```powershell
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/...
```

For concurrent code, also run:

```powershell
go test -race ./...
```

Tests must use temporary directories.

Do not require real cloud credentials for ordinary unit tests.

Network code must use `httptest.Server` or an equivalent fake server.

## Development Workflow

For every change:

1. Inspect the relevant files and design documents.
2. State a short implementation plan.
3. Add or update tests first when practical.
4. Implement the smallest complete change.
5. Run formatting, tests, vet, and build.
6. Review the Git diff.
7. Report:

   * files changed;
   * design decisions;
   * commands run;
   * test results;
   * remaining limitations.

## Definition of Done

A task is complete only when:

* requested behavior is implemented;
* relevant tests exist;
* all required checks pass;
* runtime state and credentials are not committed;
* documentation matches the implementation;
* the architecture rules are followed.

## Destructive Actions

Never:

* rewrite Git history;
* run destructive commands outside this repository;
* remove user files;
* delete synchronization roots;
* use `git push --force`;
* commit credentials, databases, cache files, tokens, or logs;
* install system drivers without explicit user approval.

## Full Project Review Rules

## Project

This is a Windows cloud-drive client written in Go.

Main executables:

* `hddctl`: command-line control client
* `hddsyncd`: background synchronization daemon
* `hddfs`: WinFsp filesystem process (planned, not yet implemented)

Main systems:

* AnyShare API provider
* CAS/OAuth2 interactive authentication
* WebView2 login
* Windows named-pipe IPC
* SQLite metadata storage
* file synchronization engine
* local cache
* WinFsp mount

## Review priorities

Review in this order:

1. Build failures
2. Runtime crashes
3. Data loss risks
4. Authentication and secret leakage
5. Path traversal and unsafe filesystem operations
6. Concurrency bugs and goroutine leaks
7. WinFsp deadlocks and incorrect error mapping
8. IPC framing, timeout and validation
9. SQLite transaction and restart recovery
10. AnyShare protocol correctness
11. Missing tests
12. Maintainability and documentation

## Architecture constraints

* `cmd/` contains only startup and dependency assembly.
* WinFsp callbacks must not perform slow remote operations directly.
* File writes must first enter local cache.
* Uploads must pass through the persistent task queue.
* AnyShare-specific code stays under the cloud provider package.
* SQL stays under the SQLite store package.
* Windows-specific code stays under Windows platform packages.
* Do not introduce a second competing authentication implementation.
* Do not silently ignore errors.
* Do not log passwords, verification codes, cookies, tokens, authorization headers or signed temporary URLs.
* Do not disable TLS verification.
* Do not change confirmed AnyShare API paths without evidence.
* Do not delete user files during tests.
* Tests must use temporary directories and fake HTTP servers.

## Required checks

After every repair batch, run:

```powershell
gofmt -w .
go test ./...
go vet ./...
go build ./cmd/...
```

For concurrency-related changes, also run:

```powershell
go test -race ./...
```

## Repair workflow

Before modifying code:

1. Inspect the relevant files.
2. Reproduce or prove the issue.
3. State the root cause.
4. Propose the smallest safe fix.
5. Add or update a regression test.

After modifying code:

1. Run focused tests.
2. Run repository-wide checks.
3. Review the diff.
4. Report changed files, test results and remaining risks.

Do not combine unrelated fixes in one change.
Do not perform broad refactoring while fixing a specific defect.
Do not claim a bug is fixed without a regression test or reproducible validation.

# WinFsp + hddfs 全链路审查报告

审查日期：2026-06-28
审查范围：`cmd/hddfs/`、`internal/mount/winfsp/`、`internal/ipc/`、`internal/platform/windows/npipe/`、`cmd/hddsyncd/`

## 调用链

```
Windows Explorer
  → WinFsp Kernel Driver
    → cgofuse callback (Goroutine-per-call)
      → IPCFileSystem / CloudFS / MemFS
        → Named Pipe (\\.\pipe\huadian-drive)
          → hddsyncd dispatchFS
            → handleFS* handlers
              → MockProvider (本地文件系统)
```

`--daemon` 模式使用 `IPCFileSystem`（`cmd/hddfs/main.go:142-145`），`readOnly=false`。

---

## P0（数据丢失/安全严重）

无。

---

## P1（高风险）

### P1-1 `cacheDir=""` 跳过全部缓存路径校验

**位置**：`cmd/hddfs/main.go:143`、`internal/mount/winfsp/ipcfs.go:353-357`

`NewIPCFileSystem(pipePath, "")` 传入空 `cacheDir`。`validateCachePath` 的条件 `if fs.cacheDir != ""` 永为假。daemon 通过 IPC 返回的 `cache_path` 被无条件信任——恶意 daemon 可诱导 hddfs 读写任意本地文件。

### P1-2 Open 阻塞至全量下载完成

**位置**：`internal/mount/winfsp/ipcfs.go:349`、`cmd/hddsyncd/main.go:257-280`

`Open` 回调调用 `ipcOpen` → daemon `handleFSOpen` → `prov.Stat` + `prov.Download` 全量下载。FUSE 回调 goroutine 被阻塞最长 60 秒。无流式/分片下载，大文件 Open 冻结该请求。

### P1-3 Flush 不等待上传完成

**位置**：`internal/mount/winfsp/ipcfs.go:526,237`、`cmd/hddsyncd/main.go:359-361`

```
Flush → ipcMarkDirty → IPC → daemon handleFSMarkDirty
  → go enqueueUpload(path, prov)  ← fire-and-forget goroutine
  → return ipc.Response{}          ← 立即返回，不等待上传
→ h.dirty = false                  ← 标记已清除，但上传可能未开始
```

应用层收到写入成功，但数据仅存在于本地临时文件。

### P1-4 Release 不等待上传

**位置**：`internal/mount/winfsp/ipcfs.go:406-414`、`cmd/hddsyncd/main.go:299`

与 Flush 相同模式。`Release` → `ipcCloseDirty` → daemon `go enqueueUpload` fire-and-forget。

### P1-5 daemon 崩溃即数据丢失

**位置**：`cmd/hddsyncd/main.go:97-102`

```go
mockDir, _ := os.MkdirTemp("", "hddsyncd-mock-*")
defer os.RemoveAll(mockDir)
```

缓存目录是临时目录，daemon 退出时彻底清空。所有未上传文件永久丢失。无持久化任务队列，不满足并发规则 #8（Pending tasks must survive process restarts）。

### P1-6 daemon 正常关闭也丢失数据

**位置**：`cmd/hddsyncd/main.go:102,145`

`server.Shutdown()` 仅等待 handler goroutine（`wg.Wait` at `npipe.go:126`），不等待 `go enqueueUpload` 产生的上传 goroutine。`defer os.RemoveAll(mockDir)` 在上传 goroutine 可能仍在读取时删除源文件。

### P1-7 缓存永不过期

**位置**：`internal/mount/winfsp/ipcfs.go:96,442-458`

- `fs.files` map 中的文件内容永不清除（仅在 Release/Unlink 时移除）
- `fs.dirs` 目录缓存仅在本地变更时清除（`flushDirCacheFor`）
- 无 TTL、无 ETag 校验、无远程轮询
- 远程文件变更永久不可见，直到手动触发本地操作清除缓存

### P1-8 401/403 未处理

**位置**：`internal/mount/winfsp/ipcfs.go:676-694`

`ipcErrToFuse` 基于错误消息子串匹配。识别 "not found"→`ENOENT`、"is a directory"→`EISDIR`，但**不识别** "unauthorized"/"forbidden"/"401"/"403"。认证失败被映射为通用 `EIO`，用户无法区分。无重认证触发机制。

---

## P2（中风险）

### P2-1 IPC 断连后不重连

**位置**：`internal/mount/winfsp/ipcfs.go:46-57`

`IPCClient.conn` 断连后永不重置为 `nil`。`ipcCall` 中的 `c.conn == nil` 检查永不触发重连。一旦管道断裂，所有后续 IPC 调用失败，直到重新挂载。

### P2-2 无上传去重

**位置**：`internal/mount/winfsp/ipcfs.go:526,508`、`cmd/hddsyncd/main.go:303-319`

多次 Flush 触发多个并发 `go enqueueUpload`，均读取同一缓存文件。违反并发规则 #5（Tasks for the same canonical path must be serialized）。

### P2-3 写入与上传竞争

**位置**：`cmd/hddsyncd/main.go:308-314` vs `internal/mount/winfsp/ipcfs.go:501`

`enqueueUpload` 用 `os.Open(path)` 读取缓存文件时，hddfs 的 Write 回调可能正通过 `f.WriteAt(buff, ofst)` 写入同一文件。上传内容可能混入新旧数据。

### P2-4 `ipcMarkDirty`/`ipcCloseDirty` 丢弃 IPC 错误

**位置**：`internal/mount/winfsp/ipcfs.go:237,246`

```go
func (fs *IPCFileSystem) ipcMarkDirty(ctx context.Context, path string) {
    req := ipc.Request{...}
    fs.ipcCall(req) // 返回值被丢弃
}
```

daemon 不可达时，Flush/Release 静默返回成功。

### P2-5 文件句柄按 path 而非 fh 索引

**位置**：`internal/mount/winfsp/ipcfs.go:96`

`fs.files` map 用 `path` 作 key，忽略 WinFsp `fh` 参数。多进程打开同一文件共享一个 `ipcFileHandle`，dirty 标记共享——任一进程的 Flush 清除所有进程的 dirty 状态。

### P2-6 Release 先删 handle 再触发上传

**位置**：`internal/mount/winfsp/ipcfs.go:405-414`

```go
delete(fs.files, path)           // 行 407：先删除
fs.ipcCloseDirty(ctx, path, dirty) // 行 414：再触发上传
```

上传触发时 handle 已不存在。并发 Open 可能创建新 handle 引用同一缓存文件，与进行中的上传竞争。

### P2-7 无 context 传播

**位置**：`internal/mount/winfsp/ipcfs.go:255,300,333` 等

所有 FUSE 回调使用 `context.Background()`，无取消、无超时、无父 context。即使用户关闭 Explorer，下载/上传继续运行。

### P2-8 Windows 保留名未拒绝

**位置**：`internal/mount/winfsp/cloudfs.go:74-87`、`cmd/hddsyncd/main.go:321-335`

`cleanPath`、`safeCachePath`、`validateCachePath` 均不检查 `CON`/`PRN`/`AUX`/`NUL`/`COM1`/`LPT1` 等保留名。`os.Create("...\\CON")` 时 OS 返回 "The parameter is incorrect"，映射为通用 `EIO`。

### P2-9 尾部空格/点未处理

**位置**：同上

`filepath.Clean` 不剥离尾部空格和点。`os.Create("file.txt ")` 在 Windows 上失败，错误信息混乱。

### P2-10 `IPCFileSystem.Close()` 永不被调用

**位置**：`cmd/hddfs/main.go:142-145`

`main.go` 中 `validateAndMount` 不调用 `fs.Close()`。IPC 连接在进程退出时由 OS 关闭，非优雅断开。

### P2-11 卸载时不通知 daemon

**位置**：`internal/mount/winfsp/ipcfs.go:114-116`

hddfs 卸载时 daemon 无感知，缓存文件留在 daemon 侧。daemon 可能继续为已断开的客户端提供 IPC 服务。

---

## P3（低风险）

| # | 问题 | 位置 |
|---|------|------|
| P3-1 | `handleFSOpen` 先 Stat 再 Download——加倍 RTT | `cmd/hddsyncd/main.go:260,280` |
| P3-2 | Rmdir 无 pathLock（Unlink 有）——不一致 | `ipcfs.go:604` vs `589` |
| P3-3 | Read/Write 对已打开 handle 无 pathLock——不致命但不一致 | `ipcfs.go:365,473` |
| P3-4 | 无端到端 Unicode 集成测试 | 全局 |
| P3-5 | `containsAny` 手写逻辑——可用 `strings.Contains` | `cloudfs.go:177-188` |
| P3-6 | FUSE 路径用 `filepath.Clean` 而非 `path.Clean` | `cloudfs.go:79` |

---

## Rename 死锁分析

**结论：无死锁。** 所有操作遵循统一锁顺序 `pathLock → IPCClient.mu → fs.mu → dirsMu`。Rename 在锁定前按字典序排序路径（`ipcfs.go:554-557`），避免 AB/BA 死锁。同名路径只锁一次。

## Unicode 路径分析

**结论：正确处理。** Go 字符串原生 UTF-8，`filepath` 函数 Unicode 安全，JSON 编码正确处理。有 4 个单元测试覆盖中文路径。缺失端到端集成测试。

---

## 摘要

| 严重度 | 数量 | 关键主题 |
|--------|------|---------|
| P0 | 0 | — |
| P1 | 8 | 数据丢失（Flush/Release/daemon 崩溃）、缓存验证跳过、缓存永不过期、401/403 未处理、Open 阻塞下载 |
| P2 | 11 | IPC 重连、上传去重、写入竞争、错误丢弃、handle 生命周期、保留名、context 缺失 |
| P3 | 6 | 冗余 Stat、锁不一致、Unicode 测试、代码简化 |

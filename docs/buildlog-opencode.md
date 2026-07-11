# OpenCode 开发日志

本文档记录 OpenCode AI 辅助编码工具参与 Huadian Drive Windows 云盘客户端项目的开发过程。
涵盖从 new13（跨父目录移动）到 new18（复制上传）共六个大版本的迭代。

---

## 约定

- **工作目录**：`D:\ncepupan`
- **禁止行为**：不访问真实云盘、不构建 EXE、不提交 Git、不运行 `go test ./...`
- **验证命令**（每轮执行）：`go test`, `go vet`, `go build ./cmd/...`（部分轮次豁免 build）
- **核心技术栈**：Go、WinFsp/cgofuse、Named Pipe IPC、SQLite、AnyShare HTTP 客户端
- **代码分界**：`cmd/hddsyncd/main.go`（daemon 端）、`internal/mount/winfsp/ipcfs.go`（WinFsp 回调层）、`cmd/hddfs/main.go`（CLI）

---

## 第 1 阶段：跨父目录移动（new13）

### 目标

实现 `--mkdir-rename-move-only` 模式下目录跨父目录移动，复用 `DirectRemoteProvider.Move` 而非 `Provider.Rename`。

### 时间

2026-07-06 之前（前期人工开发，new13 已通过人工真实云盘验证）

### 已实现功能

- 挂载盘读取和目录枚举
- 目录创建（`Mkdir`）
- 目录同父目录重命名（`Rename`）
- 目录跨父目录移动（`Move`）
- Named Pipe 陈旧连接只读重连
- 写操作不自动重试

### 问题

`prov.Rename()` 对跨父目录移动返回成功（nil error），但远端状态未变化（旧路径仍存在、新路径缺失），属于 Provider 假成功。

### 原因

真实华电 `Provider.Rename` 仅支持同父目录重命名。跨父目录调用时，API 执行无操作（basename 相同）并返回成功。

### 解决方案

- 同父目录：继续使用 `prov.Rename()`
- 跨父目录：类型断言 `prov.(cloud.DirectRemoteProvider)` 后调用 `dirProv.Move(ctx, sourcePath, destinationDirectory, TransferConflictFail)`
- 增加后置状态校验：Stat oldPath（必须返回 ENOENT），Stat newPath（必须返回目录）
- 配置 `hddsyncd.new13.exe` + `hddfs.new13.exe`

---

## 第 2 阶段：文件同父目录重命名（new14）

### 目标

新增模式 `--mkdir-rename-move-file-rename-only`，支持普通文件同父目录重命名。

### 时间

2026-07-06 上午

### 架构设计

引入 `writePolicy` 结构体（capability-based 权限控制），替代分散的布尔标志：

| 模式 | canMkdir | canRenameFile | canRenameDir | canMoveDir |
|---|---|---|---|---|
| read_only | N | N | N | N |
| mkdir_only | Y | N | N | N |
| mkdir_rename_only | Y | N | Y | N |
| mkdir_rename_move_only | Y | N | Y | Y |
| mkdir_rename_move_file_rename_only | Y | Y | Y | Y |

### 关键实现

- **daemon 端**：`handleFSRenameFile()` — 同父目录文件重命名 → `prov.Rename()`，后置校验（文件大小比对）
- **WinFsp 端**：`renameInFileRenameMode()` — 源类型判断，文件同父路由
- **Access**：`DELETE_OK` 对文件允许（new14 模式），`W_OK` 拒绝
- **projectMode**：文件 `0100444`，目录 `040777`

### 问题

WinFsp `Access(DELETE_OK)` 对文件放行逻辑遗漏 new14，导致 Windows Explorer 重命名文件时报"访问被拒绝"。

### 原因

`Access` 回调中 DELETE_OK 分支只检查了 `mkdirRenameOnly || mkdirRenameMoveOnly`，未包含 `mkdirRenameMoveFileRenameOnly`。

### 解决方案

增加 `mkdirRenameMoveFileRenameOnly` 到 Access DELETE_OK 允许列表。文件模式保持 `0100444`（只读投影）。

### 二进制

`hddsyncd.new14.exe` + `hddfs.new14.exe`

---

## 第 3 阶段：文件跨父目录移动（new15）

### 目标

新增模式 `--mkdir-rename-move-file-rename-move-only`，支持普通文件跨父且同名移动（basename 不变）。

### 时间

2026-07-06 上午

### 关键实现

- **daemon 端**：`handleFSRenameFile()` 跨父分支 → `DirectRemoteProvider.Move(ctx, oldPath, newParent, TransferConflictFail)`
- 跨父+改名 → 拒绝，报错 `file-cross-parent-rename-not-supported`
- 后置校验：old 不存在 + new 存在且大小一致 → 成功

### 问题（严重 Bug）

`dispatchFS` 的受限模式白名单使用 `switch pol.name` 逐模式判断，漏掉了新模式 `"mkdir_rename_move_file_rename_move_only"`。新区 `blocked=true` → `fs.rename` 被返回 `read_only_filesystem` → daemon 从未进入 `handleFSRename` → 文件移动完全不生效。

### 原因

基于字符串白名单的权限判断，每新增一个模式就需要手动添加 case，极易遗漏。

### 解决方案（见第 4 阶段）

---

## 第 4 阶段：capability-based dispatchFS 重写（new16）

### 目标

消除第 3 阶段的根因——用 capability-based 权限判断替代字符串白名单，一劳永逸防止新增模式漏改 dispatchFS。

### 时间

2026-07-06 中午

### 关键实现

新增 `policyAllowsRequest(pol writePolicy, reqType string) bool`：

```go
case "fs.rename":
    return pol.canRenameFile || pol.canMoveFile || pol.canRenameDir || pol.canMoveDir
case "fs.remove":
    return pol.canDeleteFile || pol.canDeleteDir
case "fs.uploadStaged":
    return pol.canCopyUploadFile
```

dispatchFS 改写为：

```go
if isRestricted && !policyAllowsRequest(pol, req.Type) {
    return read_only_filesystem
}
```

不再依赖 `pol.name` 字符串比较。

### 同阶段修复的其他遗漏

| 位置 | 问题 | 修复 |
|---|---|---|
| `projectMode` | new16 模式文件未列入 `isRestrictedMode()` 的分支，落入 `else` 得 `0100644` | 引入 `isRestrictedMode()` 统一判断 |
| `Open` 写标志 | 仅检查 `readOnly || mkdirOnly`，其他受限模式误入 `openWriteable` | 全部改为 `isRestrictedMode()` |
| `Truncate/Unlink/Rmdir` | 同上 | 同上 |
| `Write` | 仅检查 `readOnly || mkdirOnly || mkdirRenameOnly` | 改为 `isRestrictedMode()` |
| `Utimens/Chmod/Chown/xattr` | 仅检查 `readOnly || mkdirOnly` | 改为 `isRestrictedMode()` |
| 路径比较 | 使用 `strings.LastIndexByte` | 改为 `path.Base()` |

### 二进制

`hddsyncd.new16.exe` + `hddfs.new16.exe`

---

## 第 5 阶段：文件/目录删除模式（new17）

### 目标

新增模式 `--mkdir-rename-move-file-rename-move-remove-only`，允许删除普通文件和空目录。

### 时间

2026-07-06 下午

### 关键实现

- **writePolicy**：新增 `canDeleteFile`、`canDeleteDir`
- **daemon 端**：`handleFSRemove(ctx, req, prov, pol)` — Stat 判断源类型 → canDeleteFile/canDeleteDir 权限检查 → `prov.Remove()` → 后置校验
- **WinFsp 端**：`Unlink` 和 `Rmdir` 在 new17 模式放行：`isRestrictedMode() && !new17 → EROFS`

### 问题 1：普通文件删除失败

`Remove-Item` 报"对路径的访问被拒绝"，daemon 日志无 `fs_remove`。

### 原因 1

new17 删除模式文件投影为 `0100444`，uid=0/gid=0，Windows 认为文件只读，拒绝进入 Unlink 回调。

### 解决 1

`projectMode` 文件分支：new17 模式 → `S_IFREG | 0666`，其他受限模式保持 `0444`。

### 问题 2：0666 仍不够

`uid=0 gid=0 + 0666` 下，Linux/POSIX 世界权限为 `rw-rw-rw-`，但 Windows 可能对 uid=0 的文件有额外保护。

### 解决 2

已验证 0666 可通过。文件内容写入仍由 `isRestrictedMode()` → `Write/Truncate/Create` → EROFS 全程拒绝。

### Mock E2E 通过 + 真实云盘隔离目录测试通过

### 二进制

`hddsyncd.new17.exe` + `hddfs.new17.exe`

---

## 第 6 阶段：复制上传模式（new18）

### 目标

新增模式 `--mkdir-rename-move-file-rename-move-remove-copy-upload-only`，支持从本地复制文件到挂载目录（stage→write→commit 模式）。

### 时间

2026-07-06 晚上

### 核心设计：Staged Upload

```
Create/Open → createStagedUpload() → 创建本地临时 staging 文件
Write → 只写 staging（fs.isRestrictedMode() 限制下仅允许 staged handle）
Flush → 空操作（staged handle 不上传）
Release → commit 上传（fs.uploadStaged IPC）→ daemon → Provider.Upload → 后置校验 → 清理 staging
```

### 关键实现

- **IPC 协议**：新增 `fs.uploadStaged`（`FSUploadStagedRequest`/`FSUploadStagedResponse`）
- **daemon 端**：`handleFSUploadStaged()` — staging 路径安全校验（反穿透）、冲突策略（fail/overwrite/auto_rename）、上传、后置校验、清理
- **WinFsp 端**：`createStagedUpload()` — 创建临时 staging 文件 + staged handle
- **冲突策略**：fail（目标存在→EEXIST）、overwrite（目标存在→替换）、auto_rename（helper 生成新路径）

### 问题 1：PowerShell Copy-Item -Force overwrite 失败

`Copy-Item -Force` 对已有目标文件报"对路径的访问被拒绝"，daemon 无 `fs_upload_staged` 日志。

### 原因 1（第一轮分析）

`createStagedUpload` 中目标存在时无条件设置 `conflictPolicy="overwrite"`，但 Windows Explorer 的初始 probe 也会命中此逻辑，导致自动覆盖。

### 解决 1（第一轮）

恢复 `O_TRUNC` 判断：只有 `flags & os.O_TRUNC` 才允许 overwrite，否则返回 EEXIST。

### 问题 2：O_TRUNC 判断过窄

`Copy-Item -Force` 和 Explorer "替换目标中的文件" 实际不传 `O_TRUNC`，二者均被 EEXIST 拒绝。

### 原因 2

WinFsp 对于 `Create` 回调（Windows `CREATE_ALWAYS`）和 `Open` 回调（Windows `TRUNCATE_EXISTING`）的 flags 映射不完全等价于 POSIX `O_TRUNC`。仅依赖 `O_TRUNC` 无法覆盖所有显式替换场景。

### 解决 2

引入 `isCreate` 参数区分回调来源：

| 回调 | `isCreate` | 目标存在 | 行为 |
|---|---|---|---|
| `Create`（Windows CREATE_ALWAYS） | `true` | 是 | **overwrite**（Create 本就代表新建/替换） |
| `Open`（Windows TRUNCATE_EXISTING） | `false` + `O_TRUNC` | 是 | **overwrite**（显式截断标识） |
| `Open`（Explorer 初始 probe） | `false` 无 `O_TRUNC` | 是 | **EEXIST**（拒绝，触发冲突对话框） |

### 问题 3：PowerShell helper AutoRename 验证误报

`Copy-Item` 成功后立即 `Test-Path` 失败，helper 报"destination not found"。

### 原因 3

挂载盘可见性延迟：Write→Release→daemon upload 为异步链路，`Test-Path` 时 file handle 刚 Release 但目录缓存未刷新。

### 解决 3

增加重试验证：最多 5 秒，每 100ms `Test-Path` 一次。错误信息区分"Copy-Item failed"与"upload verification timeout"。

### 问题 4：Access W_OK 对 new18 模式阻断

`Copy-Item -Force` 在进入 Open/Create 之前被 `Access(W_OK)` 拒绝。

### 原因 4

`Access` 回调中 `W_OK` 检查分支：

```go
} else if fs.readOnly || ((restrictedList) && !isDir) {
    rc = -fuse.EROFS
}
```

`restrictedList` 未包含 new18 模式，但 `isDir` 条件导致非目录（文件）仍落入 EROFS。

### 解决 4

new18 模式的特殊处理：`DELETE_OK`（含 W_OK|DELETE_OK 组合）显式列入允许列表；文件 W_OK 单独通过（因为 new18 不在 EROFS 列表）。实际写入仍由 Write 回调的 `isRestrictedMode()` 拦截。

### Mock E2E 结果

- 新文件复制上传 ✓
- 默认同名拒绝 ✓
- helper Fail/AutoRename ✓
- helper Overwrite 待用户重建 EXE 后复测
- Explorer Replace/Skip/Keep both 待用户重建 EXE 后复测

### 二进制（用户手动构建）

`hddsyncd.new18.exe` + `hddfs.new18.exe`

---

## 经验总结

### 避免基于字符串白名单扩散权限

`dispatchFS` 原实现用 `switch pol.name` 逐模式列出允许的 IPC 请求类型。每新增一个模式就要在两处（daemon 和 hddfs）同步增加 case，极其容易遗漏。

**改进**：`policyAllowsRequest()` 按 capability 字段（`canRenameFile`、`canDeleteDir` 等）判断权限。新增模式时只需在 `writePolicyFromArgs` 中声明能力，无需修改 dispatch。

### 受限模式下写回调必须统一审计

新增模式后，仅改一处权限判断是不够的。所有写回调（Open、Create、Write、Truncate、Unlink、Rmdir、Chmod、Chown、Utimens、Setxattr、Removexattr）都曾出现仅检查 `readOnly || mkdirOnly` 而遗漏新模式的问题。

**改进**：引入 `isRestrictedMode()` 统一判断函数，所有写回调入口优先调用此函数。

### WinFsp 权限投影与实际写权限分离

文件 `mode=0444` 在 WinFsp 层阻止 Windows 进入 Rename/Unlink 回调，但实际内容写入由打开/写入回调控制。new17 删除模式需要 `mode=0666` 让 Windows 进入 Unlink，但内容写入仍需 EROFS。

**正确的分层**：
- `projectMode`：控制 Windows 侧的读写可见性（只影响 DELETE/Rename 能否进入回调）
- `Open/Create/Write`：控制实际内容写入（即使 mode=0666 也拒绝）
- `Access`：控制 DELETE_OK/W_OK 权限掩码

### 重视 E2E 测试反馈

`Copy-Item -Force` overwrite 失败暴露了三层问题：
1. `Access(W_OK)` 被拦截 → 增加 DELETE_OK 显式放行
2. `Open` 回调未路由到 staged handle → 增加 new18 写标志路由
3. `createStagedUpload` 无条件 overwrite → 引入 `isCreate` 参数区分 Create/Open

每一层都是单元测试无法覆盖的真实 Windows 行为差异。

### 写操作永不重试

所有写操作（fs.rename、fs.mkdir、fs.remove、fs.uploadStaged）严格执行"调用一次，不自动重试"原则。只读操作（fs.stat、fs.list、fs.open）允许最多一次 reconnect 重试。

---

## 文件变更总览

| 文件 | 主要变更 |
|---|---|
| `cmd/hddsyncd/main.go` | writePolicy、policyAllowsRequest、dispatchFS、handleFSRename*、handleFSRemove、handleFSUploadStaged、staging 校验 |
| `cmd/hddfs/main.go` | 全模式 CLI flag 解析、互斥校验、IPCFileSystemConfig 传递 |
| `internal/mount/winfsp/ipcfs.go` | isRestrictedMode、projectMode、Access、Open、Create、Write、Flush、Release、Truncate、Unlink、Rmdir、renameInFileRenameMode、createStagedUpload、ipcUploadStaged、ipcErrToFuse |
| `internal/ipc/protocol.go` | FSUploadStagedRequest/Response |
| `internal/cloud/mock/provider.go` | DirectRemoteProvider 实现（Move、Copy、UploadFile、UploadDirectory） |
| `cmd/hddsyncd/cachepath_test.go` | 各模式 dispatchFS 权限测试 |
| `cmd/hddsyncd/new18_upload_test.go` | new18 staged upload 专项测试 |
| `internal/mount/winfsp/ipcfs_test.go` | projectMode、isRestrictedMode、write 回调拒绝、error mapping、Unlink/Rmdir/Open 各模式测试 |
| `scripts/Copy-ToHddMount.ps1` | PowerShell copy-upload helper（-OnConflict Fail/Overwrite/AutoRename） |

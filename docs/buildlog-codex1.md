# 华电云盘 Windows 客户端开发报告（Codex1 部分）

## 1. 报告范围

本文只记录 Codex 在本轮会话中直接完成的工作，不代表项目的完整开发历史，也不认领其他编程体、用户或既有代码完成的功能。

本轮工作范围仅包括 WinFsp daemon 挂载的 `--mkdir-only` 模式：

- 修复 Windows 无法创建目录的问题；
- 修复首次根目录枚举偶发 I/O 错误；
- 保持 200 GiB 容量展示；
- 保证普通文件及其他修改操作仍被拒绝；
- 修复禁止删除时 Windows 错误返回成功的问题。

本轮没有访问真实华电云盘，没有执行真实远端写入，没有运行 `go test ./...`，也没有提交 Git。

## 2. 时间口径

- 时区：Asia/Shanghai（UTC+8）。
- 因本轮没有 Git 提交，时间主要依据构建进程、WinFsp 调试日志和人工验收日志确定。
- 开始时间是能够从本地记录确认的近似时间，不表示全部分析和思考的精确起点。

| 阶段 | 开始时间 | 结束时间 | 主要时间证据 |
|---|---:|---:|---|
| 阶段一：cgofuse/WinFsp 行为审计与根因定位 | 2026-07-05 20:16（约） | 2026-07-05 20:31 | Go 构建进程记录及首次 new8 挂载日志 |
| 阶段二：mkdir-only 目录创建与首次枚举修复 | 2026-07-05 20:31 | 2026-07-05 20:40（约） | new8、new8-retest 的 hddfs 日志和人工验收记录 |
| 阶段三：禁止写操作错误语义修复 | 2026-07-05 20:45（约） | 2026-07-05 21:01 | 自动测试记录及 new9 WinFsp 人工验收日志 |

## 3. 阶段一：cgofuse/WinFsp 行为审计与根因定位

### 目标

从本机实际使用的 cgofuse v1.6.0 源码和当前项目代码出发，确定 Windows 为什么在进入 `Mkdir` 回调前就拒绝目录创建，并排除错误假设。

### 遇到的问题

1. PowerShell `New-Item -ItemType Directory` 返回访问拒绝。
2. 调试日志只有 `Getattr`，没有 `Mkdir` 或 `Create`。
3. 既有代码曾尝试在 `Create` 中通过 `mode & 0x4000` 判断目录，但真实请求没有进入该分支。
4. 需要确认 mkdir-only 是否被错误地当作只读挂载，以及 `Access` 是否参与提前拒绝。

### 问题原因

1. `Getattr("/")` 和普通目录固定返回 `040555`，目录没有写权限。WinFsp 根据目录权限完成 Windows 写访问映射后，在调用目录创建回调前拒绝请求。
2. 挂载参数本身没有错误：只有 `readOnly=true` 才添加 `-o ro`，mkdir-only 没有添加 `ro`。
3. cgofuse v1.6.0 的 Windows 适配会把目录创建分派到 `Mkdir`；`Create` 用于普通文件创建，并会按普通文件语义补充 `S_IFREG`。因此在 `Create` 中猜测目录类型属于错误路径。
4. `IPCFileSystem` 当时没有覆盖 `Access`，继承的 `FileSystemBase.Access` 返回 `ENOSYS`。首次目录创建失败并不是项目自定义 `Access` 导致的。

### 最终解决办法

1. 使用 cgofuse 的 `S_IFDIR`、`S_IFREG` 和 `S_IFMT` 处理类型位，不再使用裸常量猜测目录类型。
2. 将权限投影按运行模式拆分：
   - read-only：目录 `040555`，文件 `0100444`；
   - mkdir-only：目录 `040777`，文件 `0100444`；
   - 普通可写模式：目录 `040755`，文件 `0100644`。
3. mkdir-only 的目录可写位只是虚拟文件系统向 WinFsp 暴露的权限；真正允许的操作仍由 FUSE 回调和 daemon allowlist 强制限制。
4. 增加 `Access` 实现，允许 mkdir-only 对目录执行创建子目录所需的读、遍历和写访问，同时拒绝普通文件写访问。

### 阶段结果

修复后真实 `Getattr("/")` 返回 `040777`，Windows 目录创建进入真实 `Mkdir` 回调，而不是 `Create`。人工日志记录的目录创建 mode 为 `0770`。

## 4. 阶段二：mkdir-only 目录创建与首次枚举修复

### 目标

在允许目录创建的同时，保持其他写操作被拒绝，解决第一次 `Get-ChildItem` 偶发 I/O 错误，并保持 200 GiB 容量展示不回退。

### 遇到的问题

1. 第一版权限修复后，目录创建已经成功，但首次 `Get-ChildItem P:\ -Force` 仍返回 I/O device error。
2. 后续 20 次根目录枚举全部成功，说明问题只出现在首次 IPC 读取。
3. 初始 `Readdir` 日志只记录 errno，不包含底层 IPC 错误，无法直接区分 daemon 错误、连接错误和数据错误。
4. `Readdir` 没有显式返回 `.` 和 `..` 项。

### 问题原因

1. `IPCFileSystem` 构造时查询 `fs.cacheDir`，提前建立了 named-pipe 连接。
2. WinFsp 完成挂载初始化期间该连接长时间闲置。首次 `fs.list` 使用陈旧连接时，底层返回 `The pipe is being closed`。
3. `IPCClient` 在传输失败后会清空连接，因此第二次枚举自动重连并成功，形成“第一次失败、以后正常”的稳定现象。
4. 缺少 `.` 和 `..` 是目录枚举协议完整性问题，但不是首次 EIO 的唯一根因。

### 最终解决办法

1. 对幂等的 `fs.list` 传输失败增加一次立即重连重试。
2. 不增加 sleep，不要求预热挂载，也不对 `fs.mkdir`、`fs.remove` 等写操作重试，避免重复远端修改。
3. `Readdir` 补充 `.` 和 `..`。
4. 增强以下回调日志，记录开始、结束、errno 和耗时：
   - `Getattr`；
   - `Readdir`；
   - `Statfs`；
   - `Access`；
   - `Mkdir`；
   - `Create`。
5. 保持容量参数：
   - `Bsize = 4096`；
   - `Frsize = 4096`；
   - `Blocks/Bfree/Bavail = 52428800`；
   - 总容量 `214748364800` bytes，即 200 GiB。

### 自动测试

新增或完善的测试覆盖：

- read-only、mkdir-only 和普通模式的目录/文件 mode；
- mkdir-only 目录写访问允许、普通文件写访问拒绝；
- read-only 写访问拒绝；
- read-only 挂载参数包含 `ro`，mkdir-only 和默认模式不包含 `ro`；
- mkdir-only 的 Create、Write、Rename、Unlink、Rmdir、Truncate 拒绝；
- `Readdir` 包含 `.`、`..`，并可连续执行 20 次。

### 人工 WinFsp Mock 验收

new8-retest 的实测结果：

- 第一次 `Get-ChildItem P:\ -Force` 成功；
- 空目录枚举成功；
- 连续 20 次枚举失败数为 0；
- `Get-PSDrive` 和 `cmd dir` 显示 200 GiB；
- `New-Item -ItemType Directory` 成功；
- Mock 物理目录出现；
- 真实请求进入 `Mkdir`，`ipcMkdir` 执行一次；
- 普通文件 `Create` 使用 flags `0x502`，返回 `EROFS`；
- Rename 被拒绝。

## 5. 阶段三：禁止写操作错误语义修复

### 目标

修复 `cmd /c rmdir P:\probe-dir` 返回退出码 0、但物理目录仍存在的错误成功语义。禁止操作必须向 Windows 调用方明确失败，并且不能触发 daemon 写方法。

### 遇到的问题

1. `IPCFileSystem.Rmdir` 已返回 `-fuse.EROFS`，但 `cmd rmdir` 仍返回 0。
2. PowerShell `Remove-Item` 没有稳定抛出错误，但 Mock 物理目录实际没有删除。
3. `Utimens` 原实现无条件返回 0，会对禁止修改给出虚假成功。
4. `Chmod`、`Chown`、`Setxattr`、`Removexattr` 没有 IPCFileSystem 的显式保护实现，依赖基类默认行为，错误语义不统一。

### 问题原因

cgofuse v1.6.0 在 Windows 上需要显式启用 `FileSystemHost.SetCapDeleteAccess(true)`，才能在 Windows 获取删除权限时调用 `Access(DELETE_OK)`。未启用该能力时，WinFsp 可能在删除流程中吞掉后续 `Rmdir/Unlink` 的错误，使应用层看到成功。

cgofuse 对该能力的契约是：

- `Access(DELETE_OK)` 对禁止删除返回 `EPERM`；
- 如果仍进入 `Unlink/Rmdir`，修改回调继续返回对应失败 errno。

### 最终解决办法

1. 只对实现了 delete-access 契约的 IPCFileSystem 挂载启用 `SetCapDeleteAccess(true)`，避免影响其他文件系统实现。
2. read-only 和 mkdir-only 模式下，`Access(DELETE_OK)` 返回 `-fuse.EPERM`，让 Windows 在获取删除权限阶段明确失败。
3. 以下实际修改回调统一返回 `-fuse.EROFS`：
   - `Rmdir`；
   - `Unlink`；
   - `Rename`；
   - `Truncate`；
   - `Chmod`；
   - `Chown`；
   - `Utimens`；
   - `Setxattr`；
   - `Removexattr`。
4. 新增统一拒绝日志，包含 PID、instanceID、operation、path、readOnly、mkdirOnly 和 returned errno。
5. 在 IPC 调用边界注入测试函数，验证禁止分支不会发送 `fs.remove` 等 daemon 写请求；允许的 `Mkdir` 仍调用真实 Mock Provider。

### 自动测试

测试结果包括：

- mkdir-only `Rmdir` 返回 `EROFS`；
- read-only `Rmdir` 返回 `EROFS`；
- Mock Provider `Remove` 调用次数为 0；
- `Unlink/Rename/Truncate/Chmod/Chown/Utimens/Setxattr/Removexattr` 明确失败；
- `Mkdir` 调用 Mock Provider 一次并成功；
- 普通文件 `Create` 返回 `EROFS`。

### 人工 WinFsp Mock 验收

new9 实测结果：

- `cmd /c rmdir P:\probe-dir` 输出 `Access is denied.`；
- `%ERRORLEVEL%`/PowerShell `$LASTEXITCODE` 为 `5`；
- `Remove-Item P:\probe-dir -ErrorAction Stop` 抛出访问拒绝；
- Mock 物理目录继续存在；
- 日志记录两次 `Access(DELETE_OK)`，均返回 `-EPERM`；
- daemon 日志中没有 `fs.remove` 请求。

## 6. 修改文件

本轮 Codex 直接修改或新增：

- `cmd/hddfs/main.go`；
- `cmd/hddfs/main_test.go`；
- `internal/mount/winfsp/ipcfs.go`；
- `internal/mount/winfsp/ipcfs_test.go`；
- `docs/buildlog-codex1.md`（本报告）。

工作区中其他已有改动不属于本报告认领范围。

## 7. 验证命令与结果

执行并通过：

```powershell
go test -p=1 -count=1 -timeout=120s ./cmd/hddfs/...
go test -p=1 -count=1 -timeout=120s ./internal/mount/winfsp/...
go test -p=1 -count=1 -timeout=120s ./cmd/hddsyncd/...
go test -p=1 -count=1 -timeout=120s ./cmd/hddctl/...
go test -p=1 -count=1 -timeout=120s ./internal/ipc/...
go test -p=1 -count=1 -timeout=120s ./internal/cloud/mock/...

go vet ./cmd/hddfs/... ./internal/mount/winfsp/... ./cmd/hddsyncd/... ./cmd/hddctl/... ./internal/ipc/... ./internal/cloud/mock/...

go build -o .\hddctl.new9.exe .\cmd\hddctl
go build -o .\hddsyncd.new9.exe .\cmd\hddsyncd
go build -o .\hddfs.new9.exe .\cmd\hddfs

git diff --check
```

未执行全仓 `go test ./...`，这是对应开发任务的明确限制。

## 8. 最终状态与限制

### 已完成

- mkdir-only 可以通过 PowerShell、资源管理器语义对应的 WinFsp 回调创建目录；
- 首次根目录枚举不再出现 I/O 错误；
- 200 GiB 容量展示保持不变；
- 普通文件和其他修改操作继续被拒绝；
- 禁止删除会向 CMD 和 PowerShell 明确返回失败，不再静默成功；
- 禁止操作不会调用 daemon 写方法。

### 仍需注意

- 本轮只使用 Mock Provider 和隔离 named pipe 验证，没有访问真实云盘；
- 没有扩展上传、大文件、Token Refresh、同步冲突、Windows service 或 WinFsp 其他文件操作能力；
- 工作区包含其他编程体留下的未提交修改，本报告没有审计或认领这些改动；
- 本轮没有 Git 提交，后续提交前仍应由集成负责人复核完整 diff。

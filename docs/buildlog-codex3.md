# 华电云盘 Windows 客户端开发报告（Codex 3 部分）

## 1. 报告范围

本文只记录 Codex 3 在“华电云盘直接远端操作”任务中完成的工作，不代表项目全部开发历史，也不认领其他编程体、用户或既有代码的成果。

本轮工作范围包括：

- 单文件上传冲突策略；
- 本地目录递归上传；
- 文件和目录复制；
- 文件和目录跨父目录移动；
- 对应的 `hddctl remote` 命令；
- 相关错误分类、缓存失效和自动测试。

本轮没有修改或实现 `hddsyncd`、`hddfs`、WinFsp、实时同步、任务队列、登录协议或 refreshToken 逻辑；没有访问真实华电云盘，没有修改真实 `metadata.db`，也没有提交 Git。

## 2. 时间说明

- 时区：Asia/Shanghai（UTC+8）。
- 开发日期：2026-07-04。
- 会话记录可以确认相关测试在 10:51 左右执行，但没有保存每次编辑动作的完整、独立时间戳。
- 下表中的分钟为根据会话内命令顺序和测试日志整理的约值，不应作为精确工时或 Git 审计时间。
- 当前工作区文件时间在后续环境处理中统一变为 2026-07-06，因此未用文件 `LastWriteTime` 反推阶段时间。

| 阶段 | 开始时间 | 结束时间 | 时间精度 |
|---|---:|---:|---|
| HAR 协议证据整理 | 2026-07-04 约 10:20 | 2026-07-04 约 10:33 | 会话顺序估计 |
| 现状检查与接口设计 | 2026-07-04 约 10:33 | 2026-07-04 约 10:42 | 会话顺序估计 |
| 单文件上传流程与冲突策略 | 2026-07-04 约 10:42 | 2026-07-04 约 10:55 | 含 10:51 测试日志 |
| 目录递归上传 | 2026-07-04 约 10:55 | 2026-07-04 约 11:08 | 会话顺序估计 |
| 文件/目录复制、移动与缓存失效 | 2026-07-04 约 11:08 | 2026-07-04 约 11:22 | 会话顺序估计 |
| CLI 接入与帮助文本 | 2026-07-04 约 11:22 | 2026-07-04 约 11:31 | 会话顺序估计 |
| 测试、安全收敛与最终验证 | 2026-07-04 约 11:31 | 2026-07-04 约 11:48 | 会话顺序估计 |

## 3. 阶段一：HAR 协议证据整理

### 目标

在修改代码之前，从 `D:\ncepupan-har` 中确认本轮上传、建议名称、目录创建、复制和移动接口的 endpoint、HTTP 方法、请求字段、`ondup` 值及响应特征，同时避免输出凭据和个人信息。

### 遇到的问题

1. HAR 文件体积较大，部分文件超过 20 MB。
2. 部分 HAR 不是严格合法的 JSON，字符串中存在未正确转义的换行，PowerShell `ConvertFrom-Json` 无法解析。
3. HAR 同时包含 Cookie、Authorization、Token、完整 DocID、用户信息和对象存储签名 URL，不能直接输出或复制到仓库。
4. `login.har` 中存在接口名称，但部分只是网页脚本字符串，不能当作真实网络调用证据。

### 原因

HAR 由浏览器开发工具生成，其中既有网络请求，也有页面脚本和大段响应内容；文件格式异常会让整体 JSON 解析失败。直接搜索上下文还可能把敏感头、个人信息或完整响应一并打印出来。

### 最终解决办法

1. 先按明确的 11 个目标 endpoint 进行只读筛选。
2. 不输出请求值，只提取来源文件、endpoint、方法、HTTP 状态和 `ondup` 等非敏感元数据。
3. 将仅出现在脚本中的 `login.har` 字符串排除出协议调用证据。
4. 整理出以下协议结论：
   - 秒传：`predupload` 后调用 `/file/dupload`；
   - 非秒传：`/file/osbeginupload`、对象存储上传、`/file/osendupload`；
   - 文件建议名和目录建议名分别由服务器接口返回；
   - 文件复制确认 `ondup=1/2`；
   - 目录复制只确认 `ondup=2`；
   - 文件和目录移动确认 `ondup=1`；
   - 业务码 `403002039` 表示同类型同名冲突。
5. HAR 没有复制进仓库，也没有加入 Git。

## 4. 阶段二：现状检查与接口设计

### 目标

检查现有 Provider、路径缓存和 CLI 实现，在不影响同步守护进程及其他 Provider 的前提下增加语义化的直接远端操作能力。

### 遇到的问题

1. 工作树开始时已有大量未提交修改，其中 `cmd/hddctl` 和 `internal/cloud/huadian` 与本轮范围直接重叠。
2. 原有 `cloud.Provider` 只有基础 `Upload`、`Mkdir`、`Rename` 等方法，没有冲突策略、复制和跨父目录移动语义。
3. 如果直接扩展基础 `cloud.Provider`，会迫使 mock、同步相关代码和其他 Provider 同时实现本轮 CLI 专用能力。
4. CLI 不应直接接触原始 `ondup` 数字。

### 原因

基础 Provider 同时服务于多个系统。直接向该接口加入所有直接远端操作，会扩大影响范围并违反本轮“不修改 hddsyncd、任务队列和同步系统”的边界。

### 最终解决办法

1. 保留原有 `cloud.Provider` 不变。
2. 新增独立的 `cloud.DirectRemoteProvider` 扩展接口，只用于 `hddctl remote`。
3. 新增语义类型：
   - `UploadConflictPolicy`；
   - `DirectoryUploadConflictPolicy`；
   - `TransferConflictPolicy`；
   - `UploadResult`；
   - `TransferResult`。
4. CLI 只传递 `fail`、`auto-rename`、`overwrite`、`merge` 等语义值，`ondup` 映射封装在 Huadian Provider 内部。
5. 保留旧 Provider 的默认上传兼容路径，避免破坏既有调用方。

## 5. 阶段三：单文件上传与冲突策略

### 目标

实现 `fail`、`auto-rename`、`overwrite` 三种策略，并严格区分秒传和非秒传流程。

### 遇到的问题

1. 原实现无论 `predupload.match` 为真或假，都调用 `osbeginupload` 和 `osendupload`。
2. 原实现没有 `dupload` 秒传分支。
3. 原实现没有完整的 `client_mtime`、CRC32、`csflevel` 和明确 `ondup` 字段。
4. 原实现把 401 和所有 403 统一映射为 `unauthorized`。
5. 对象存储客户端的底层错误可能在错误字符串中包含签名 URL。
6. 覆盖后服务器可能保留原有大小写名称，不能以请求名称作为最终名称。

### 原因

旧上传实现基于此前不完整的协议理解；HAR 后续确认秒传和非秒传是两条不同流程。HTTP 状态本身也不足以区分业务同名冲突和普通权限失败。

### 最终解决办法

1. 本地路径先通过 `Lstat` 验证为普通文件，再读取大小、mtime、MD5 和 CRC32。
2. `match=true` 时调用 `/api/efast/v1/file/dupload`：
   - `fail` 使用 `ondup=1`；
   - `overwrite` 使用 `ondup=3`；
   - `auto-rename` 先请求服务器建议名称，再使用 `ondup=1`。
3. `match=false` 时调用 `/file/osbeginupload`，只使用 begin 返回的授权请求、DocID 和 rev；对象存储只接受 HTTP 204，随后调用 `/file/osendupload`。
4. `osendupload` 失败分类为 `upload_finalize_failed`，对象存储失败分类为 `object_storage_upload_failed`。
5. 业务码 `403002039` 优先映射为 `already_exists`；401 映射为 `unauthorized`；其他 403 映射为 `forbidden`。
6. 上传后失效父目录缓存并重新 List，校验最终对象是文件、大小一致、可用时 rev 一致；覆盖时额外确认没有生成第二个大小写等价的同名对象。
7. 最终名称以服务器响应和重新 List 的结果为准。
8. 对象存储日志只记录固定的脱敏路径；构造请求和网络错误返回固定错误，不包含签名 URL。

## 6. 阶段四：本地目录递归上传

### 目标

实现 `hddctl remote upload-dir`，支持空目录、多级目录、Unicode/中文/空格名称，以及 `fail`、根目录 `auto-rename` 和 `merge`。

### 遇到的问题

1. AnyShare 没有单独的“上传目录”接口，必须由客户端组合目录创建和文件上传。
2. 本地目录可能包含 symlink、junction、reparse point、device 或其他特殊条目。
3. 中途失败后远端可能已经创建部分目录或文件，不能把错误伪装成完全失败或自动回滚。
4. merge 不能误实现为 mirror，不能删除远端独有内容。

### 原因

目录上传本质上是多步非事务操作。若允许重解析点或路径逃逸，可能上传同步根之外的文件；若错误地删除远端独有内容，则存在数据损失风险。

### 最终解决办法

1. 使用 `os.ReadDir` 并按名称稳定排序，第一版串行执行。
2. 使用 `Lstat` 检查条目，拒绝符号链接和非普通文件/目录；使用 `filepath.Rel` 确认每个条目仍位于本地根目录内。
3. 先创建目录，再上传文件；空目录也会执行 `/dir/create`。
4. `fail`：所有目录 `ondup=1`，文件使用上传 `fail`。
5. `auto-rename`：只对根目录调用 `/dir/getsuggestname`；根目录内部继续使用 `fail`。
6. `merge`：所有目录使用 `ondup=3`，文件使用 `overwrite`；不执行任何远端删除。
7. 每个文件上传后重新验证远端结果。
8. 中途失败返回 `partial_directory_upload`，并附带相对于本地根目录的具体失败路径，不把本地绝对路径写入安全日志。

## 7. 阶段五：复制、移动和缓存失效

### 目标

实现文件/目录服务端复制、同文档库跨父目录移动、冲突预检、后置验证和目录子树缓存失效。

### 遇到的问题

1. 文件复制和目录复制使用不同 endpoint，且目录复制只确认 `ondup=2`。
2. move 没有自动改名和覆盖协议证据。
3. 同父目录移动不应发送 API，应提示使用 rename。
4. 目录不能复制或移动到自身或其后代。
5. 目录移动后，缓存中整棵子树的父链和 DocID 关系可能过期。
6. 自动改名响应理论上可能缺少 `name`，不能由客户端自行推算后缀。

### 原因

服务端协议对文件和目录的冲突策略并不对称。路径缓存保存了完整路径到 DocID 的映射，目录跨父移动会使旧前缀下所有缓存失效。

### 最终解决办法

1. 操作前 Stat 源路径和目标路径，确认目标是目录，并解析两者 DocID。
2. 从 DocID 中比较文档库标识；不同文档库返回 `cross_doc_library_unsupported`。
3. 文件复制：
   - `fail` 先 List 目标目录，无冲突时调用 `/file/copy`、`ondup=1`；
   - `auto-rename` 调用 `/file/copy`、`ondup=2`，使用响应名称；响应缺名时通过重新 List 查找新增名称。
4. 目录复制：
   - `auto-rename` 调用 `/dir/copy`、`ondup=2`；
   - `fail` 不猜测未经 HAR 确认的 `ondup=1`，返回 `unsupported_conflict_policy_for_directory_copy`。
5. 文件和目录移动分别调用 `/file/move`、`/dir/move`，仅支持 `fail` 和 `ondup=1`。
6. 当前父目录等于目标目录时，在任何 HTTP 请求前返回 `invalid_move_same_parent`，并提示使用 `remote rename`。
7. 目录复制/移动到自身或后代分别返回 `invalid_copy_into_descendant` 或 `invalid_move_into_descendant`。
8. 操作后重新 Stat 新路径；移动还确认旧路径不存在。
9. 移动时失效旧路径、旧父目录、目标父目录和新路径；目录移动对旧路径和新路径执行前缀级失效，避免继续使用旧子树 DocID。

## 8. 阶段六：hddctl remote CLI

### 目标

实现并更新以下命令及帮助文本：

```text
hddctl remote upload <local-file> <remote-file> [--conflict fail|auto-rename|overwrite]
hddctl remote upload-dir <local-directory> <remote-parent-directory> [--conflict fail|auto-rename|merge]
hddctl remote copy <source-path> <destination-directory> [--conflict fail|auto-rename]
hddctl remote move <source-path> <destination-directory> [--conflict fail]
hddctl remote rename <old-path> <new-path>
```

### 遇到的问题

1. 原 `move` 命令直接返回“不支持跨父目录移动”。
2. Go 标准 `flag.FlagSet` 默认在遇到第一个位置参数后停止解析，无法自然支持需求示例中放在位置参数末尾的 `--conflict`。
3. 子命令 `--help` 如果在 Provider 初始化之后处理，可能在只查看帮助时触发认证加载或连接。
4. mock Provider 不实现本轮扩展能力，但旧的默认上传测试仍需保持兼容。

### 原因

原 CLI 只覆盖基础 Provider 方法，且标准 flag 的位置参数规则与本轮命令格式不完全一致。

### 最终解决办法

1. 增加 `upload-dir`、`copy` 和真实 `move` 分派。
2. 实现专用的 conflict 参数解析，支持：
   - `--conflict value`；
   - `--conflict=value`；
   - 选项位于位置参数之前或之后。
3. 在连接 Provider 前处理 `remote` 及子命令帮助。
4. 缺少参数、非法策略、Provider 不支持操作及远端错误均返回非零退出码。
5. 默认上传在 Provider 不支持扩展接口时仍可回退到旧 `Upload`；非默认冲突策略和新增操作明确返回 unsupported。
6. CLI 成功时只输出最终远端路径，不输出完整 DocID。

## 9. 阶段七：测试、安全收敛与最终验证

### 目标

使用本地 fake server、临时目录和 mock Provider 验证新增能力，不访问真实云盘，并执行用户指定的相关包检查。

### 遇到的问题

1. 首次测试无法使用默认 Go 构建缓存：`C:\Users\Ring\AppData\Local\go-build` 返回 `Access is denied`。
2. 旧测试仍假定对象存储成功状态为 200，并假定 `match=true` 继续调用 begin/end。
3. `gofmt` 在 Windows 环境中生成了一个未跟踪的 Provider 临时副本，普通删除受到权限限制。
4. 工作树包含大量其他编程体留下的未提交修改，不能清理、覆盖或纳入本轮成果。

### 原因

1. 当前沙箱只允许写工作区和临时目录，默认用户缓存目录不可写。
2. 原测试对应旧协议理解，与 HAR 新证据冲突。
3. Windows 文件替换和安全软件/权限控制使格式化工具的临时文件未能自动清理。
4. 多编程体共用同一工作树，Git 状态不能简单等同于本轮修改。

### 最终解决办法

1. 将本轮 `GOCACHE` 指向系统临时目录，没有在仓库创建运行时缓存。
2. 更新 fake server：对象存储返回 204，并增加 `dupload` 处理器。
3. 增加或更新测试，覆盖：
   - 秒传/非秒传分支；
   - `ondup=1/3`；
   - begin 返回的 DocID/rev 用于 finalize；
   - `403002039` 优先映射；
   - 普通 403 映射 forbidden；
   - CLI 各 conflict 策略、缺参和非法策略；
   - 同父目录移动不发送请求；
   - 目录自身/后代保护；
   - 子树缓存前缀失效；
   - 对象存储错误不泄露哨兵 Token 或签名 URL。
4. 对已确认由本轮工具产生的临时 Provider 副本，核验绝对路径位于仓库内后单独删除；未清理其他既有临时文件或用户修改。
5. 只执行需求允许的相关包检查，没有运行 `go test ./...`。

### 验证结果

以下命令通过：

```powershell
go test -count=1 ./cmd/hddctl/...
go test -count=1 ./internal/cloud/huadian/...
go vet ./cmd/hddctl/... ./internal/cloud/huadian/...
go build -o .\hddctl.exe .\cmd\hddctl
git diff --check
git status --short
git diff --stat
```

没有运行并发 race 测试，因为本轮没有新增后台 goroutine 或并发工作池；目录上传第一版明确采用串行执行。

## 10. Codex 3 修改的文件

- `internal/cloud/cloud.go`
- `internal/cloud/huadian/provider.go`
- `internal/cloud/huadian/provider_test.go`
- `cmd/hddctl/main.go`
- `cmd/hddctl/remote.go`
- `cmd/hddctl/remote_test.go`

说明：这些文件在本轮开始前已有部分未提交修改。本报告只描述 Codex 3 在本轮完成的增量，不表示文件全部内容均由 Codex 3 编写。

## 11. 未实现能力和剩余限制

因缺少 HAR 证据，本轮明确没有实现或宣称支持：

- move `ondup=2`；
- move 自动改名或覆盖；
- copy 覆盖或跳过；
- 目录 copy 的未确认 `ondup=1` 原子 fail；
- 其他未经确认的 `ondup` 数值；
- 跨文档库复制或移动；
- 批量 copy/move 接口；
- refreshToken 自动刷新；
- 大目录 copy 是否使用异步任务。

此外，本轮所有云端协议测试均基于 `httptest.Server` 和 fake Provider。Codex 3 没有执行真实上传、覆盖、复制、移动或删除，因此不能宣称真实华电云盘人工测试已经通过。

## 12. 建议的后续人工验证

应由具备授权的人员在专用测试文档库和可删除的测试目录中进行：

1. 分别上传可秒传和不可秒传的小文件。
2. 验证 `fail`、服务器自动改名和大小写不同的同名覆盖。
3. 上传含空目录、多级目录、中文、空格和 Unicode 名称的目录。
4. 验证 merge 保留远端独有对象。
5. 验证文件同目录自动改名复制、跨目录复制和目录递归复制。
6. 验证文件及目录跨父目录移动，并确认旧路径消失、新路径存在。
7. 验证同父目录 move 在客户端被拒绝，rename 仍可正常改名。
8. 每一步均通过重新 List/Stat 或网页界面核对最终名称、大小、rev 和同名对象数量。


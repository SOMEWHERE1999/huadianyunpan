# 华电云盘 Windows 客户端综合开发报告

## 1. 报告说明

本文按时间顺序汇总 `docs` 目录中以下六份开发日志：

- `buildlog-codex4.md`；
- `buildlog-codex3.md`；
- `buildlog-codex2.md`；
- `buildlog-codex1.md`；
- `buildlog-opencode.md`；
- `buildlog-codex.md`。

报告只归纳上述日志明确记载的工作，不把其他人工开发、既有代码或未记录工作归属于 Codex/OpenCode。日志中没有精确时分的阶段保留“日期未知时分”或“约值”，不根据当前文件时间伪造开发时间。

项目由多个编程体在同一工作树中连续开发。后续阶段曾修正前期实现，因此本文以时间顺序同时记录“当时方案”和“最终纠正”，不把已被后续推翻的结论描述为当前最终状态。

## 2. 总体时间线

| 顺序 | 阶段 | 编程体 | 开始时间 | 结束时间 |
|---:|---|---|---:|---:|
| 1 | 全仓库功能验收 | Codex | 2026-07-02（时分未记录） | 2026-07-02（时分未记录） |
| 2 | 敏感日志整改 | Codex | 2026-07-02（时分未记录） | 2026-07-02（时分未记录） |
| 3 | 真正 SQLite Store 与持久任务队列基础 | Codex | 2026-07-02（时分未记录） | 2026-07-02（时分未记录） |
| 4 | HAR 证据、远端 Provider 与 CLI 初版 | Codex | 2026-07-04 约 10:20 | 2026-07-04 约 11:48 |
| 5 | copy/move 冲突策略扩展 | Codex | 2026-07-04 约 18:45 | 2026-07-04 约 19:11 |
| 6 | 非秒传 multipart 初次修复与 upload-dir 收敛 | Codex | 2026-07-04 约 19:11 | 2026-07-04 约 19:39 |
| 7 | 基于 HAR/真实错误的上传协议纠正 | Codex | 2026-07-04 19:39 | 2026-07-04 约 20:18 |
| 8 | WinFsp mkdir-only 行为审计与根因定位 | Codex | 2026-07-05 约 20:16 | 2026-07-05 20:31 |
| 9 | mkdir-only 创建目录与首次枚举修复 | Codex | 2026-07-05 20:31 | 2026-07-05 约 20:40 |
| 10 | 禁止写操作错误语义修复 | Codex | 2026-07-05 约 20:45 | 2026-07-05 21:01 |
| 11 | new13：目录跨父移动 | OpenCode | 2026-07-06 之前（时分未记录） | 2026-07-06 之前（时分未记录） |
| 12 | new14：文件同父重命名 | OpenCode | 2026-07-06 上午 | 2026-07-06 上午 |
| 13 | new15：文件跨父移动 | OpenCode | 2026-07-06 上午 | 2026-07-06 上午 |
| 14 | new16：capability-based 权限分派 | OpenCode | 2026-07-06 中午 | 2026-07-06 中午 |
| 15 | new17：文件/目录删除模式 | OpenCode | 2026-07-06 下午 | 2026-07-06 下午 |
| 16 | new18：staged copy-upload | OpenCode | 2026-07-06 晚上（20:23 前） | 2026-07-06 20:23 前 |
| 17 | new18 默认覆盖安全修复 | Codex | 2026-07-06 20:23:01 | 2026-07-06 20:46:07 |
| 18 | new19 覆盖能力调查与安全收敛 | Codex | 2026-07-06 20:46:07 | 2026-07-06 20:51:39 |
| 19 | 外发 release 目录整理 | Codex | 2026-07-06 21:48:02 | 2026-07-06 约 22:14 |
| 20 | 单终端启动控制脚本 | Codex | 2026-07-06 22:14:15 | 2026-07-06 22:25:46 |

## 3. 2026-07-02：基础验收、安全与持久化

### 阶段 1：全仓库功能验收

- **编程体**：Codex
- **开始时间**：2026-07-02（具体时分未记录）
- **结束时间**：2026-07-02（具体时分未记录）
- **目标**：检查 CLI、认证、Huadian Provider、daemon、Named Pipe、Store、同步、过滤、缓存和 WinFsp 的真实完成状态，区分“有代码”“已接入”“自动测试通过”和“真实环境验证”。
- **遇到的问题**：附件中文乱码；WinFsp 包缺少 SDK 头文件；Named Pipe 测试访问被拒绝；默认 Go telemetry/build cache 不可写；文档与代码状态冲突；测试在仓库产生下载文件。
- **原因**：文本编码错误；环境缺少 `fuse_common.h`；受限进程无法建立测试管道；沙箱禁止写用户缓存目录；历史文档没有同步实现；下载测试默认输出到工作目录。
- **最终解决办法**：改用 UTF-8；把 WinFsp/Named Pipe 明确标记为环境阻塞；以入口、调用链、测试和运行结果作为证据；输出 `feature-status-report.md`、`remaining-work.md` 和 `demo-readiness.md`；不伪造全仓通过，也不擅自删除测试生成文件。

### 阶段 2：敏感日志整改

- **编程体**：Codex
- **开始时间**：2026-07-02（具体时分未记录）
- **结束时间**：2026-07-02（具体时分未记录）
- **目标**：建立白名单式安全日志，禁止 Cookie、Token、Authorization、OAuth/CAS 信息、完整 DocID、账号、响应正文、authrequest、签名 URL和完整本地路径泄漏。
- **遇到的问题**：秘密可能通过 error 间接进入日志；CDP 登录保留大量调试输出；Windows 路径被误判为 URL；日志分散在 stdout、stderr 和 `slog`；测试会触碰错误跟踪的 `.gocache`。
- **原因**：旧 HTTP 错误直接拼接响应正文；登录调试代码输出 Header/body；`C:\...` 的盘符被 `url.Parse` 当成 scheme；各模块日志方式不统一；构建缓存曾被纳入版本控制。
- **最终解决办法**：新增统一 `SecurityEvent`；路径做不可逆摘要并丢弃 query/fragment；Provider 只返回状态分类；仅含 `://` 的字符串按 URL 解析；用秘密哨兵同时捕获 stdout、stderr、`slog`；覆盖控制面和对象存储失败场景。

### 阶段 3：真正 SQLite Store

- **编程体**：Codex
- **开始时间**：2026-07-02（具体时分未记录）
- **结束时间**：2026-07-02（具体时分未记录）
- **目标**：将伪 SQLite 的 `.row` 文本存储替换为真正 SQLite，为持久任务队列、自动同步和 WinFsp 写回建立基础。
- **遇到的问题**：Go Proxy 连接失败；module cache 无写权限；Go 标准库缺少 `embed`；并发 claim 出现 `SQLITE_BUSY`；旧测试依赖文本 Store 私有方法；旧文件删除受权限限制；`go mod tidy` 需要额外元数据。
- **原因**：网络代理和沙箱限制；本机 Go 安装不完整；busy timeout 仅对单连接生效；旧测试耦合内部文本格式；Windows 文件权限/占用影响删除。
- **最终解决办法**：经授权使用 `goproxy.cn,direct`；选择纯 Go 的 `modernc.org/sqlite v1.34.0`；保留 migration 文件并在 Store 注册内容以绕开 `embed`；Store 最大连接数设为 1；任务领取使用事务、条件更新和唯一索引；支持任务去重、同路径串行、retry_wait、stale running 恢复和事务提交；补充并发、恢复、回滚和 Unicode 测试。

## 4. 2026-07-04：Huadian 直接远端操作

### 阶段 4：HAR 证据、Provider 与 CLI 初版

- **编程体**：Codex
- **开始时间**：2026-07-04 约 10:20
- **结束时间**：2026-07-04 约 11:48
- **目标**：实现单文件上传冲突策略、递归目录上传、文件/目录复制、跨父移动及相应 `hddctl remote` CLI。
- **遇到的问题**：HAR 大且部分不是严格 JSON；包含大量秘密和个人信息；原上传不区分秒传/非秒传；基础 Provider 没有直接远端操作语义；旧代码把 401/403 统一分类；目录移动需要整棵缓存前缀失效。
- **原因**：HAR 混合网络和脚本内容；早期协议理解不完整；基础 `cloud.Provider` 服务多个系统，不能直接扩大接口；路径缓存保存完整路径到 DocID 映射。
- **最终解决办法**：脱敏提取 HAR endpoint/ondup；新增独立 `DirectRemoteProvider` 和语义冲突类型；秒传走 `dupload`，非秒传走 begin/storage/end；实现建议名称、覆盖后验证、目录递归、copy/move 安全检查及缓存失效；CLI 支持 conflict 参数前置或后置；测试使用 fake server，不访问真实云盘。

### 阶段 5：copy/move 冲突策略扩展

- **编程体**：Codex
- **开始时间**：2026-07-04 约 18:45
- **结束时间**：2026-07-04 约 19:11
- **目标**：按文件/目录类型扩展 copy/move 的 fail、auto-rename、overwrite、merge，并用响应 DocID 定位最终对象。
- **遇到的问题**：原策略只有 fail/auto-rename；CLI move 只接受 fail；客户端同名预检阻止 overwrite/merge；文件和目录误用统一 ondup；后置验证及缓存失效不足。
- **原因**：早期协议假设过窄；文件和目录 ondup 语义不同；overwrite/merge 响应可能只含目标 DocID。
- **最终解决办法**：按源类型校验策略；文件 copy/move 映射 ondup 1/2/3，目录 copy 仅 ondup=2，目录 move fail/merge 映射 1/3；写后 fresh List 并按响应 DocID 验证；补全源/目标路径及前缀缓存失效。用户后续报告真实云盘 copy/move 测试通过。

### 阶段 6：multipart 初次修复与 upload-dir 收敛

- **编程体**：Codex
- **开始时间**：2026-07-04 约 19:11
- **结束时间**：2026-07-04 约 19:39
- **目标**：修复非秒传对象存储 POST 403，并把 upload-dir 收敛为根目录冲突立即失败、只允许 fail。
- **遇到的问题**：旧实现直接 POST 原始字节；把签名字段当 Header；固定外层 Content-Type；整体读文件入内存；自动测试通过但真实秒传仍 400、storage 仍 403。
- **原因**：HAR 实际使用 multipart/form-data；首轮 fake server 只验证“可解析”，没有严格复刻 CRC32 格式、MIME、字段顺序和浏览器/WAF Header。
- **最终解决办法**：首次引入 multipart 流式拼装、精确 Content-Length、独立 storage client、204 后才 finalize；upload-dir 固定 fail。真实反馈证明该轮仍不完整，随即进入下一轮纠正。

### 阶段 7：基于 HAR 和真实错误的上传协议纠正

- **编程体**：Codex
- **开始时间**：2026-07-04 19:39
- **结束时间**：2026-07-04 约 20:18
- **目标**：分别解决 dupload 400 和 storage POST 403，并建立严格 HAR 对照测试。
- **遇到的问题**：CRC32 使用十进制；file part MIME 固定为 octet-stream；缺少网页成功请求中的非认证浏览器/WAF Header；重定向和失败正文可能造成不确定状态或秘密泄漏。
- **原因**：HAR 要求 CRC32 为 8 位小写十六进制；签名表单字段与文件 part MIME 是不同概念；站点 WAF依赖部分浏览器 Header；标准 multipart 可解析不代表协议完全匹配。
- **最终解决办法**：CRC32 改为 `%08x`；加入原始 JSON golden 测试；严格保持 authrequest 字段值和顺序；按扩展名设置 file MIME；使用 WebKit 风格 boundary、精确长度和非认证浏览器 Header；禁止重定向；限制错误正文读取；根目录 create 冲突时不进入递归。自动测试通过，但报告明确要求真实云盘复验。

## 5. 2026-07-05：WinFsp mkdir-only 修复

### 阶段 8：cgofuse/WinFsp 行为审计

- **编程体**：Codex
- **开始时间**：2026-07-05 约 20:16
- **结束时间**：2026-07-05 20:31
- **目标**：定位 Windows 在进入 `Mkdir` 回调前就拒绝目录创建的原因。
- **遇到的问题**：PowerShell 创建目录报访问拒绝；日志只有 Getattr；代码错误尝试在 Create 中判断目录；需要排除只读挂载和 Access 影响。
- **原因**：mkdir-only 目录投影为 `040555`，WinFsp 在回调前拒绝写访问；cgofuse Windows 路径会将目录创建路由到 `Mkdir`，不是普通文件 `Create`。
- **最终解决办法**：使用 cgofuse 类型常量；read-only、mkdir-only、普通模式分别投影不同 mode；mkdir-only 目录改为 `040777`、文件保持只读；增加 Access 对目录创建所需权限的精确放行。

### 阶段 9：目录创建和首次枚举修复

- **编程体**：Codex
- **开始时间**：2026-07-05 20:31
- **结束时间**：2026-07-05 约 20:40
- **目标**：允许 mkdir-only 创建目录，同时修复首次 `Get-ChildItem` 偶发 EIO，并保持 200 GiB 容量展示。
- **遇到的问题**：首次枚举失败、后续连续成功；Readdir 日志信息不足；目录项缺少 `.` 和 `..`。
- **原因**：构造阶段的 `fs.cacheDir` 提前建立 named-pipe，挂载初始化后首次 `fs.list` 使用陈旧连接并得到 “pipe is being closed”；第二次自动重连才成功。
- **最终解决办法**：只对幂等 `fs.list` 增加一次立即重连；写操作绝不重试；Readdir 补 `.`/`..`；增强关键回调日志；保持 Statfs 的 200 GiB 参数。new8-retest 人工 Mock 验收通过。

### 阶段 10：禁止写操作错误语义

- **编程体**：Codex
- **开始时间**：2026-07-05 约 20:45
- **结束时间**：2026-07-05 21:01
- **目标**：禁止删除等操作时向 Windows 明确返回失败，避免调用方看到成功但远端/Mock 未变化。
- **遇到的问题**：Rmdir 已返回 EROFS，但 CMD 退出码仍为 0；Utimens 无条件成功；多个修改回调依赖基类，语义不一致。
- **原因**：未启用 cgofuse `SetCapDeleteAccess(true)`，Windows 删除权限流程可能吞掉后续回调错误。
- **最终解决办法**：启用 delete-access 契约；Access(DELETE_OK) 返回 EPERM；所有禁止修改回调显式返回 EROFS；增加统一拒绝日志和 IPC 边界测试。new9 人工 Mock 验收确认 CMD/PowerShell 均返回访问拒绝且 daemon 未收到 remove。

## 6. 2026-07-06：WinFsp 能力逐步开放（OpenCode）

### 阶段 11：new13 目录跨父移动

- **编程体**：OpenCode
- **开始时间**：2026-07-06 之前（具体时间未记录）
- **结束时间**：2026-07-06 之前（具体时间未记录）
- **目标**：在受限模式中支持目录跨父目录移动。
- **遇到的问题**：跨父调用 `Provider.Rename` 返回 nil，但旧路径仍在、新路径不存在，形成假成功。
- **原因**：Huadian Rename 只支持同父改名；跨父且 basename 相同相当于无操作。
- **最终解决办法**：同父继续 Rename，跨父改用 `DirectRemoteProvider.Move`；强制验证旧路径消失、新路径为目录；只读 IPC 可重连一次，写请求不重试。

### 阶段 12：new14 文件同父重命名

- **编程体**：OpenCode
- **开始时间**：2026-07-06 上午
- **结束时间**：2026-07-06 上午
- **目标**：允许普通文件同父目录重命名，并引入 capability-based `writePolicy`。
- **遇到的问题**：Explorer 文件重命名报访问拒绝。
- **原因**：Access(DELETE_OK) 的允许列表漏掉 new14 模式。
- **最终解决办法**：将新模式纳入 DELETE_OK；daemon 使用 Rename 并校验文件大小；文件继续以只读内容模式投影。

### 阶段 13：new15 文件跨父移动

- **编程体**：OpenCode
- **开始时间**：2026-07-06 上午
- **结束时间**：2026-07-06 上午
- **目标**：支持普通文件跨父、basename 不变的移动。
- **遇到的问题**：新模式下 fs.rename 直接返回只读文件系统，daemon 从未进入移动处理。
- **原因**：`dispatchFS` 使用模式名字符串白名单，新增模式时遗漏 case。
- **最终解决办法**：跨父文件调用 `DirectRemoteProvider.Move` 并验证旧/新状态；根本性权限问题在 new16 改为 capability 判断。

### 阶段 14：new16 capability-based 分派

- **编程体**：OpenCode
- **开始时间**：2026-07-06 中午
- **结束时间**：2026-07-06 中午
- **目标**：消除模式字符串白名单扩散造成的权限遗漏。
- **遇到的问题**：dispatch、projectMode、Open、Write、Truncate、删除和元数据回调分别维护模式列表，新模式容易漏改。
- **原因**：权限由 `pol.name` 字符串比较，而不是由能力字段决定。
- **最终解决办法**：新增 `policyAllowsRequest` 和统一 `isRestrictedMode()`；按 canRename/canMove/canDelete/canCopyUpload 判断；统一审计所有写回调；路径 basename 改用 `path.Base()`。

### 阶段 15：new17 删除模式

- **编程体**：OpenCode
- **开始时间**：2026-07-06 下午
- **结束时间**：2026-07-06 下午
- **目标**：允许删除普通文件和空目录，同时继续禁止内容写入。
- **遇到的问题**：文件投影为 `0444` 时 Windows 在进入 Unlink 前拒绝删除。
- **原因**：WinFsp 的权限投影影响 Windows 是否进入删除回调；内容写权限与删除回调权限需要分层控制。
- **最终解决办法**：new17 文件投影为 `0666` 以允许进入删除流程，真正的 Open/Create/Write/Truncate 仍由受限模式返回 EROFS；daemon Stat 类型后按 capability 调用 Remove 并后置验证。Mock E2E 和隔离真实目录测试通过。

### 阶段 16：new18 staged copy-upload

- **编程体**：OpenCode
- **开始时间**：2026-07-06 晚上（20:23 前）
- **结束时间**：2026-07-06 20:23 前
- **目标**：支持从本地向挂载盘复制文件，通过本地 staging 写入并在 Release 时提交远端上传。
- **遇到的问题**：Copy-Item/Explorer 覆盖流程受 Access、Open/Create flags 和目录缓存延迟影响；AutoRename 上传后立即 Test-Path 会误报；覆盖意图难以从 FUSE flags 判断。
- **原因**：Windows Create/Open 语义不能简单等价于 POSIX `O_TRUNC`；Access(W_OK) 可能提前阻断；Release 后远端可见性有延迟。
- **最终解决办法（当时方案）**：新增 `fs.uploadStaged` IPC；Create/Open 写 staging、Release 上传；引入 `isCreate` 与 O_TRUNC 区分覆盖；helper 最多等待 5 秒验证；放行 new18 所需 Access。该阶段的“existing Create 可 overwrite”随后被 Codex 判定有数据损失风险并撤销。

## 7. 2026-07-06：覆盖安全收敛与发布准备（Codex）

### 阶段 17：new18 默认覆盖安全修复

- **编程体**：Codex
- **开始时间**：2026-07-06 20:23:01
- **结束时间**：2026-07-06 20:46:07
- **目标**：消除 Explorer/PowerShell 普通同名探测被误判为明确覆盖、从而自动覆盖远端文件的风险。
- **遇到的问题**：new18 使用 `destination exists && isCreate => overwrite`；普通冲突探测也可能进入 Create。
- **原因**：FUSE Create 不能作为 Windows CREATE_ALWAYS/Replace 的充分证据；当前 cgofuse 没有暴露 create disposition/options。
- **最终解决办法**：集中 `isExplicitOverwriteRequest`；existing Create 默认 EEXIST；仅 Open(existing)+明确 O_TRUNC 允许 staged overwrite；其他写 flags、W_OK、isCreate 均不能单独触发覆盖；增加决策日志和回归测试；helper 非 Overwrite 不再无条件 Force，成功后比较 SHA256。

### 阶段 18：new19 覆盖能力调查与 fail-closed

- **编程体**：Codex
- **开始时间**：2026-07-06 20:46:07
- **结束时间**：2026-07-06 20:51:39
- **目标**：调查 Explorer Replace、Copy-Item -Force 和 helper Overwrite 能否在不牺牲默认安全的前提下支持。
- **遇到的问题**：三类覆盖仍失败；缺少实际 Replace debug log；Go 回调拿不到 Windows disposition/options/access mask；曾尝试放宽 Create+O_TRUNC，但证据不足。
- **原因**：普通冲突探测和显式 Replace 的可靠区别位于 WinFsp 原始 disposition，而 cgofuse v1.6.0 没有把这些字段暴露给 Go；混用 `os.O_EXCL` 与 `fuse.O_EXCL` 也会误判。
- **最终解决办法**：保持 fail-closed；只认 Open(existing)+O_TRUNC；日志明确 disposition/options/accessMask unavailable；使用 cgofuse 常量域；不宣称 Explorer Replace、Copy-Item -Force 覆盖、helper Overwrite或在线编辑已经支持。后续需要扩展/fork 绑定或取得可靠回调证据。

### 阶段 19：外发 release 目录整理

- **编程体**：Codex
- **开始时间**：2026-07-06 21:48:02
- **结束时间**：2026-07-06 约 22:14
- **目标**：创建不含 EXE、凭据、数据库、真实日志或缓存的对外发行目录和使用文档。
- **遇到的问题**：脏工作树和 `.gocache` 噪声；补丁载体与 Markdown/PowerShell 反引号冲突；Windows PowerShell 5 误解码无 BOM UTF-8 中文脚本。
- **原因**：release 为未跟踪目录；补丁模板字符串需要转义反引号；PowerShell 5 对无 BOM UTF-8 兼容性差。
- **最终解决办法**：用补丁创建独立 release 结构；代码示例改用缩进；可执行占位脚本使用 ASCII；补充 README、配置、快速开始、限制、测试、排错和安全 helper；确认无 EXE、metadata.db、缓存和测试目录。

### 阶段 20：单终端启动控制脚本

- **编程体**：Codex
- **开始时间**：2026-07-06 22:14:15
- **结束时间**：2026-07-06 22:25:46
- **目标**：通过一个可见 PowerShell 终端完成环境检查、登录探测、隐藏启动 daemon/hddfs、打开挂载盘和最终停止控制。
- **遇到的问题**：初版产生多个窗口；隐藏子进程后父脚本会退出；直接按 Enter 不能误触发停止。
- **原因**：`Start-Process` 窗口策略、父脚本生命周期和 Read-Host 交互是独立问题。
- **最终解决办法**：start.ps1 保持为唯一控制终端；两个后台进程用 Hidden 并重定向日志；登录和 remote ls 在当前终端执行；挂载后打开 Explorer；空输入只重复提示，只有输入完整停止命令才停止进程，输入 EXIT 才结束终端。该脚本只完成静态检查，未执行真实登录和挂载测试。

## 8. 关键技术演进总结

### 8.1 从状态不明到可审计

首先通过全仓验收明确“代码存在、入口接入、自动测试、真实验证”四个层次，并把 WinFsp SDK、Named Pipe 权限等环境阻塞与代码回归分开报告。

### 8.2 从伪持久化到真正 SQLite

文本 `.row` Store 被替换为真正 SQLite，加入 schema migration、WAL、任务去重、事务领取、同路径串行、重试恢复和崩溃恢复，为 daemon 后续装配建立基础。

### 8.3 从粗略协议模拟到 HAR 严格对照

Huadian 上传经历“初版流程”“multipart 可解析”“真实错误反馈”“HAR 原始形态严格测试”四步。最终认识是：fake server 成功不等于真实协议正确，CRC32 格式、multipart 字段顺序、file MIME、Header 和重定向策略都必须有证据。

### 8.4 从模式字符串到 capability 权限

WinFsp 权限由不断扩散的模式名白名单改为 `writePolicy` capability；`dispatchFS`、投影 mode 和写回调统一按能力判断，降低新增模式漏改风险。

### 8.5 从“尽量支持覆盖”转向 fail-closed

OpenCode new18 曾将 existing Create 视为覆盖信号；后续 Codex 根据数据损失风险撤销该判断。当前安全结论是：没有 WinFsp disposition/options 证据时，普通 Create 必须 EEXIST，宁可显式 Replace 暂不可用，也不能自动覆盖远端文件。

## 9. 当前明确限制

根据六份日志，以下事项不能描述为已经完整解决：

1. Explorer Replace、Copy-Item -Force 覆盖、helper Overwrite 和在线编辑已有远端文件仍不受支持。
2. start.ps1 只经过静态检查，尚未由 Codex 执行真实登录、daemon 启动和 WinFsp 挂载验收。
3. 2026-07-04 最终上传纠正虽然有严格自动测试，日志仍要求隔离真实云盘复验。
4. SQLite Store 已实现，但将其完整装配进 daemon 的真实 watcher→task→worker→Provider 链仍属于后续工作。
5. WinFsp SDK 和 Named Pipe 在部分受限环境中仍可能阻塞全仓 test/vet/build。
6. 多编程体共享脏工作树，正式发布前应从干净工作树复核完整 diff、重新构建并生成 EXE 哈希。

## 10. 来源归属

- **Codex**：全仓验收、安全日志、SQLite Store、Huadian 直接远端操作及上传协议纠正、mkdir-only 修复、覆盖安全收敛、release 文档和单终端控制脚本。
- **OpenCode**：new13—new18 的 WinFsp 能力扩展，包括目录/文件移动、文件重命名、capability 分派、删除模式和 staged copy-upload 初版。
- **用户/人工验证**：日志中明确注明的真实云盘、WinFsp Mock 或隔离目录测试结果仅作为外部验证证据，不归属于 Codex/OpenCode 自动执行。


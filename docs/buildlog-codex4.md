# 华电云盘客户端开发报告（Codex 负责部分）

## 说明

本报告仅记录本次协作中由 Codex 实际承担的工作，不覆盖其他编程智能体或人工开发者完成的功能。

时间依据 Codex 会话日期整理。会话没有单独记录每项工作的精确时分，因此以下只写可靠的日期范围，不推测具体小时和分钟。

Codex 负责的工作主要包括：

1. 全仓库功能验收与状态报告；
2. 第一阶段安全日志整改；
3. 第二阶段真正 SQLite Store 与持久任务队列基础实现。

## 阶段一：全仓库功能验收

### 开发目标

- 全面检查 CLI、认证、Huadian Provider、daemon、Named Pipe、Store、同步、过滤、缓存和 WinFsp 的实际完成状态；
- 区分“存在代码”“已经接入入口”“通过自动测试”和“经过真实环境验证”；
- 追踪登录、远端目录、后台同步及 WinFsp 读写调用链；
- 生成后续开发所需的功能状态、剩余工作和演示准备度报告。

### 时间

- 开始日期：2026-07-02；
- 结束日期：2026-07-02；
- 具体时分：未单独记录。

### 完成内容

- 阅读项目入口、核心模块、测试和设计文档；
- 执行 `go list ./...`、`gofmt -l .`、`go test ./...`、`go test -race ./...`、`go vet ./...` 和 `go build ./cmd/...`；
- 生成以下报告：
  - `docs/feature-status-report.md`；
  - `docs/remaining-work.md`；
  - `docs/demo-readiness.md`；
- 发现原 `internal/store/sqlite` 实际是 `.row` 文本文件存储，并不是真正 SQLite；
- 确认 daemon 当时仍硬编码 Mock Provider，尚未装配认证、持久任务、watcher 和真实 worker 链；
- 确认 Huadian Provider 已有完整 API 流程及 `httptest.Server` 测试，但最初缺少本次会话内的真实环境证据。

### 遇到的问题

1. 附件首次读取出现中文乱码；
2. WinFsp 相关包无法编译；
3. Named Pipe 测试无法建立连接；
4. Go telemetry 和默认 build cache 目录没有写权限；
5. 既有文档、README 与实际代码状态存在冲突；
6. 测试命令在仓库内产生了未跟踪下载文件。

### 问题原因

1. PowerShell 首次使用了错误的文本编码；
2. 当前环境缺少 WinFsp SDK 头文件 `fuse_common.h`；
3. 当前受限 Windows 进程调用 `CreateFileW` 访问测试 Named Pipe 时返回 `Access is denied`；
4. 沙箱不允许写入用户目录下的 Go telemetry 和默认构建缓存；
5. 文档包含历史设计、旧审计结果和未同步更新的里程碑，不能直接作为完成证据；
6. CLI 下载测试的默认路径落在工作目录下。

### 最终解决办法

- 使用 UTF-8 重新读取附件；
- 将 WinFsp 和 Named Pipe 失败明确标记为环境阻塞，不伪造通过结果；
- 以实际入口、调用链、测试和运行输出为验收依据；
- 对真实云服务只作证据判定，不使用真实凭据执行危险操作；
- 将发现的问题按 P0—P3 写入剩余工作清单；
- 保留测试生成文件并在报告中披露，没有擅自删除用户文件。

## 阶段二：敏感日志整改

### 开发目标

- 建立统一的安全日志辅助层；
- 日志只允许使用以下业务字段：
  - `operation`；
  - `method`；
  - `redacted_path`；
  - `status`；
  - `request_id`；
  - `error_class`；
- 禁止输出 Cookie、Token、Authorization、OAuth Code、CAS Ticket、RootDocID、UserID、Account、响应正文、POST body、authrequest、签名 URL、URL query 和完整本地路径；
- 保持已经真实验证的登录、列目录、上传、下载和创建目录行为不变；
- 为敏感信息泄漏增加自动回归测试。

### 时间

- 开始日期：2026-07-02；
- 结束日期：2026-07-02；
- 具体时分：未单独记录。

### 完成内容

- 新增 `internal/logging/security.go`，集中生成安全结构化日志；
- 对路径或 URL path 计算不可逆摘要，丢弃 query 和 fragment；
- 清理 Huadian Provider 中以下输出：
  - 401、403、404、500 响应正文；
  - `dir/list` 原始响应；
  - 下载 `authrequest`；
  - 对象存储临时签名 URL；
  - 对象存储失败响应正文；
- 清理认证模块中的 Cookie 元数据、RootDocID、账号、UserID、POST body、请求 Header 和完整浏览器 API URL；
- 将 daemon、watcher 和 worker 中的完整路径及原始错误日志改为安全事件；
- 保留 Cookie、Token 和 RootDocID 的内部捕获、持久化和请求注入逻辑；
- 新增安全日志与 Provider 秘密哨兵测试。

### 遇到的问题

1. 部分敏感信息不是直接日志，而是被拼进返回 error，随后可能被上层记录；
2. CDP 登录代码包含大量临时诊断输出；
3. Windows 路径被 `url.Parse` 误判为带 scheme 的 URL；
4. 测试必须同时观察 stdout、stderr 和 `slog`；
5. 全仓库测试仍受 WinFsp 和 Named Pipe 环境影响；
6. Go 测试更新了仓库中被错误跟踪的 `.gocache` 文件。

### 问题原因

1. 旧代码在 HTTP 错误处理中读取响应正文并直接格式化到错误文本；
2. CDP 调试阶段为了分析协议，曾输出完整 URL、Header、POST body 和目录响应；
3. `C:\...` 中的盘符冒号会被通用 URL 解析逻辑识别为 scheme；
4. 不同模块混用了 `fmt`、stderr 和默认 `slog`；
5. WinFsp SDK 和 Named Pipe 权限问题与本阶段代码无关；
6. 项目历史上把构建缓存提交进了版本控制。

### 最终解决办法

- Provider 错误只保留状态分类，不再包含远端响应正文；
- 引入字段白名单式 `SecurityEvent`；
- 仅在字符串包含 `://` 时按 URL 处理，否则按本地路径处理；
- 测试中重定向 stdout、stderr，并将 `slog` handler 指向捕获流；
- 使用包含 Cookie、Token、Authorization、OAuth Code、CAS Ticket、RootDocID、UserID、Account、响应正文和签名参数的秘密哨兵；
- 覆盖 401、403、404、500、对象存储上传失败和下载失败；
- 单独报告非 WinFsp 包通过情况，不将环境失败描述为代码通过。

## 阶段三：真正 SQLite Store

### 开发目标

- 将 `internal/store/sqlite` 从 `.row` 文本文件替换为真正 SQLite；
- 为 daemon、自动同步和 WinFsp 写回提供可靠持久任务队列基础；
- 建立 `files`、`tasks`、`sync_roots`、`conflicts` 和 `settings`；
- 支持 upload、download、mkdir、remove、rename；
- 支持 pending、running、retry_wait、succeeded、failed；
- 保持现有 Store 对外接口，避免本阶段修改 daemon 或 WinFsp；
- 实现事务领取、任务去重、同路径串行、重试恢复和崩溃恢复。

### 时间

- 开始日期：2026-07-02；
- 结束日期：2026-07-02；
- 具体时分：未单独记录。

说明：SQLite 实施发生在 2026-07-02 的前序会话中，本报告整理日期为 2026-07-06；会话没有提供比日期更精确的时分记录。

### 驱动与设计

- 选择 `modernc.org/sqlite v1.34.0`；
- 使用纯 Go 驱动，避免新增 SQLite DLL 或额外 CGO 依赖；
- 数据库文件名为 `metadata.db`，由 `Open(dir)` 创建在调用方指定的用户数据或测试临时目录；
- 启用：
  - foreign keys；
  - WAL；
  - `synchronous=NORMAL`；
  - busy timeout；
- 使用 `schema_migrations` 管理迁移版本；
- 增加 `internal/store/sqlite/migrations/0001_initial.sql`。

### 完成内容

- 删除原文本 `.row` Store 运行实现及其旧备份源文件；
- 引入真正的 SQLite schema 和 CRUD；
- 保留原有 `Store`、`TaskRow`、`FileRow`、`SyncRootRow` 和 `ConflictRow` 主要接口；
- 将旧操作名 `delete` 兼容映射为数据库操作 `remove`；
- 将旧的 `failed + next_retry_at` 兼容映射为 `retry_wait`；
- 任务领取使用显式事务和条件更新；
- 使用 partial unique index限制同一 canonical path 同时只有一个 running 任务；
- 使用 active task unique index实现任务去重；
- 支持到期 retry_wait 恢复为 pending；
- 支持启动时将超时 running 恢复为 pending；
- 增加任务成功与文件元数据共同提交的事务方法；
- 数据库关闭后所有操作正常返回错误，不静默忽略。

### 遇到的问题

1. 首次下载 SQLite 驱动时官方 Go Proxy 无法连接；
2. 依赖下载随后又遇到用户 Go module cache 写权限问题；
3. 使用 `embed` 时当前 Go 安装报告标准库目录缺少 `embed`；
4. 初版并发 claim 测试出现 `SQLITE_BUSY`；
5. 旧测试依赖文本 Store 私有方法 `updateRow`；
6. 补丁工具和普通权限均无法删除旧 `store.go`；
7. `go mod tidy` 需要额外下载驱动的测试依赖元数据。

### 问题原因

1. 当前环境的默认代理指向不可用连接，官方代理的网络请求超时；
2. 沙箱禁止写入 `C:\Users\Ring\go\pkg\mod` 的部分目录；
3. 当前 Go 工具链虽然报告 Go 1.24.5，但 `C:\go\src` 的标准库文件不完整；
4. busy timeout PRAGMA 只对执行它的连接生效，多连接同时写入仍可能立即返回 busy；
5. 原测试直接修改 `.row` 文件内部状态，不是公共接口测试；
6. 文件删除受到 Windows 文件权限或占用限制；
7. `go mod tidy` 会解析依赖模块的测试依赖。

### 最终解决办法

- 经用户授权后改用 `https://goproxy.cn,direct` 下载同一已批准依赖；
- 质量检查使用系统临时目录作为 `GOCACHE`，避免把新缓存写进仓库；
- 保留独立 SQL migration 文件，同时在 Store 中注册迁移内容，绕开不完整标准库的 `embed` 问题；
- 将 SQLite Store 最大连接数设为 1，在 Store 层提供确定性写串行；worker 并发仍位于 Store 之上；
- 任务领取继续使用显式事务、条件 UPDATE 和 unique index作为多重并发保护；
- 将旧 stale running 测试改为直接设置 SQLite 测试数据并调用正式恢复接口；
- 在验证绝对路径属于仓库后，经提升权限删除旧文本 Store；
- 提升权限完成 `go mod tidy`。

### 新增或强化的测试

- migration 初始化；
- migration 重复运行；
- 五张业务表存在性；
- 原 Store CRUD 兼容测试；
- 32 个 goroutine 并发领取同一任务；
- 同 canonical path 串行；
- 事务失败回滚；
- stale running 恢复；
- retry_wait 到期恢复；
- active task 去重；
- 数据库关闭后的错误传播；
- Unicode、中文和空格路径；
- 数据库关闭并重新打开后的任务恢复；
- upload、download、mkdir、remove、rename 五种操作。

## 质量检查结果

### 已通过

- `gofmt -w .`；
- SQLite Store 普通测试；
- SQLite Store race 测试；
- SQLite Store vet；
- `hddctl`、`hddsyncd`、sync、watch、worker 与 SQLite 的兼容测试；
- 非 WinFsp 相关包的普通测试、race 和 vet；
- `git diff --check`。

### 环境阻塞

- `go test ./...`：`hddfs` 和 `internal/mount/winfsp` 因缺少 `fuse_common.h` 失败；
- Named Pipe 测试在当前受限进程中因 `CreateFileW: Access is denied` 失败；
- `go vet ./...` 和 `go build ./cmd/...` 同样被 WinFsp SDK 头文件问题阻塞。

上述失败不属于 SQLite 或安全日志代码回归，且没有被描述为通过。

## 当前遗留工作

以下工作不是 Codex 在上述阶段中已经完成的内容：

- 将真正 SQLite Store 装配进 `hddsyncd` 启动流程；
- daemon 加载 Credential、Huadian Provider、同步根和恢复任务；
- watcher → filter → SQLite task → worker → Huadian Provider 的真实单向同步；
- WinFsp SDK、Named Pipe 环境和 IPCFS 写回链；
- 大文件流式上传，当前 Provider 仍可能把内容完整读入内存；
- 仓库中历史 HAR、构建缓存和数字后缀备份的系统性清理。

## 总结

Codex 在本项目中完成了从“状态不明确”到“有证据的验收报告”、从“可能泄漏真实会话信息”到“字段白名单安全日志”，以及从“伪 SQLite 文本 Store”到“真正 SQLite 持久任务队列基础”的三项工作。

这些工作没有完成整个自动同步客户端，但已经为下一阶段的真实 daemon 装配、可靠单向同步和 WinFsp 写回提供了安全和持久化基础。

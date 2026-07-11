# 功能验收状态报告

审查日期：2026-07-02  
审查范围：仓库代码、测试、文档、构建入口与本机安全验证；未使用真实凭据，未对真实华电云盘执行操作。

## 执行摘要

当前代码可列出全部 Go 包，`hddctl`、`hddsyncd` 及多数非 WinFsp 包可编译并通过测试，但仓库级 `test/race/vet/build` 均因 WinFsp C 头文件缺失而失败，Named Pipe 测试还受当前进程权限阻塞。三个旧 `.exe` 位于工作区但被 `.gitignore` 忽略，不能作为本次源码构建成功证据。

登录、Cookie/Token/RootDocID 保存和 Huadian 文件 API 均有实现；自动证据只到加密文件存储和 `httptest.Server`。没有真实登录、跨进程 `dir/list`、上传或下载成功证据，因此“`user/get` 200 后新进程 `dir/list` 403”只能判定为**无法验证是否修复**，不能判定已修复。

自动同步仍仅在 `hddctl sync run` 中以临时 Mock Provider 串接 watcher、SQLite 和 worker；daemon 自身尚未加载 Session、同步根、任务、watcher 或 worker。SQLite Store 已替换为真正数据库并通过迁移、事务领取、并发、恢复和重启测试。WinFsp 有 memfs、直接 Mock CloudFS 和 IPCFS 三种代码路径；IPCFS 后端 daemon 仍为 Mock，当前环境不能重新构建或挂载。

| 能力 | 结论 |
| --- | --- |
| 构建 | 部分：非 WinFsp 包可构建；全量构建失败 |
| 运行测试 | 部分：多数包通过；WinFsp 构建和 Named Pipe 测试失败 |
| 完成登录 | 已实现但尚未验证（WebView2/CDP 依赖人工环境） |
| 跨进程加载 Session | 文件级实现并有测试；真实会话未验证 |
| 列出真实云盘目录 | 已实现但尚未验证；历史 403 未闭环 |
| 上传/下载 | API 流程有 httptest；真实环境未验证 |
| 自动同步 | 仅 Mock、单向本地变更上传链路，daemon 未接入 |
| WinFsp 挂载 | 被 WinFsp 头文件/运行环境阻塞 |
| Explorer 安全读写 | 未验证；后端只接 Mock daemon |
| 正常卸载 | 有代码和旧文档声明，但本次无法验证 |

## 工程检查结果

| 命令 | 执行 | 结果 | 摘要 | 分类/影响 |
| --- | --- | --- | --- | --- |
| `git status --short` | 是 | 成功 | 执行前工作树无变更 | 基线 |
| `go env GOOS GOARCH CGO_ENABLED` | 是 | 成功 | `windows/amd64/1`；另有 Go telemetry 用户目录拒绝访问警告 | 环境 |
| `go list ./...` | 是 | 成功 | 列出 24 个包；同样有 telemetry 警告 | 环境，不影响包发现 |
| `gofmt -l .` | 是 | 完成、有输出 | `internal/cloud/anyshare/auth/login_cdp.go` 未格式化 | 代码质量；按要求未修改 |
| `go test ./...` | 是 | 失败 | 多数包通过；WinFsp 缺 `fuse_common.h`；7 个 npipe 用例 `CreateFileW: Access is denied` | WinFsp/IPC 环境阻塞 |
| `go test -race ./...` | 是 | 失败 | 通过包未报告 race；失败边界同上 | 无法给全仓库竞态结论 |
| `go vet ./...` | 是 | 失败 | WinFsp C 头文件缺失 | 全仓 vet 未完成 |
| `go build ./cmd/...` | 是 | 失败 | `hddfs` 依赖 cgofuse/WinFsp 头文件；不能证明三入口可从源码全量构建 | 工程/WinFsp 阻塞 |

Go 还无法写入 `%APPDATA%/go/telemetry` 和 `%LOCALAPPDATA%/go-build/trim.txt`，属于本次受限执行环境问题。未安装依赖，未修改业务代码。

## 功能状态总表

| 编号 | 功能 | 状态 | 代码证据 | 测试证据 | 真实环境证据 | 阻塞因素 | 下一步 |
| -- | -- | -- | ---- | ---- | ------ | ---- | --- |
| 1 | CLI help/version | 已完成并通过自动测试 | `cmd/hddctl/main.go` 入口已接通 | hddctl 包通过 | 无需云环境 | 无 | 补入口快照测试 |
| 2 | CLI login/logout/auth status | 已实现但尚未验证 | `cmd/hddctl/main.go:237-289` 接 SessionManager | auth 单测通过 | 无本次真实登录证据 | WebView2/CDP、人工认证 | Windows 人工只读验收 |
| 3 | CLI auth diagnose | 未实现 | `cmdAuth` 只接受 `status` | 无 | 无 | 代码缺失 | 增加不泄密诊断命令 |
| 4 | CLI remote 七项操作 | 已实现但尚未验证 | `cmd/hddctl/main.go:134-231` 默认接 Huadian Provider | CLI 21 个 Mock 测试通过 | 无真实云证据 | VPN/凭据/历史 403 | 对真实只读先验收，再分项写入验收 |
| 5 | CLI sync add/run/status | 部分完成 | `cmd/hddctl/sync.go`；run 强制临时 Mock | 相关 store/watch/worker 测试通过 | 无 | 真实 Provider 未接入 | 接 daemon/真实 Provider |
| 6 | CLI sync list/pause/resume | 未实现 | 分派仅 add/run/status | 无 | 无 | 代码缺失 | 实现持久化控制 |
| 7 | CLI task list/conflict list | 未实现 | 无命令入口 | store 有底层方法，无 CLI 测试 | 无 | 入口缺失 | 增加命令与分页 |
| 8 | filter rule list/test | 部分完成 | 默认静态规则；`rule test` 未调用带 size 信息版本 | filter 10 个测试通过 | 无 | 无 `.hddignore`，watch 入队前未过滤 | 接入 watcher/入队并解析配置 |
| 9 | WebView2/CDP 登录 | 已实现但尚未验证 | WebView2 native 与 CDP 路径存在，fallback 已接 | Cookie/验证测试通过；UI 无端到端测试 | 无 | WebView2/浏览器/人工认证 | 真机执行并留脱敏证据 |
| 10 | Session/Credential 持久化 | 部分完成 | Token/Cookie/User/RootDocID/ExpiresAt 写入加密文件 | credential 5、cookie 16 个测试通过 | 无真实跨进程 API 成功证据 | 非 Credential Manager；Server 未持久化 | 使用 Windows Credential Manager 或明确威胁模型；跨进程验收 |
| 11 | `user/get` 验证和状态分类 | 已完成并通过自动测试 | `verification.go` 处理 200/401/403/重定向并限流读取 | verification 8 个测试通过 | 无真实环境证据 | 真实网络不可用 | 真实登录只读验证 |
| 12 | Huadian List/Stat | 已实现但尚未验证 | Provider 接 `dir/list`、`metadata`，根 `/` 映射 RootDocID | Huadian 共 21 个 httptest | 无；历史新进程 403 未闭环 | VPN/真实 Session | 先执行只读 ls/stat 验收 |
| 13 | Huadian Upload | 已实现但尚未验证 | predupload→begin→对象存储→end 已实现 | httptest 覆盖流程 | 无 | 禁止本轮真实写入 | 隔离测试账号人工验收 |
| 14 | Huadian Download | 已实现但尚未验证 | osdownload→authrequest→对象存储已实现 | httptest 覆盖流程 | 无 | VPN/真实 Session | 真实只读下载并校验散列 |
| 15 | Huadian Mkdir/Rename/Remove | 已实现但尚未验证 | 确认 API 路径均已接 | httptest 覆盖 | 无 | 禁止本轮真实写入 | 隔离目录人工验收 |
| 16 | hddsyncd 生命周期/IPC 启动 | 部分完成 | console run 启动 npipe、信号关闭；仅 Mock | daemon cache path 测试通过；npipe 运行测试受阻 | 无 | Named Pipe 权限 | 真机并发/关闭验收 |
| 17 | hddsyncd Session/任务/worker/watcher 恢复 | 未实现 | `runDaemon` 无 auth/store/watch/worker 引用，只有 Mock+轮询 | 无 daemon 集成测试 | 无 | 调用链缺失 | 重构为真实 daemon 装配 |
| 18 | Windows Service 管理 | 部分完成 | SCM install/start/stop/uninstall 函数及 CLI 存在 | 无测试；daemon 未调用 `service.Run` | 无 | 需管理员/SCM | 接 service main、Event Log、用户凭据模型 |
| 19 | IPC 协议分帧/上限 | 已完成并通过自动测试 | 4 字节长度前缀、1 MiB 上限 | protocol 5 个测试通过 | 无 | 无 | 增加 fuzz/短写测试 |
| 20 | Named Pipe 客户端/服务端 | 被阻塞 | Windows API 实现、并发连接、超时存在 | 9 个测试中本次 7 个因拒绝访问失败 | 无 | 当前权限 | 普通 Windows 会话重跑 |
| 21 | FS IPC 操作 | 部分完成 | list/stat/open/create/close/dirty/mkdir/rename/remove/setattr；无独立 fs.read，使用缓存路径 | IPCFS 仅 8 个路径/错误测试，非端到端 | 无 | 后端仅 Mock；setattr 空操作 | daemon 集成测试并持久排队 |
| 22 | SQLite 元数据存储 | 已完成并通过自动测试 | modernc SQLite、版本迁移、五张业务表、WAL、事务 claim 和恢复均已实现 | 原有 CRUD 测试及新增迁移、并发 claim、同路径串行、回滚、重启测试通过 | 无需真实云环境 | WinFsp 全仓检查仍受环境阻塞 | 第三阶段接入 daemon |
| 23 | 持久任务队列/退避/去重 | 部分完成 | 文本存储任务状态、worker 有界 goroutine/退避/最大重试 | worker 13、store 11 个测试通过 | 无 daemon 恢复证据 | daemon 未接入；非事务 | SQLite 化并做崩溃恢复测试 |
| 24 | 自动同步 | 部分完成 | watcher→文本任务→worker→Mock Upload 仅在 `hddctl sync run` | sync/watch/worker 单测通过 | 无 | 非 daemon、非真实 Provider | 完整装配并端到端测试 |
| 25 | 双向/删除/重命名/目录同步 | 部分完成 | Diff 与冲突副本工具存在；运行入口只入队 upload，未设 delete callback | sync 8 个算法测试 | 无 | 调用链未接 | 实现扫描、下载、删除、rename 与状态提交 |
| 26 | 文件监听 | 部分完成 | 轮询递归 WalkDir、写防抖、删除检测 | watch 5 个测试通过 | 无长时真机证据 | 非 Windows 原生；无溢出模型 | 中文/空格/rename/压力测试 |
| 27 | 缓存 | 部分完成 | daemon cache、下载临时文件、IPCFS 缓存路径校验 | cache path 及 IPCFS 路径测试通过 | 无多进程/重启证据 | 无容量/淘汰/索引 | 设计持久缓存状态机 |
| 28 | WinFsp memfs | 被阻塞 | `hddfs memfs` 接 NewMemFS，只读 | 本次无法编译 cgofuse | 仅旧文档声称，非本次证据 | 缺 WinFsp 头文件/运行环境 | 配齐 SDK 后重建挂载 |
| 29 | WinFsp direct cloudfs | 部分完成 | 只允许直接 Mock Provider，read-only | 12 个 CloudFS 测试因构建阻塞未运行 | 无 | 仅 Mock、WinFsp 缺失 | 不作为真实云演示 |
| 30 | WinFsp IPCFS 只读 | 被阻塞 | hddfs→IPCFS→npipe→mock daemon→cache；API 代码存在 | IPCFS 测试未运行，npipe 受阻 | 无 | WinFsp/pipe/Mock 后端 | 接真实 daemon 后真机验收 |
| 31 | WinFsp IPCFS 可写 | 部分完成 | Create/Write/Flush/Fsync/Release 等存在 | 仅少量辅助测试，未挂载验收 | 无 | 上传同步执行、非持久队列；setattr 空操作 | 持久排队、去重和断网恢复测试 |
| 32 | 正常卸载 | 被阻塞 | Ctrl+C 调 host.Unmount；IPCFS Close 还发送 daemon shutdown | 无本次运行测试 | 旧报告不足以证明当前版本 | WinFsp 缺失 | 真机 Explorer/强制断开验收 |
| 33 | Windows 路径安全 | 部分完成 | Mock root/reparse 检查、cache `Rel` 校验 | Mock/IPCFS 路径测试通过 | 无 | 保留名、junction、多进程覆盖不足 | 增加 Windows 专项安全测试 |
| 34 | 凭据和日志安全 | 部分完成 | 文件加密、Cookie/Token redaction 存在 | redaction 测试通过 | 无 | Provider 明文记录响应、authrequest、签名 URL | 立即移除敏感诊断输出并回归测试 |
| 35 | TLS/响应限制 | 部分完成 | 未发现禁用 TLS；错误响应/用户信息有限流 | httptest 覆盖部分状态 | 无 | 成功 `dir/list` 无上限，下载无限流属预期但需配额；超时策略未全测 | 增加响应/磁盘配额与超时测试 |
| 36 | 仓库卫生与可复现性 | 部分完成 | `.gitignore` 忽略 exe/db/token | 无 | 无 | 已跟踪大量缓存、数字后缀副本、HAR、diff、测试产物；无 migrations/test 目录 | 清理版本库并完善 ignore（另行授权） |

状态统计（以上 36 项）：已完成并通过自动测试 4；已完成并经过真实环境验证 0；已实现但尚未验证 8；部分完成 16；仅有骨架 0；未实现 4；被阻塞 4。

## 真实运行调用链

### 登录

`hddctl login`（已接）→ `SessionManager.LoginInteractive`（已接）→ Windows WebView2 可用则 WebView2，否则 CDP（代码已接、环境未验）→ 提取 Cookie/API Token/RootDocID（代码存在；RootDocID 依赖捕获到 `dir/list`）→ 加密文件 CredentialStore（已接，非 Windows Credential Manager）→ `POST user/get`（已接且 httptest 通过）→ 保存（已接）。整链状态：**已实现但尚未验证**。

### 远端列目录

`hddctl remote ls /`（已接）→ 加密文件加载 Token/Cookies/UserID/RootDocID（已接）→ `/` 映射 RootDocID，缺失时用 `gns:{userid}` 推导（已接但真实正确性未验）→ `huadian.Provider.List`（已接）→ `/api/efast/v1/dir/list`（httptest 通过）。整链状态：**已实现但尚未验证**；历史 403 **无法验证是否修复**。

### 后台同步

真实 `hddsyncd`：尚未装配 watcher/filter/scheduler/SQLite/worker，只有 Mock Provider、FS IPC 和 30 秒 list 轮询。另一个 CLI 实验链为：文件变化→轮询 watcher→直接 `InsertTask(upload)`（过滤不在 watcher 前）→SQLite→worker→MockProvider.Upload。整链状态仍为**部分完成**。

### WinFsp 读取

Explorer→cgofuse 回调→`hddfs --daemon`→IPCFS `fs.open`→Named Pipe→`hddsyncd`→缓存文件→MockProvider.Download。代码链接通到 Mock；不经过 Huadian Provider，且当前不能编译/挂载。整链状态：**被阻塞/部分完成，不是云盘读取**。

### WinFsp 写入

Explorer→IPCFS Write 写本地缓存→Flush/Release→`fs.markDirty/fs.close`→daemon 同步调用 `enqueueUpload`→临时副本→MockProvider.Upload。没有 SQLite task、持久 worker 或真实 Huadian Provider；Flush 和 Release 可分别触发上传，只有进程内 in-flight map 去重。整链状态：**部分完成**。

## 已完成功能

- 平台无关 IPC JSON 长度分帧与 1 MiB 限制，自动测试通过。
- `user/get` 解析、401/403/登录重定向分类、响应体限制，自动测试通过。
- 基础 help/version 入口可用；其余业务能力按实际后端分别归类，未因命令存在而算完成。

## 部分完成功能

- Mock Provider、worker、watcher、过滤器和文本任务存储各自有较充分单测，但真实 daemon 没有把它们组成目标架构。
- Huadian 七项 API 已实现且经 fake HTTP 服务测试，缺真实 Session、WAF、对象存储和跨进程验证。
- Credential 保存了 Token、Cookies（含 Domain/Path/Secure/HttpOnly/会话 Cookie）、RootDocID、UserID、Account 和 ExpiresAt；未保存 Server，且使用自管 AES 密钥文件而非 Credential Manager。
- IPCFS 有读写回调，但慢速下载在 `fs.open` handler 内同步执行；写入直接同步上传到 Mock，不经过持久任务队列。
- filter 只有静态默认规则；无 `.hddignore`、优先级/否定规则，`rule test` 也未读取文件大小，watcher 入队前没有执行过滤。

## 未完成功能

- 真 SQLite、SQL migrations 和事务未实现。
- daemon 的认证加载、同步根加载、持久任务恢复、worker pool、watcher 和真实 Provider 装配未实现。
- `auth diagnose`、`sync list/pause/resume`、`task list`、`conflict list` CLI 未实现。
- 双向实际执行、远端轮询、目录/重命名同步、离线恢复和网络恢复完整链路未接通。
- Event Log 和服务模式调用未接通；SCM 管理函数存在不等于服务可运行。

## 已知缺陷

### P0

1. `internal/cloud/huadian/provider.go:296-302,627,648` 将真实 API 响应、`authrequest` 和临时签名 URL写入 stderr，可能泄漏 Token、签名 URL、文件元数据和个人信息。
2. 目标要求 SQLite 持久化，但当前 `internal/store/sqlite/store.go` 是非事务文本行存储；崩溃期间 `os.Create` 可留下截断状态，不能满足任务可靠恢复要求。

### P1

1. daemon 仅 Mock，完全未装配认证、真实 Provider、持久队列、watcher 和 worker，无法演示自动云同步。
2. 登录验证成功不等于文件 API 可用；历史跨进程 `dir/list` 403 无回归或真实证据。
3. IPCFS 写入绕过持久任务队列，同步上传；进程/网络故障可能丢失待上传状态。
4. WinFsp 当前源码不能全量构建，Named Pipe 端到端测试在本环境不可运行。
5. hddsyncd 可安装服务但主程序未进入 `service.Run`，服务数据目录/用户凭据/Event Log 未解决。

### P2

1. `cmdRemoteDownload` 在未给目标路径时写仓库相对目录 `.testdownload`，违反运行时文件不落源码仓库原则。
2. Credential 默认使用 `%LOCALAPPDATA%`，而项目规则要求配置在 `%APPDATA%`；安全凭据位置和服务账号访问模型未定义。
3. Huadian `Upload` 及 `UploadByDocID` 将完整内容读入内存，不适合大文件。
4. watcher 在持锁状态调用回调，且只按 modtime 轮询；rename、同时间戳修改及长时压力覆盖不足。
5. `fs.setattr` 静态成功但不应用修改；属于误报成功。
6. 仓库跟踪了大量 Go 缓存、工具链缓存、数字后缀备份、HAR 和测试产物，存在敏感/可复现性风险。

### P3

1. 多处注释和文档仍把文本存储称为 SQLite，README 又称 mock only，文档状态互相冲突。
2. `login_cdp.go` 未通过 gofmt 检查，且源码/终端字符串存在乱码字符。

## 环境阻塞

- WinFsp/CGO：`GOOS=windows`、`CGO_ENABLED=1`，但 cgofuse 编译缺 `fuse_common.h`；本轮禁止安装依赖。
- Named Pipe：当前受限进程创建/打开测试管道时被拒绝访问，不能据此断言普通 Windows 会话也失败。
- WebView2/CDP：需要 WebView2 Runtime 或可启动的 Edge/Chrome 及人工 CAS/OAuth2 登录；未执行。
- VPN/AnyShare：没有使用真实账号、VPN 或服务器写操作；所有 Huadian 自动测试均为 `httptest.Server`。
- Go 用户目录：telemetry token 与 build cache trim 文件不可写，产生警告但不是业务代码结论。

## 测试覆盖清单

仓库静态统计共有 186 个 `Test*`：hddctl 21、daemon 1、auth 29、Huadian 21、Mock 26、config 5、domain 2、filter 10、IPC protocol 5、CloudFS 12、IPCFS 8、npipe 9、store 11、sync 8、watch 5、worker 13。普通测试和 race 中，除 WinFsp 构建与 npipe 权限阻塞外，其余列出的包通过。

| 模块 | 断言性质 | fake/真实服务 | 关键缺口 |
| --- | --- | --- | --- |
| Auth/Credential | 行为断言较完整 | 本地临时目录/httptest | 无 UI、跨进程真实 API |
| Huadian Provider | 请求路径、body、流程和状态 | httptest；不访问真实服务 | WAF、403、真实对象存储、大文件 |
| Mock/CLI remote | CRUD/路径/命令适配 | 临时目录 Mock | 不代表真实云 |
| 文本 store | CRUD、恢复、去重 | `t.TempDir` | 非 SQLite、无事务/崩溃注入 |
| worker/watch/sync/filter | 重试、算法、轮询行为 | Mock/临时目录 | daemon 集成、双向、网络恢复、中文/空格覆盖不系统 |
| IPC protocol | 分帧与上限 | 内存 | 无 fuzz/完整 FS 消息 |
| Named Pipe | 服务/客户端/异常 | 本机管道 | 本次 7 个权限失败 |
| WinFsp | CloudFS/IPCFS 辅助行为 | Mock | 本次未编译；无真实挂载/Explorer |
| Service | 无测试 | 无 | install/run/Event Log/凭据全链 |

未发现普通单元测试主动访问真实服务器；真实环境相关内容主要是手工清单和旧报告，不能替代本次可复现证据。

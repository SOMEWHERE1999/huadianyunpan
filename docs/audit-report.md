# Huadian Drive 全面审查报告

审查日期：2026-06-28  
审查范围：`AGENTS.md`、`README.md`、`go.mod`、`docs/`、`cmd/`、`internal/`、`scripts/`  
审查方式：只读静态审查及构建/测试验证；未修改业务代码，未安装依赖，未访问真实云盘。

## 执行摘要

项目当前状态：

- 编译：部分可编译。`go build ./cmd/...` 在 `CGO_ENABLED=0` 下成功；完整包因 Huadian Provider 源文件损坏而不能编译。
- 运行测试：不能。普通测试存在编译失败和 Named Pipe 失败。
- 登录：不能确认可用。实际调用 CDP 浏览器登录，不是文档所述 console，也不是 WebView2。
- 访问 AnyShare：不能。有效 Provider 文件损坏，遗留 Provider 的接口方法也均返回未实现。
- 启动 daemon：mock daemon 可构建，但未接入认证、AnyShare、SQLite 或同步引擎；Named Pipe 运行测试失败。
- 挂载 WinFsp：不能可靠挂载。原生 CGO 构建失败，挂载生命周期和只读参数也有缺陷。
- 安全读写文件：不能。存在路径穿越、任意缓存路径信任、下载前截断和写回丢失风险。

### 命令结果

- `go test ./...`：失败。`internal/cloud/huadian/provider.go:1:1: expected 'package', found At`；Named Pipe 测试出现 `CreateFileW: Access is denied`。
- `go test -race ./...`：失败。默认环境为 `CGO_ENABLED=0`；启用仓库 GCC 后，`runtime/cgo` 仍以 exit status 2 失败，同时被损坏的 Provider 阻断。
- `go vet ./...`：失败。Huadian Provider 语法损坏。
- `go build ./cmd/...`：成功，但未编译 Huadian 包，且使用 `CGO_ENABLED=0`。
- 额外原生验证：启用 CGO 构建 `hddfs` 失败于 `runtime/cgo` exit status 2。

## 问题清单

### 编号 1

严重度：P0  
模块：AnyShare Provider / 构建  
文件和行号：`internal/cloud/huadian/provider.go:1`  
现象：文件包含 PowerShell/Git 错误文本，不是有效 Go 源码。  
根本原因：一次失败的恢复或脚本输出覆盖了源码。  
可能后果：完整测试、vet 和 Huadian Provider 编译全部失败。  
修复建议：从可信版本恢复 Provider，删除错误文本，并核对构造函数与测试接口。  
建议回归测试：`go test ./internal/cloud/huadian`、`go test ./...`、`go vet ./...`。  
是否确定存在，还是需要运行时验证：确定存在，已由编译器复现。

### 编号 2

严重度：P0  
模块：IPC / 文件系统安全  
文件和行号：`cmd/hddsyncd/main.go:317-330`  
现象：`fs.create` 将客户端路径直接拼入缓存目录后调用 `os.Create`，没有拒绝 `..`、卷名或缓存目录逃逸。  
根本原因：daemon 信任 IPC 请求路径，且在 Provider 校验前创建文件。  
可能后果：能够连接 Named Pipe 的进程可截断或创建 daemon 权限范围内的任意文件。  
修复建议：规范化远端路径；用 `filepath.Rel`、卷名检查和最终路径检查确保目标位于缓存根；拒绝重解析点。  
建议回归测试：提交 `../../victim`、绝对路径、盘符和 UNC 路径，验证目标文件不变。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 3

严重度：P0  
模块：WinFsp / IPC 缓存路径  
文件和行号：`cmd/hddfs/main.go:129-142`、`internal/mount/winfsp/ipcfs.go:351-359,493`  
现象：daemon 模式向 `IPCFileSystem` 传入空缓存根，跳过 `validateCachePath`，随后直接打开服务端返回的路径。  
根本原因：客户端不知道 daemon 缓存目录，却用信任任意存在路径代替安全协议。  
可能后果：伪造或抢占同名 Named Pipe 的进程可诱导 hddfs 读取、截断或修改任意本地文件。  
修复建议：不要通过 IPC 返回可任意解释的路径；使用受认证句柄、受控共享缓存根或 daemon 文件句柄，并校验 Pipe 服务端身份。  
建议回归测试：伪造 Pipe 服务端返回缓存外文件，验证 Open/Create/Write 全部拒绝。  
是否确定存在，还是需要运行时验证：路径信任确定存在；Pipe 抢占影响需运行时验证。

### 编号 4

严重度：P0  
模块：WinFsp 写回 / daemon  
文件和行号：`internal/mount/winfsp/ipcfs.go:510-527`、`cmd/hddsyncd/main.go:288-315`  
现象：Flush 忽略 IPC 错误并清除 dirty；daemon 收到远端路径后用 `os.Open(path)` 当作本地缓存文件，再直接启动 goroutine 上传。  
根本原因：IPC 消息未携带受控缓存标识，上传未进入持久任务队列，错误也不返回 WinFsp。  
可能后果：应用收到写入成功，但内容从未上传；进程退出后修改永久丢失。  
修复建议：daemon 从内部索引解析缓存文件；先持久入队成功再确认 Flush；只有上传确认后才能清 dirty。  
建议回归测试：模拟 daemon 不可用、上传失败、重启和重复 Flush，确认 dirty 与任务持续存在。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 5

严重度：P0  
模块：下载 / 本地文件安全  
文件和行号：`internal/worker/pool.go:189-201`、`cmd/hddctl/remote.go:94-117`  
现象：下载在网络操作前直接 `os.Create` 最终目标。  
根本原因：没有临时文件、校验和原子替换流程。  
可能后果：断网、401、500 或进程崩溃会截断原文件并留下部分内容。  
修复建议：下载到同目录临时文件，完成长度/校验后关闭并原子替换；失败保留原文件。  
建议回归测试：已有目标文件下模拟中途失败、零字节响应和取消，确认原文件字节不变。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 6

严重度：P0  
模块：认证凭据存储  
文件和行号：`internal/cloud/anyshare/auth/credential.go:20-54`、`internal/cloud/anyshare/auth/session.go:65-80`  
现象：Token 和完整 Cookie 值以明文 JSON 永久存入 `%LOCALAPPDATA%\HuadianDrive\auth.json`。  
根本原因：`CredentialStore` 只是普通文件；Windows 下 `0600/0700` 不能替代 DPAPI 或 Credential Manager。  
可能后果：本地文件泄露、备份或其他进程读取可直接导致账号会话泄露。  
修复建议：使用 Windows Credential Manager 或 DPAPI；不要永久保存浏览器 Cookie。  
建议回归测试：验证磁盘文件不包含 token/cookie 明文，且复制文件后无法解密。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 7

严重度：P0  
模块：CDP 登录安全  
文件和行号：`internal/cloud/anyshare/auth/session.go:25-30`、`internal/cloud/anyshare/auth/login_cdp.go:19-24,68,198-231`  
现象：`hddctl login` 默认启动固定 9224 端口的 CDP，提取并持久保存全部 Cookie；任何 NCEPU 域 Cookie 都可被视为登录成功；程序还会强制终止占用该端口的任意进程。  
根本原因：将调试协议作为正式认证机制，缺少随机端口、来源过滤和进程所有权校验。  
可能后果：本地进程可在登录期间读取 CAS/AnyShare Cookie；错误保存 CAS Cookie；无关进程可能被终止。  
修复建议：停止将 CDP 作为默认认证；采用正式 WebView2 授权回调并只提取必要凭据；绝不终止非自身进程。  
建议回归测试：恶意本地 CDP 客户端、错误域 Cookie、端口占用和取消登录测试。  
是否确定存在，还是需要运行时验证：代码行为确定；凭据窃取场景需运行时验证。注意："强制终止占用该端口的任意进程" 的声明应直接对照 `login_cdp.go:198-231` 源码验证——可能被误读为端口清理逻辑而非进程终止。

### 编号 8

严重度：P0  
模块：测试安全  
文件和行号：`internal/worker/pool_test.go:24-35,90-105`  
现象：worker 测试使用 `C:\src.txt`、`C:\out.txt` 等固定绝对路径，下载测试会尝试创建或截断系统盘根目录文件。  
根本原因：测试数据没有放在 `t.TempDir()` 下。  
可能后果：在有权限的开发机或 CI 中截断用户已有文件。  
修复建议：所有路径由 `t.TempDir()` 派生，并断言写入路径位于临时目录。  
建议回归测试：运行前创建哨兵文件，确认测试不会访问临时目录外路径。  
是否确定存在，还是需要运行时验证：确定存在；本次环境未留下该文件。

### 编号 9

严重度：P0  
模块：Mock Provider / 重解析点  
文件和行号：`internal/cloud/mock/provider.go:42-53,116-137,173-197`  
现象：仅进行字符串级 `filepath.Rel` 检查，随后使用会跟随 junction/symlink 的文件操作。  
根本原因：未解析和验证重解析点后的最终路径。  
可能后果：根内 junction 可把上传或覆盖操作引向根目录外。  
修复建议：拒绝同步根内重解析点，或逐级打开并验证最终路径仍在根内。  
建议回归测试：根内 junction 指向根外目录，验证 Upload/Download/Remove 全部拒绝。  
是否确定存在，还是需要运行时验证：静态缺陷确定；Windows junction 行为需集成验证。

### 编号 10

严重度：P1  
模块：AnyShare Provider  
文件和行号：`internal/cloud/huadian/provider.go.8525611242547531338:158-162,251-253,349-350,395-438`  
现象：完整实现只存在于不会参与编译的数字后缀副本；Provider 接口的七个文件操作均返回 `ErrNotImplemented`。  
根本原因：Provider 以 docid API 编写，但没有路径到 docid 的解析层，也未接入 SessionManager。  
可能后果：即使恢复副本，hddctl、同步和 WinFsp 仍不能访问 AnyShare。  
修复建议：建立经过抓包确认的路径/docid 元数据模型，再让正式 Provider 完整实现 `cloud.Provider`。  
建议回归测试：用 `httptest.Server` 覆盖七个 Provider 操作和路径/docid 缓存失效。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 11

严重度：P1  
模块：AnyShare 协议正确性  
文件和行号：`internal/cloud/huadian/provider.go.8525611242547531338:255-305,311-335,353-382`  
现象：下载文档显示 `authrequest` 为数组，但代码按对象解析；`predupload.match=true` 被直接当作上传成功；上传把整个文件读入内存；403 未映射认证错误。  
根本原因：遗留实现对抓包结构和秒传语义作了未验证假设。  
可能后果：下载解析失败、文件未真正关联目标目录、大文件内存耗尽、Session 过期提示不明确。  
修复建议：严格按确认样本解析 authrequest；验证 method/URL；补齐秒传提交步骤；流式上传；统一 401/403 认证错误。  
建议回归测试：数组/对象 authrequest、match=true、401、403、500、大文件和中途断连。  
是否确定存在，还是需要运行时验证：解析和提前成功确定存在；服务器实际语义需抓包验证。

补充：遗留 DTO 中 `docid/rev` 为 string，`modified/size` 为 int64，类型选择合理；未发现禁用 TLS 校验或直接记录签名 URL。

### 编号 12

严重度：P1  
模块：SQLite / 持久状态  
文件和行号：`go.mod:1-5`、`internal/store/sqlite/store.go:1-42,85-94`  
现象：`internal/store/sqlite` 实际是每行一个文本文件，不是 SQLite；go.mod 没有 SQLite 依赖；写入直接覆盖，没有事务和原子替换。  
根本原因：占位文件存储被放入正式 SQLite 包并替代预期实现。  
可能后果：崩溃或多进程访问可产生半行、丢字段、重复 ID 和任务状态不一致。  
修复建议：使用真正 SQLite；启用 WAL、busy timeout、迁移版本和明确事务边界。  
建议回归测试：崩溃恢复、双连接并发、迁移回滚、任务状态原子提交。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 13

严重度：P1  
模块：Worker / 持久任务队列  
文件和行号：`internal/worker/pool.go:126-171`、`internal/store/sqlite/store.go:393-493`  
现象：任务没有持久去重；处理前不原子声明 running；不同任务类型只按本地路径锁定，delete 没有本地路径；超过八次重试后直接删除失败任务。  
根本原因：队列状态机不完整，去重仅是进程内瞬时 `inFlight` map。  
可能后果：重复上传、上传与删除并发、失败任务永久丢失、重启恢复不准确。  
修复建议：SQLite 中增加唯一任务键和原子 claim；按规范化远端路径串行化；耗尽重试后保留 dead/failed 状态。  
建议回归测试：多 worker 抢同一任务、同路径上传与删除、进程崩溃后恢复、重试耗尽仍可见。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 14

严重度：P1  
模块：同步 watcher / 状态机  
文件和行号：`internal/watch/watcher.go:96-147`、`internal/sync/syncer.go:73-123`  
现象：watcher 只检测新建和修改，不检测删除、重命名；防抖时间从首次变化开始且不随新事件重置；同步状态仅在内存 map 中。远端缺失时会生成删除本地动作。  
根本原因：轮询快照和持久状态机未完成。  
可能后果：仍在写入的文件被提前上传；删除/重命名永不同步；未来执行删除动作时可能误删本地文件。  
修复建议：持久化双端快照；稳定期从最后变化计算；删除动作必须有 tombstone、版本和同步根校验。  
建议回归测试：连续写入、删除、重命名、双根、重启恢复和远端暂时不可见。  
是否确定存在，还是需要运行时验证：确定存在；删除动作当前尚未接入执行器。

### 编号 15

严重度：P1  
模块：WinFsp 挂载生命周期  
文件和行号：`cmd/hddfs/main.go:198-235`  
现象：所有模式都传入 `-o ro`；代码等待 `host.Mount` 返回后才安装 Ctrl+C 处理，而 Mount 通常直到卸载才返回。  
根本原因：错误理解 cgofuse Mount 的阻塞生命周期，并把只读演示参数用于 IPC 写回模式。  
可能后果：写回回调不可达；正常卸载逻辑可能永远不能运行，只能强制终止。  
修复建议：按 cgofuse 生命周期提前安装取消处理；只对 memfs/cloudfs 添加只读选项。  
建议回归测试：真实 WinFsp 下挂载、Ctrl+C、daemon 停止和重复卸载。  
是否确定存在，还是需要运行时验证：只读参数确定；具体阻塞表现需真实 WinFsp 验证。

### 编号 16

严重度：P1  
模块：WinFsp 并发与回调  
文件和行号：`internal/mount/winfsp/ipcfs.go:546-570`、`internal/mount/winfsp/cloudfs.go:288-298`  
现象：Rename 按调用顺序锁两个路径；同名 Rename 会重复锁同一 mutex，反向 Rename 可形成锁顺序死锁。CloudFS 在全局缓存锁内执行远端下载。  
根本原因：缺少稳定锁顺序及锁外 I/O 设计。  
可能后果：WinFsp 回调永久卡住，导致文件操作或卸载挂死。  
修复建议：按规范化路径排序加锁并处理同路径；所有网络和慢磁盘操作移出全局锁。  
建议回归测试：同路径 Rename、A→B 与 B→A 并发、慢下载期间并发 Getattr/Release。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 17

严重度：P1  
模块：IPC 分帧与关闭  
文件和行号：`internal/ipc/protocol.go:85-125`、`internal/platform/windows/npipe/npipe.go:103-130,185-222`  
现象：写入没有 `io.WriteFull`；Pipe 缓冲区仅 4096 字节却允许 1 MiB 消息；最大实例数为 1；Shutdown 不跟踪活动连接；超时后同步等待取消 I/O goroutine。  
根本原因：直接封装同步 Win32 Handle，缺少连接生命周期和消息模式大帧处理。  
可能后果：大目录响应失败、并发客户端失败、daemon 停止卡住或 goroutine 泄漏。  
修复建议：完整写循环；处理 `ERROR_MORE_DATA`；允许合理并发实例；跟踪并关闭活动连接；Shutdown 等待 WaitGroup。  
建议回归测试：半包、粘包、短写、>4 KiB 帧、1 MiB 边界、多客户端和关闭时阻塞连接。  
是否确定存在，还是需要运行时验证：静态缺陷确定；本次 Named Pipe 测试还实际出现 Access Denied，环境因素需复核。

### 编号 18

严重度：P1  
模块：Windows/CGO 构建条件  
文件和行号：`go.mod:3-5`、`cmd/hddfs/main.go:13`、`internal/platform/windows/npipe/npipe.go:1`、`internal/cloud/anyshare/auth/cdp_windows.go:1`  
现象：Windows、syscall、cgofuse 文件都没有 build tag；默认 CGO 关闭时命令构建成功，但不验证真实 WinFsp；启用 CGO 后本环境无法构建。  
根本原因：没有拆分 `windows && cgo`、Windows 非 CGO stub 和其他平台 stub；构建脚本也没有构建 hddfs。  
可能后果：CI 给出虚假成功，原生 hddfs 到部署阶段才失败。  
修复建议：增加正确 build tags 和明确不可用 stub；CI 分别验证 Windows CLI 与 Windows CGO/WinFsp 构建。  
建议回归测试：`CGO_ENABLED=0/1` 构建矩阵和缺少 WinFsp SDK 时的明确错误。  
是否确定存在，还是需要运行时验证：build tag 缺失及 CGO 构建失败确定存在。

### 编号 19

严重度：P2  
模块：认证状态一致性  
文件和行号：`internal/cloud/anyshare/auth/session.go:43-88`、`internal/cloud/anyshare/auth/credential.go:74-97`  
现象：SaveSession 忽略存储错误并总返回 nil；cookie-only CDP Session 被判为未认证；logout 忽略删除错误；expires_at 保存解析不一致。  
根本原因：凭据文件被拆成伪键值接口，但底层每次 Set 都重写整文件。  
可能后果：CLI 显示登录或退出成功，但凭据实际未保存或未清除。  
修复建议：原子保存完整 Session；传播所有错误；logout 后验证文件不存在。  
建议回归测试：只读目录、磁盘写入失败、cookie-only session、部分删除失败。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 20

严重度：P2  
模块：运行目录与架构  
文件和行号：`cmd/hddctl/sync.go:39-124`、`cmd/hddsyncd/main.go:95-385`  
现象：同步、watcher、worker、IPC handler 等业务逻辑位于 `cmd/`；同步数据库和缓存位于 `%TEMP%`；daemon 不使用 hddctl 创建的任务或根配置。  
根本原因：多个演示阶段直接堆叠到入口程序，没有统一 application 层。  
可能后果：hddctl、daemon、hddfs 各自维护互不相通的状态，重启后缓存和任务可能消失。  
修复建议：把 daemon application、IPC handler 和同步编排移到 `internal/`；统一 APPDATA/LOCALAPPDATA 路径。  
建议回归测试：hddctl 添加根后 daemon 可见、重启后任务和缓存仍存在。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 21

严重度：P2  
模块：Windows 文件名与缓存  
文件和行号：`internal/mount/winfsp/cloudfs.go:65-98,430-471`、`internal/mount/winfsp/ipcfs.go:639-701`  
现象：没有处理 `CON/PRN/AUX/NUL/COM1/LPT1`、尾随空格/点、非法字符和大小写碰撞；缓存没有容量限制、版本校验或淘汰。  
根本原因：远端逻辑路径被直接映射为 Windows 名称。  
可能后果：远端文件无法创建、多个文件映射到同一路径、缓存无限增长或长期返回旧内容。  
修复建议：定义可逆 Windows 名称编码；建立大小写无关冲突索引；缓存按 rev/ETag 校验并设容量上限。  
建议回归测试：保留名、尾点、尾空格、混合大小写、非法字符、长路径和缓存淘汰。  
是否确定存在，还是需要运行时验证：缺失确定；具体 WinFsp 返回码需运行时验证。

### 编号 22

严重度：P2  
模块：测试质量  
文件和行号：`internal/worker/pool_test.go:24-203`、`internal/platform/windows/npipe/npipe_test.go:109-132`  
现象：多个测试只 sleep 后检查 pending 为空；failed 任务不会出现在该列表，因此实际失败也会被当作成功。去重和串行测试没有断言；restart 测试的两个分支都接受；InvalidJSON 测试没有发送无效 JSON。  
根本原因：测试没有观察最终文件、Provider 调用次数和持久状态。  
可能后果：同步、去重、恢复和 IPC 缺陷被绿色测试掩盖。  
修复建议：使用可观测 fake Provider、事件同步而非 sleep，并对最终字节、调用顺序和状态逐项断言。  
建议回归测试：失败任务不应被当成功、重复任务只调用一次、无效 JSON 返回确定错误。  
是否确定存在，还是需要运行时验证：确定存在。

### 编号 23

严重度：P2  
模块：文档 / AGENTS.md  
文件和行号：`AGENTS.md:149,189-204,206-220,223-241`  
现象："Full Project Review Rules" 节使用一级标题 `#` 而非二级 `##`，破坏文档标题层级；"Architecture constraints"（行 189-204）与已有 "Architecture Rules"（行 38-61）大量内容重复——cmd/ 职责、AnyShare 包归属、SQL 包归属、Windows 代码归属、禁用 TLS 校验、不记录凭据、测试用临时目录等规则均以不同措辞重复；"Required checks"（行 206-220）与 "Testing Rules"（行 85-106）完全重复；"Repair workflow"（行 223-241）与 "Development Workflow"（行 108-125）及 "Definition of Done"（行 126-135）部分重叠；"Main executables" 列出 hddfs 为正式可执行文件，与第 15 行 "A third executable will be added later" 矛盾。  
根本原因：新增审查指导章节时未对照已有章节进行去重和一致性检查。  
可能后果：规则变更时需同时修改多处，遗漏一处即产生矛盾；hddfs 状态不一致使维护者对当前交付阶段产生错误预期。  
修复建议：将重复规则改为对已有章节的交叉引用而非复制；统一 hddfs 为 planned 状态；修正标题层级至二级标题。  
建议回归测试：无。  
是否确定存在，还是需要运行时验证：确定存在。

## 重复/冲突实现

### 认证

- 实际入口是 `hddctl login → SessionManager.LoginInteractive → CDPLoginUI`。
- console 实现存在但没有 CLI 选择入口。
- WebView2 只有永久返回不可用的 stub。
- README/auth 文档仍称 console 默认、WebView2 未来，与代码冲突。

### Provider

- 有损坏的正式 `provider.go` 和一个完整但不参与编译的数字后缀 Provider。
- 正式调用链没有任何地方构造 Huadian Provider。
- hddctl remote 每次创建全新的临时 mock，命令之间数据不持久。

### WinFsp

- `internal/mount/winfsp` 包含 memfs、只读 cloudfs、IPC write-back fs。
- `internal/platform/windows/winfsp` 还有一套未使用的 FileSystem，直接在 Flush 调用 Provider.Upload，违反持久队列规则。
- hddfs 实际行为：`memfs` 使用 memfs；直接 mock 使用 cloudfs；`--daemon` 使用 ipcfs。

### 同步

- `hddctl sync run` 自己运行 watcher/worker。
- `hddsyncd` 只运行临时 mock 和 IPC handler。
- `internal/sync.Syncer` 没有接入两者。

### 源码副本与文档

- 发现约 20 份源码级数字后缀副本（不含 gomodcache 缓存文件），其中 `hddctl/main.go` 有 6 份、worker 源码/测试各 3 份、daemon main 有 3 份。
- Pipe 名同时出现 `\\.\pipe\hddsyncd` 与 `\\.\pipe\huadian-drive`。
- README 仍称 “mock provider only”，milestones 又把已存在模块标为 future。
- 多份中文文档和测试字符串已乱码。

## 缺失测试

### 认证

- SessionManager、CredentialStore、logout、401/403、过期 Session。
- CDP 超时、取消、恶意本地连接、Cookie 域过滤。
- WebView2 实现及测试。
- 明文凭据和日志泄漏测试。

### AnyShare Provider

- 七个 Provider 接口没有有效网络测试。
- 没有 `httptest.Server` 覆盖 predupload、osbeginupload、对象存储、osendupload。
- 没有 authrequest 数组、异常 JSON、401、403、500、超时和响应体关闭测试。
- 没有大文件、断点失败、重复上传和截断下载测试。

### IPC

- `internal/ipc` 完全没有单元测试。
- 缺少半包、粘包、短写、4 KiB 以上帧、空帧、多客户端和 shutdown 活动连接测试。
- 无缓存路径穿越及 Pipe 服务端身份测试。

### 同步与并发

- 去重和路径串行测试没有有效断言。
- 缺少上传/删除/重命名同路径并发。
- 缺少 watcher 删除、重命名、持续写入、多根和 junction。
- race 检查当前不能运行。
- 缺少真实持久队列崩溃恢复。

### SQLite 与缓存

- 没有真正 SQLite，因此无迁移、事务、WAL、busy timeout 测试。
- 缺少写入中崩溃、多进程并发和损坏恢复。
- 缺少缓存容量、淘汰、原子替换和旧 rev 失效测试。

### WinFsp

- 仅测试路径 helper 和错误字符串映射。
- Getattr、Readdir、Open、Read、Create、Write、Flush、Release、Rename、Unlink、Mkdir、Rmdir、Setattr 没有端到端测试。
- 缺少真实 WinFsp 挂载、卸载和 daemon 断开测试。
- 缺少中文、空格、保留名称、尾点、大小写碰撞、长路径。
- 缺少 Session 过期和网络超时的错误码验证。

### 测试数据

- 普通测试没有访问真实华电云盘。
- 存在尝试写系统盘固定路径的测试。
- 中文测试字符串本身已经乱码。
- 空文件有 mock 测试；大文件、断网、401、403、500 覆盖不足。

## 修复顺序

### 第一批：构建基线

- 恢复损坏的正式 Provider 源码。
- 隔离数字后缀副本。
- 建立 Windows/CGO build tags 和可复现构建矩阵。
- 目标：test、vet、build 至少能够完整启动。

### 第二批：阻断本地文件破坏

- 修复 IPC `fs.create` 路径穿越。
- 禁止信任任意 `cache_path`。
- 下载改为临时文件加原子替换。
- 拒绝 junction/symlink 逃逸。
- 修复测试中的固定绝对路径。

### 第三批：认证安全

- 停止默认 CDP 和固定调试端口方案。
- 使用 WebView2/正式 OAuth 回调。
- 迁移到 DPAPI/Credential Manager。
- 修复 Session 原子保存、logout 和 401/403 映射。

### 第四批：真正的 SQLite 和任务状态机

- 引入正式 SQLite、迁移、事务和 WAL。
- 实现原子 claim、去重、dead 状态和重启恢复。
- 所有上传必须通过持久任务队列。

### 第五批：同步正确性

- 修复 watcher 删除、重命名和真正防抖。
- 持久化双端状态和 tombstone。
- 统一规范化路径锁和同步根边界。

### 第六批：IPC 稳定性

- 完成短写、大帧、多客户端、超时和 shutdown。
- 增加服务端身份与访问控制。
- 禁止通过 JSON 传输文件内容或不受控本地路径。

### 第七批：AnyShare Provider

- 以确认的 HAR/文档实现路径到 docid。
- 完成上传、下载、秒传语义及 authrequest 解析。
- 全部网络测试使用 fake HTTP server。

### 第八批：WinFsp

- 只保留 IPC write-back 架构。
- 修复挂载生命周期、句柄模型、锁顺序和错误映射。
- 完成缓存容量、版本失效和 Windows 文件名映射。
- 最后进行真实 WinFsp 集成与卸载测试。

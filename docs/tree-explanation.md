# Huadian Drive 项目文件树说明

> OpenCode 快照文件（`*.编号` 后缀，如 `main.go.2082049016302660708`）是 AI 会话自动备份，非正式源码，本文不逐一解释。测试文件（`*_test.go`）略。

---

```
D:\ncepupan/                          ← 项目根目录
│
├── go.mod                            Go 模块声明。模块名 ncepupan/hdd，Go 版本 1.24.5，两个直接依赖：
│                                      github.com/winfsp/cgofuse v1.6.0（WinFsp FUSE Go 绑定）
│                                      modernc.org/sqlite v1.34.0（纯 Go 实现的 SQLite 驱动）
│
├── winfsp-x64.def                    WinFsp DLL 的 CGo 链接符号表。hddfs.exe 编译时通过 CGo 链接此文件
├── AGENTS.md                         AI 辅助开发规范（OpenCode / Codex 行为准则和架构约束）
├── README.md                         项目简介和开发指南
│
├── .git/                             Git 版本控制仓库（commit 历史、分支、对象存储）。git init 创建
├── .gocache/                         Go 编译器构建缓存。go build 自动生成，下次构建复用加速编译
├── .tools/                           开发工具集（手动下载放置）
│   ├── chromium/chrome-win/           Chromium 浏览器，CDP 登录模式需要它启动浏览器完成 CAS 认证
│   └── mingw64/mingw64/              MinGW-w64 交叉编译工具链，CGo 编译 WinFsp 时需要
├── .tmp/                             临时测试数据目录（Mock E2E 日志输出、手工测试记录）
│
├── cmd/                              三个独立可执行程序的入口
│   │
│   ├── hddctl/                       命令行维护工具 hddctl.exe
│   │   ├── main.go                   入口。全局 flag（--mock/--cdp/--console/--help），
│   │   │                             子命令路由：login → cmdLogin()、logout → cmdLogout()、
│   │   │                             auth → cmdAuth()、sync → runSyncCmd()、rule → runRuleCmd()、
│   │   │                             remote → runRemoteCmd()、daemon-probe → runDaemonProbeCmd()
│   │   ├── remote.go                 hddctl remote 全部子命令实现：
│   │   │                             cmdRemoteLs（列出目录）、cmdRemoteStat（查看属性）、
│   │   │                             cmdRemoteMkdir（创建目录）、cmdRemoteUpload（上传文件）、
│   │   │                             cmdRemoteUploadDirectory（递归上传目录）、
│   │   │                             cmdRemoteCopy（文件/目录复制，支持冲突策略）、
│   │   │                             cmdRemoteMove（文件/目录跨父目录移动）、
│   │   │                             cmdRemoteRename（同父目录重命名）、
│   │   │                             cmdRemoteDownload（下载文件）、cmdRemoteRm（删除）
│   │   ├── sync.go                   hddctl sync 同步根管理子命令：
│   │   │                             cmdSyncAdd（注册同步根）、cmdSyncList（列出已注册）、
│   │   │                             cmdSyncRemove/Enable/Disable（管理同步根状态）、
│   │   │                             cmdSyncStatus（查看同步状态）、cmdSyncTasks（查看任务列表）、
│   │   │                             cmdSyncRun（启动前台 watcher + worker pool）
│   │   ├── daemon_probe.go           hddctl daemon-probe：通过 Named Pipe IPC 向 hddsyncd
│   │   │                             发送 status/fs.stat/fs.list 验证 daemon 存活。
│   │   │                             支持 --mkdir 和 --confirm-mock-write 测试写能力
│   │   └── rule.go                   hddctl rule：cmdRuleList（列出默认过滤规则）、
│   │                                  cmdRuleTest（测试某路径是否被过滤）、
│   │                                  cmdRuleTestWithInfo（带文件元数据的完整过滤测试）
│   │
│   ├── hddsyncd/                     后台守护进程 hddsyncd.exe
│   │   └── main.go                   ~1442 行，全仓库入口最大的单文件。分为 7 大区块：
│   │                                 [1] writePolicy 能力定义（8 种受限模式 + 默认 fully_writable）
│   │                                 [2] parseRunArgs() CLI 参数解析 + 互斥校验
│   │                                 [3] main() 子命令分发（run/version/service/help）
│   │                                 [4] runDaemon() 生命周期：provider 装配 → SQLite Store →
│   │                                     worker Pool → directory Watcher → Named Pipe IPC Server
│   │                                 [5] dispatchFS() IPC 请求总入口，policyAllowsRequest() 权限守门
│   │                                 [6] 全部 FUSE handler：handleFSList/Stat/Open/Create/Close/
│   │                                     Mkdir/Rename/RenameDir/RenameFile/Remove/UploadStaged/
│   │                                     Setattr。含跨父目录移动、文件 post-check、staging 安全校验
│   │                                 [7] 工具函数：stagingRoot/validateStagingPath/remoteParent/safeCachePath
│   │
│   └── hddfs/                        WinFsp 挂载程序 hddfs.exe
│       └── main.go                   参数解析（手动 for loop）→ 互斥校验（~140 行）→
│                                      [daemon 模式] daemonHandshake 探活 →
│                                      validateAndMount(IPCFileSystem{8 种模式}) →
│                                      [direct 模式] validateAndMount(CloudFS{mock provider})
│                                      挂载框架：盘符校验、SetCapDeleteAccess、-o ro 选项、
│                                      host.Mount 阻塞、SIGINT 优雅卸载、cleanup 回调
│
├── internal/                         Go 库源代码——全部编译后嵌入三个 EXE
│   │
│   ├── app/                          应用引导工具
│   │   └── app.go                    PrintVersion(name) 打印 "hddsyncd version 0.1.0-dev"。
│   │                                 SetupLogging(level) 初始化全局 slog（预留，尚未被 main 调用）
│   │
│   ├── domain/                       全仓库共享的基础类型
│   │   └── types.go                  四个类型：FileInfo（文件元数据：Path/Size/ModTime/IsDir/ETag）、
│   │                                 SyncRoot（本地↔远端路径映射对）、SyncStatus（synced/pending/
│   │                                 conflicted/error）、FileEvent（本地文件变更事件：Create/
│   │                                 Modify/Delete/Rename）。FileInfo 贯穿 cloud.Provider →
│   │                                 dispatchFS → WinFsp Getattr 全线
│   │
│   ├── config/                       配置文件读写（预留，尚未被任何入口导入）
│   │   └── config.go                 Config 结构体（SyncRoots/LogLevel/LogFile/WorkerPoolSize）、
│   │                                 Default() 返回默认值、Load() 从 JSON 加载、Save() 持久化、
│   │                                 GetAppDataDirs() 返回 %APPDATA%/hdd 和 %LOCALAPPDATA%/hdd
│   │
│   ├── filter/                       文件过滤规则引擎
│   │   └── filter.go                 三种规则类型：RuleName（glob 匹配文件名如 *.tmp/~$*/*.log）、
│   │                                 RulePath（检查路径含指定目录名如 .git/node_modules）、
│   │                                 RuleSize（info.Size() > 500MB 就排除）。
│   │                                 DefaultExcludes() 返回 6 条内置规则。
│   │                                 Exclude(path, info) 同步时据此决定是否跳过文件。
│   │                                 ExcludePath(path) 仅名规则可用，大小规则须传 FileInfo
│   │
│   ├── ipc/                          Named Pipe IPC 协议层——hddfs ↔ hddsyncd 通信的语言词典
│   │   └── protocol.go               Request{Type,ID,Data} 和 Response{Type,ID,Data,Error} 基础消息、
│   │                                 FUSE 操作专用类型：FSListData/FSStatData/FSOpenData/FSCreateData/
│   │                                 FSRenameRequest/FSRemoveRequest/FSUploadStagedRequest 等。
│   │                                 Encode(w,v)/Decode(r,v)：4 字节大端长度前缀 + JSON body，
│   │                                 最大消息 1 MB。不依赖 protobuf 或 gRPC
│   │
│   ├── logging/                      安全日志——白名单字段 + 路径脱敏
│   │   └── security.go               SecurityEvent 结构体（Operation/Method/Path/Status/RequestID/
│   │                                 ErrorClass）→ LogSecurityEvent() 用 slog 输出。
│   │                                 RedactPath() 对路径做 SHA256 前 6 字节哈希，确保即使日志外泄
│   │                                 也无法反推原始路径。永远不输出 token、cookie 或密码
│   │
│   ├── cloud/                        云存储抽象层——接口定义 + 三种实现
│   │   │
│   │   ├── cloud.go                  核心接口：
│   │   │                             Provider（List/Stat/Upload/Download/Mkdir/Rename/Remove）—
│   │   │                             供 hddsyncd daemon 和 hddctl 共用
│   │   │                             DirectRemoteProvider（UploadFile/UploadDirectory/Copy/Move）—
│   │   │                             供 hddctl remote copy/move 专用
│   │   │                             四个冲突策略枚举：TransferConflictPolicy（fail/auto-rename/
│   │   │                             overwrite/merge）、UploadConflictPolicy 等
│   │   │
│   │   ├── mock/                     本地文件系统假 Provider（测试和开发用）
│   │   │   └── provider.go           MockProvider：把远端路径映射到本地目录。
│   │   │                             List → os.ReadDir、Stat → os.Stat、Upload → os.Create、
│   │   │                             Download → os.Open、Mkdir → os.MkdirAll、Remove → os.Remove、
│   │   │                             Rename → os.Rename、Move/Copy → 文件复制+删除。
│   │   │                             同时实现 Provider 和 DirectRemoteProvider，含路径穿越防护
│   │   │
│   │   ├── huadian/                  真实华电云盘 HTTP Provider——全仓库最大的单文件
│   │   │   └── provider.go           ~1600 行，调用 pan.ncepu.edu.cn 的 AnyShare REST API：
│   │   │                             List/Stat：GET /eos/... 解析 JSON 目录/文件列表
│   │   │                             Upload：POST multipart/form-data 上传文件
│   │   │                             Download：GET 流式下载
│   │   │                             Copy/Move：POST transfer API（ondup 参数控制冲突策略）
│   │   │                             Rename：同父目录重命名 API
│   │   │                             transfer()：Copy 和 Move 共用的内部实现，含源/目标检查、
│   │   │                             DocID 验证、冲突策略分发和后置校验
│   │   │
│   │   └── anyshare/auth/            AnyShare CAS/OAuth 统一认证子系统
│   │       ├── session.go             SessionManager：从文件加载/保存凭据、LoginInteractive() 登录调度、
│   │       │                          resolveLoginUI() 降级链（WebView2 → CDP → Console）、
│   │       │                          AuthTransport() 自定义 http.RoundTripper 自动注入 Bearer token
│   │       ├── login_cdp.go           CDP 模式：启动 Chrome/Edge → 浏览 CAS 登录页 → 用户手动登录 →
│   │       │                          CDP Network.getCookies 抓取 cookies + Network.requestWillBeSent
│   │       │                          拦截 Authorization header 提取 Bearer token + dir/list 请求体
│   │       │                          提取 RootDocID。含 pollForCookies 轮询、captureRootDocID、
│   │       │                          waitForCDP 等辅助函数
│   │       ├── login_console.go       控制台模式：提示用户从浏览器 DevTools 复制 Bearer token 粘贴到终端。
│   │       │                          parseBearerToken() 去掉 "Bearer " 前缀。无 cookie、无 RootDocID
│   │       ├── verification.go        ProbeSessionWithToken()：向 GET https://pan.ncepu.edu.cn/ 发
│   │       │                          请求验证 token 有效性。CheckSession() 仅用 Bearer token 探测
│   │       ├── redaction.go           RedactToken()：日志输出时 token 脱敏（显示前 6 位 + "..."）
│   │       ├── transport.go           自定义 http.RoundTripper，每次 HTTP 请求自动注入 Authorization:
│   │       │                          Bearer <token> header。GetToken 闭包从 SessionManager 实时获取
│   │       └── credential_test.go     （测试文件，略）
│   │
│   ├── mount/winfsp/                  WinFsp FUSE 文件系统——把远端云盘投影为 Windows 盘符
│   │   ├── ipcfs.go                  ~1520 行。daemon IPC 模式（主力文件）：
│   │   │                             IPCFileSystem：嵌入 fuse.FileSystemBase，持有 IPCClient。
│   │   │                             全部 FUSE 回调实现：Getattr（属性查询，含 handle 缓存和 IPC 降级）、
│   │   │                             Readdir（目录枚举 + 缓存 TTL 30s + . 和 .. 填充）、
│   │   │                             Access（DELETE_OK/W_OK 权限掩码，按模式分策略）、
│   │   │                             Open（只读打开 + new18 写标志路由到 createStagedUpload）、
│   │   │                             Create（目录→Mkdir、new18 文件→staged upload、其他→EROFS）、
│   │   │                             Write（受限模式仅允许 staged handle 写入 staging 文件）、
│   │   │                             Flush（staged handle 不上传，仅本地 sync）、
│   │   │                             Release（staged handle 提交 fs.uploadStaged 一次）、
│   │   │                             Rename→renameInFileRenameMode（文件同父/跨父分流）、
│   │   │                             renameOrMoveDirectory（目录 rename/move）、
│   │   │                             Unlink/Rmdir（new17/new18 放行，其他→EROFS）、
│   │   │                             Truncate（仅 staged handle 允许）、
│   │   │                             Utimens/Chmod/Chown/Setxattr/Removexattr（受限模式→EROFS）、
│   │   │                             Statfs（声明 200 GiB 容量）。
│   │   │                             权限投影：projectMode() 按模式给目录 0555/0777/0755、
│   │   │                             文件 0444/0666/0644。isRestrictedMode() 统一判断 8 种受限模式。
│   │   │                             createStagedUpload() 创建 staging 临时文件 + staged handle。
│   │   │                             ipcErrToFuse() 把 IPC 错误字符串映射为 POSIX errno
│   │   ├── cloudfs.go                ~250 行。直接 Provider 模式（hddfs mount --provider mock）：
│   │   │                             不经过 daemon 和 IPC，FUSE 回调直接调 prov.List/Stat/Upload。
│   │   │                             仅支持 mock，用于本地快速测试
│   │   └── memfs.go                  ~50 行。纯内存文件系统（hddfs memfs）。
│   │                                 用于验证 WinFsp 安装正常，无云盘功能
│   │
│   ├── platform/windows/              Windows 平台专用代码——Go 与 Win32 API / CGo 交互
│   │   │
│   │   ├── npipe/                     Windows Named Pipe 传输层
│   │   │   ├── server.go              NewServer(pipePath, handler) 服务端。Serve() 循环 Accept 连接，
│   │   │   │                         每连接开 goroutine 读 Request → 调 handler → 写 Response
│   │   │   ├── client.go              Dial(pipePath, timeout) 客户端连接。Call(req) 同步发 Request →
│   │   │   │                         等 Response → 返回。传输失败自动 Close 连接，下次 Call 重连
│   │   │   └── conn.go                ClientConn 结构体：conn net.Conn、reader bufio.Reader、
│   │   │                              每帧用 ipc.Encode/Decode 编解码
│   │   │
│   │   ├── service/                   Windows 服务管理
│   │   │   └── service.go             Install(name, display, desc)：用 sc.exe 注册 hddsyncd 为
│   │   │                              Windows 服务；Uninstall/Start/Stop 同。实现 Windows SCM 协议
│   │   │
│   │   ├── webview2/                  WebView2 登录窗口
│   │   │   └── login.go              启动内嵌 WebView2 窗口 → 导航到 CAS 登录 URL →
│   │   │                              等用户完成登录 → 通过 ICoreWebView2CookieManager 提取 cookies。
│   │   │                              用 Go WebView2 SDK（纯 Go，不依赖 CGo）
│   │   │
│   │   │   └── native/               CGo WebView2 Cookie 提取适配层
│   │   │       └── cookie.go          CGo 导出函数：从 WebView2 环境获取指定域名的全部 cookies。
│   │   │                              供 login.go 在纯 Go WebView2 SDK 不可用时降级使用
│   │   │
│   │   └── winfsp/                    WinFsp 挂载封装
│   │       └── fs.go                  Mount() 对 cgofuse 的 host.Mount() 做薄包装。
│   │                                  SetCapDeleteAccess() 启用 DELETE_OK 能力
│   │
│   ├── store/sqlite/                  SQLite 持久化存储——daemon 运行时的本地状态
│   │   ├── sqlite_store.go            Store 结构体（db *sql.DB + path）。Open(dir) 建表 + 迁移 →
│   │   │                             configure() 设置 WAL 模式 + 外键 → migrate() 执行版本迁移。
│   │   │                             canonicalLocal() 路径规范化。Close()、Path()。Now() 返回 Unix 时间戳
│   │   ├── tasks.go                   任务队列 CRUD：
│   │   │                             EnqueueOrMerge() 入队/合并（同路径+同操作幂等）、
│   │   │                             ClaimTask() CAS 抢占（UPDATE status='running' WHERE status='pending'）、
│   │   │                             CompleteTask() 标记成功（DELETE 或 UPDATE status='succeeded'）、
│   │   │                             MarkCancelled/MarkBlockedAuth/MarkNeedsReconcile 异常状态处理、
│   │   │                             ListPendingTasks() 分页查待执行任务、
│   │   │                             ResumeBlockedAuth() 解除认证阻塞恢复任务
│   │   ├── sync_roots.go              同步根 CRUD：
│   │   │                             AddSyncRoot() 插入、ListSyncRoots() 查询全部、
│   │   │                             Enable/Disable/Remove/SyncRootRow 结构体
│   │   ├── files.go                   文件元数据缓存：
│   │   │                             UpsertFile() 插入或更新、GetFile() 按路径查、
│   │   │                             ListFiles() 列出某个同步根下的全部文件
│   │   ├── settings.go                通用键值存储：GetSetting(key)、SetSetting(key, value)
│   │   ├── conflicts.go               同步冲突记录：记录了 local_path/remote_path/双端 etag/resolution
│   │   ├── migrate.go                 数据库迁移框架：按版本号顺序执行 migration 函数
│   │   ├── driver.go                  注册 modernc.org/sqlite 驱动
│   │   └── migrations/               将来版本的 SQL 迁移脚本目录（当前为空）
│   │
│   ├── sync/                          双向同步 diff 引擎（预留，尚未被任何入口导入）
│   │   └── syncer.go                  Syncer{provider, state}：Diff(localFiles, remoteFiles) 比对本地
│   │                                  和远端文件列表 → 产出 []Action（Upload/Download/Conflict）。
│   │                                  ConflictName() 生成冲突文件名。CreateConflictCopies() 创建
│   │                                  本地+远端冲突副本。copyFile() 本地文件复制
│   │
│   ├── watch/                         轮询式目录监控——检测本地文件变更并触发上传入队
│   │   └── watcher.go                 Watcher{store, roots, taskFn, delFn}：
│   │                                  Start() 启动 ticker（默认 2s）→ 每次 tick 执行 scan()
│   │                                  → filepath.WalkDir 遍历所有根目录 → 比较 ModTime 检测变化
│   │                                  → 新文件/修改放入 pending → 500ms 防抖后回调 taskFn 入队
│   │                                  → 删除检测：lastSeen 集合对比，消失文件回调 delFn 取消上传。
│   │                                  AddRoot(localPath, remotePath) 注册监视目录。
│   │                                  remotePathFor() 把本地路径转为远端路径
│   │
│   └── worker/                        异步任务消费池——daemon 后台驱动引擎
│       └── pool.go                    531 行。Pool{store, provider, filter}：Start() 启动 N 个 goroutine
│                                      对 upload/download/remove 三种任务类型各自 pollLoop（默认每 2s
│                                      查 SQLite pending 任务）→ processTask() 逐个处理：
│                                      filter 过滤 → 按路径串行化（pathLock）→ 根据 operation 分发到
│                                      processUpload（读本地→prov.Upload）、processDownload（prov.Download
│                                      →写本地缓存）、processDelete（prov.Remove）。
│                                      成功→CompleteTask() 删除任务，失败→MarkWithRetry 指数退避
│                                      （backoffDuration = min(2^n * 60s, 10min)，最多 8 次）。
│                                      session_expired→所有任务暂停等待重新认证（MarkBlockedAuth）
│
├── release/                          用户发行目录——放 EXE 和文档，不含任何源码
│   │
│   ├── README.md                      发行说明：运行环境、快速开始、功能列表、限制、FAQ、免责声明
│   ├── VERSION                        版本号 v0.1.0
│   ├── CHANGELOG.md                   各版本变更摘要
│   ├── SHA256SUMS.txt                 正式发布时填入三个 EXE 的 SHA256 校验和
│   │
│   ├── bin/                           可执行文件目录
│   │   ├── README.md                  hddsyncd.exe（daemon）、hddfs.exe（挂载）、hddctl.exe（维护工具）
│   │   │                              各自的功能说明和启动命令示例
│   │   └── .gitkeep                   空目录占位符，保证目录被 Git 跟踪
│   │
│   ├── scripts/                       辅助 PowerShell 脚本
│   │   ├── README.md                  start.ps1 和 Copy-ToHddMount.ps1 的详细说明及参数表
│   │   ├── start.ps1                  完整启动入口：检查 EXE/WinFsp/盘符/认证 → 隐藏启动 daemon + hddfs →
│   │   │                              自动打开资源管理器 → 控制终端显示停止命令。直接 Enter 不会停止，
│   │   │                              输入完整停止命令才执行 Stop-Process
│   │   ├── Copy-ToHddMount.ps1        安全复制上传脚本。支持 -OnConflict Fail（目标存在即 throw）、
│   │   │                              AutoRename（生成 "file (1).txt" 新名）、Prompt（交互选择）。
│   │   │                              Overwrite 当前不支持。上传后重试 Test-Path 校验目标可见性（最多 5s），
│   │   │                              成功输出 SHA256
│   │   ├── start-daemon.ps1           单独启动 daemon（占位脚本，当前不会执行进程操作）
│   │   ├── mount.ps1                  单独挂载盘符（占位脚本，当前不会执行进程操作）
│   │   └── stop.ps1                   停止 daemon 和 hddfs（占位脚本，当前不会执行进程操作）
│   │
│   ├── config/                        配置示例目录
│   │   ├── README.md                  配置项列表说明 + 安全提示（禁止在此存 token/密码）
│   │   └── config.example.json        推荐 JSON 配置模板：provider/pipe/mount/mode/dataDir/debugLog
│   │
│   ├── docs/                          面向用户的文档集
│   │   ├── README.md                  六份文档的快速索引 + 故障排查优先级（daemon stderr → hddfs debug log）
│   │   ├── QUICKSTART.md              快速开始：安装 WinFsp → 认证登录 → 启动 daemon → 挂载盘符
│   │   ├── USER_GUIDE.md              操作指南：浏览目录、新建目录、重命名、移动、删除、上传、冲突处理
│   │   ├── FEATURE_LIST.md            功能矩阵表：8 种模式 × 所有操作能力的逐项列出
│   │   ├── KNOWN_LIMITATIONS.md       已知限制：不支持在线编辑、不支持覆盖上传（原因说明）、无 GUI
│   │   ├── TEST_REPORT.md             Mock E2E 和真实云盘隔离目录测试结果汇总
│   │   └── TROUBLESHOOTING.md         排障指南：常见错误码、日志位置、pipe 占用、盘符冲突、认证过期
│   │
│   ├── logs/                          运行日志输出目录
│   │   ├── README.md                  五种日志文件的来源和内容说明及排查优先级
│   │   └── .gitkeep                   空目录占位符
│   │
│   └── data/                          本地数据目录
│       ├── README.md                  后续计划（SQLite 元数据/任务队列/同步根/缓存/staging）。
│       │                              当前数据库功能未实现，运行时数据由 --data-dir 参数另指定路径
│       └── .gitkeep                   空目录占位符
│
├── scripts/                          仓库级开发辅助脚本
│   ├── build.ps1                      手动编译三个 EXE 的脚本
│   ├── setup-chromium.ps1             下载并配置 CDP 登录专用的 Chromium 浏览器
│   ├── mount-memfs.ps1                hddfs memfs 快速挂载测试
│   └── unmount.ps1                    强制卸载指定盘符的 WinFsp 挂载
│
├── testdata/                          测试数据目录
│   └── mock-cloud/                    Mock Provider 使用的本地文件结构
│       ├── hello.txt                  根目录测试文件
│       └── 课程资料/a.txt              中文路径测试文件（Unicode 文件名）
│
└── docs/                              项目设计文档（面向开发者）
    ├── requirements.md                 需求规格
    ├── architecture.md                 系统架构图
    ├── ipc-protocol.md                 Named Pipe IPC 协议设计
    ├── sync-state-machine.md           同步状态机设计
    ├── windows-design.md               Windows 平台设计说明
    ├── demo-plan.md                    演示计划
    ├── milestones.md                   里程碑规划
    ├── auth-usage.md                   认证使用说明
    └── buildlog1.md                    全阶段 AI 辅助开发总报告（Codex+OpenCode）
```


# 剩余工作清单

依据 2026-07-02 功能验收结果排序。每一项应单独修复、增加回归测试并执行全仓库规定检查。

## P0：必须立即完成

### P0-1 移除敏感网络诊断输出

- 目标：任何日志不得包含响应正文、Cookie/Token、Authorization、对象存储 `authrequest` 或临时签名 URL。
- 涉及模块：`internal/cloud/huadian`、`internal/cloud/anyshare/auth`、logging。
- 验收标准：日志只保留方法、脱敏路径、状态码、请求 ID；秘密扫描和单测证明哨兵 Token/URL 不出现。
- 建议测试：为 401/403/404/500、dir/list、osdownload、对象存储失败注入秘密值并捕获 stderr/log。
- 依赖条件：无。
- 是否需要真实服务器：否。

### P0-2 实现真正的 SQLite 元数据存储（已完成）

- 完成情况：已使用 modernc SQLite 替换 `.row` 文本目录，提供 migrations、事务、约束、并发领取、同路径串行和 stale running 恢复。
- 涉及模块：`internal/store/sqlite`、`migrations/`、worker、daemon。
- 验收标准：存在数据库 schema；files/tasks/sync_roots/conflicts/settings 均以 SQL 操作；任务 claim 和状态提交为事务；失败回滚；重启恢复通过。
- 建议测试：`t.TempDir` 数据库、并发 claim、故障回滚、迁移升级、stale running 恢复、数据库损坏错误传播。
- 依赖条件：选定并锁定 SQLite 驱动；不得把数据库放入仓库。
- 是否需要真实服务器：否。

## P1：答辩前必须完成

### P1-1 装配真实 hddsyncd

- 目标：daemon 加载 Session、Provider、SQLite 同步根和待办任务，并启动 watcher、filter、上传/下载有界 worker pool。
- 涉及模块：`cmd/hddsyncd`、auth、cloud、store、watch、filter、worker。
- 验收标准：无 Session 可启动并报告未认证；有 Session 恢复任务；Ctrl+C/service stop 等待 goroutine；不再硬编码 Mock 测试文件。
- 建议测试：依赖注入 Mock 的 daemon 集成测试、启动/停止泄漏测试、重启恢复、离线再上线。
- 依赖条件：P0-2。
- 是否需要真实服务器：核心集成否；最终验收是。

### P1-2 闭环真实登录和跨进程文件 API 403

- 目标：证明登录保存的 Cookie、API Token、RootDocID 在新进程中可执行 `remote ls /`。
- 涉及模块：WebView2/CDP、SessionManager、Huadian Provider、CLI。
- 验收标准：脱敏记录展示 `user/get` 200 后关闭登录进程，新进程 `dir/list` 200；401/403 能明确区分过期、WAF 和权限错误。
- 建议测试：持久化 Session 的进程级 fake server 测试；真机只读登录→退出→ls/stat。
- 依赖条件：Windows、WebView2 或 Edge/Chrome、校园认证/VPN、测试账号。
- 是否需要真实服务器：最终验收需要，只读即可。

### P1-3 完成可靠双向同步

- 目标：实现本地/远端创建、修改、删除、重命名、目录操作、冲突副本和断线重试。
- 涉及模块：sync、watch、worker、store、Provider。
- 验收标准：所有动作经持久队列；同 canonical path 串行；去重、指数退避、最大重试和冲突记录可观察；同步根外文件永不删除。
- 建议测试：Unicode/中文、空格、长路径、大小写、rename、断网、500、进程崩溃、双端同时修改。
- 依赖条件：P0-2、P1-1。
- 是否需要真实服务器：大多数否；最终端到端需要隔离目录。

### P1-4 完成 WinFsp IPCFS 真实 daemon 链

- 目标：Explorer 回调只访问缓存/IPC，下载不在 WinFsp 回调中无限等待，写入经 SQLite 持久任务队列。
- 涉及模块：hddfs、IPCFS、npipe、daemon、cache、worker。
- 验收标准：读缓存命中不重复下载；Write 只写缓存；Flush/Release 幂等入队；daemon/网络断开有确定错误；句柄释放与卸载无死锁。
- 建议测试：WinFsp 真机只读/读写、并发 Explorer、断 daemon、断网、重复 Flush、中文/空格/保留名、race。
- 依赖条件：WinFsp SDK/runtime、CGO 工具链、P1-1/P1-3。
- 是否需要真实服务器：Mock 挂载否；答辩真实云演示需要。

### P1-5 完成 Windows Service 运行模式

- 目标：SCM 启动真正进入 service dispatcher，并明确服务账号的数据目录和凭据访问模型。
- 涉及模块：`cmd/hddsyncd`、platform/windows/service、logging/config/auth。
- 验收标准：install/start/stop/uninstall 可重复；stop 触发 context 取消；Event Log 可诊断且无秘密；服务可按设计访问目标用户的 Session。
- 建议测试：管理员 Windows VM 中安装/升级/停止超时/异常退出测试。
- 依赖条件：管理员权限和隔离 Windows VM。
- 是否需要真实服务器：服务生命周期否；认证同步验收需要。

### P1-6 恢复全仓库质量门禁

- 目标：规定的 test/race/vet/build 全部通过。
- 涉及模块：构建脚本、WinFsp build tags、npipe 测试环境、格式化。
- 验收标准：`gofmt -l .` 无输出；四条规定命令退出码 0；在干净 Windows 环境可复现。
- 建议测试：Windows CI 分为非 WinFsp 与安装 WinFsp SDK 的完整 job。
- 依赖条件：WinFsp 头文件/runtime、允许创建 Named Pipe 的会话。
- 是否需要真实服务器：否。

## P2：建议完成

### P2-1 完整 CLI 管理面

- 目标：实现 `auth diagnose`、`sync list/pause/resume`、`task list`、`conflict list`，并稳定别名 `rename/delete`。
- 涉及模块：hddctl、store、IPC。
- 验收标准：所有命令查询/修改 daemon 的持久状态，不另起临时 Mock。
- 建议测试：命令表驱动测试、daemon 不在线、非法参数、分页和 Unicode。
- 依赖条件：P0-2、P1-1。
- 是否需要真实服务器：否。

### P2-2 `.hddignore` 与入队过滤

- 目标：支持规则文件、通配符、目录、大小、优先级/否定，并在任务持久化前应用。
- 涉及模块：filter、watch、scheduler、CLI rule。
- 验收标准：`rule test` 与实际入队结论一致；size 规则读取真实文件信息；规则变更行为明确。
- 建议测试：嵌套目录、中文、空格、大小边界、优先级、invalid pattern。
- 依赖条件：同步调度入口。
- 是否需要真实服务器：否。

### P2-3 持久缓存状态机与配额

- 目标：原子下载、dirty 状态、失败保留/清理、容量上限、淘汰、多进程协调和重启恢复。
- 涉及模块：cache（建议独立包）、daemon、IPCFS、store。
- 验收标准：临时文件仅在校验成功后原子替换；dirty 文件不淘汰；路径不能逃逸；磁盘满可恢复。
- 建议测试：断电点故障注入、磁盘满、并发句柄、junction/reparse、LRU 边界。
- 依赖条件：P0-2。
- 是否需要真实服务器：否。

### P2-4 大文件流式传输与限制

- 目标：上传不整文件入内存；API 成功响应有合理上限；下载受磁盘配额和校验控制。
- 涉及模块：Huadian Provider、CLI、cache。
- 验收标准：多 GiB 测试流的内存保持有界；size/hash 不符不 finalize；临时文件正确清理。
- 建议测试：生成式 reader、短读、超长响应、对象存储超时、校验失败。
- 依赖条件：确认 AnyShare 分片/流式协议。
- 是否需要真实服务器：协议最终确认需要。

### P2-5 Windows 路径安全加固

- 目标：统一 canonical path、安全同步根检查、reparse/junction、保留名、大小写和长路径策略。
- 涉及模块：Mock、cache、watch、sync、IPCFS。
- 验收标准：任何写/删/rename 均不能逃出根；`CON/NUL/AUX` 等拒绝或正确映射；TOCTOU 风险有明确控制。
- 建议测试：Unicode、混合大小写、`..`、绝对/UNC/卷路径、junction/symlink、长路径。
- 依赖条件：Windows NTFS 测试环境。
- 是否需要真实服务器：否。

### P2-6 仓库卫生

- 目标：在单独变更中移除已跟踪缓存、工具链副本、数字后缀备份、HAR、测试下载和无关二进制，并完善 ignore。
- 涉及模块：仓库根、`.gitignore`、构建文档。
- 验收标准：`git ls-files` 不含凭据/HAR/缓存/构建产物；全新 clone 可按文档构建。
- 建议测试：秘密扫描、clean clone build。
- 依赖条件：需要维护者确认哪些取证文件可归档；不得在修复批次中误删用户文件。
- 是否需要真实服务器：否。

## P3：扩展或展望

### P3-1 可观测性和运维

- 目标：结构化脱敏日志、任务指标、健康检查和故障导出。
- 涉及模块：logging、daemon、service、CLI diagnose。
- 验收标准：能定位认证/网络/队列/缓存问题且不暴露秘密。
- 建议测试：日志 schema、redaction property test、Event Log 集成。
- 依赖条件：核心链路稳定。
- 是否需要真实服务器：否。

### P3-2 性能与规模验证

- 目标：大量文件、长时间运行、限速、公平调度和缓存命中性能基线。
- 涉及模块：watch、store、worker、cache、IPCFS。
- 验收标准：10 万文件扫描与队列恢复达到约定 SLO，无 goroutine/句柄泄漏。
- 建议测试：benchmark、24 小时 soak、race、故障注入。
- 依赖条件：完整同步和 WinFsp 链。
- 是否需要真实服务器：可先 Mock；最终容量测试可用测试租户。

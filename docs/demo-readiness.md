# 答辩演示准备度

评估日期：2026-07-02

## 结论

当前等级：**只能展示 Mock**。

这是保守且可复现的等级。多数非 WinFsp Go 包和 Mock 功能测试通过；真实 Huadian Provider 只有 `httptest.Server` 证据，登录 UI、跨进程真实目录、真实上传下载、自动云同步及 WinFsp 挂载均无本次真实环境成功证据。全量 build/test/vet/race 也未通过。

## 等级判定

| 候选等级 | 判定 | 证据 |
| --- | --- | --- |
| 不可运行 | 否 | hddctl、daemon 及多数包测试通过；工作区也有旧 exe，但不作为源码构建证据 |
| 只能展示 Mock | **是** | Mock Provider、CLI remote 适配、watch/worker/filter/文本 store 有自动测试；daemon 和 WinFsp 后端实际使用 Mock |
| 可以演示登录 | 否 | WebView2/CDP 代码存在，但无本次真实 CAS/OAuth2 成功证据 |
| 可以演示真实云盘 CLI | 否 | Huadian API 只有 httptest；历史新进程 dir/list 403 未闭环 |
| 可以演示自动同步 | 否 | 只有 `hddctl sync run` 的临时 Mock 单向链；daemon 未装配同步引擎 |
| 可以演示只读 WinFsp | 否 | 当前缺 `fuse_common.h`，无法重建/挂载；IPC 后端仍为 Mock |
| 可以演示可写 WinFsp | 否 | 无真机挂载；写入不经 SQLite 持久任务队列，只同步上传 Mock |
| 可以完整答辩演示 | 否 | 构建门禁、真实认证/云 API、自动同步、WinFsp 和卸载均未验收 |

## 可安全演示的范围

在不使用真实凭据的前提下，可演示：

1. `hddctl help/version`。
2. `hddctl --mock remote ...` 的单命令行为；注意每次 remote 命令使用新的临时目录，不能展示跨命令持久云状态。
3. filter、watcher、worker、冲突算法和文本任务恢复的自动测试结果。
4. Huadian Provider 针对 fake HTTP server 的请求流程测试；必须明确它不是华电云盘实测。

不应演示或宣称：

- 真实华电云登录、跨进程 Session 可用或真实目录列表成功。
- SQLite 已完成真实数据库替换；但 daemon 尚未装配该持久队列，因此不能据此宣称自动同步完成。
- daemon 自动同步；daemon 只运行 Mock FS IPC 和轮询。
- WinFsp 真实云盘读写；旧 exe 或旧手工报告不能证明当前源码状态。
- Windows Service 可用；只有 SCM 管理函数，主 daemon 未接 service dispatcher。

## 演示前最低门槛

要提升到“可以演示真实云盘 CLI”，至少需要：

1. 删除敏感响应、authrequest 和签名 URL日志。
2. 在隔离 Windows 环境完成一次人工登录，并以新进程只读执行 `remote ls /`、`remote stat`、下载校验。
3. 对历史 `user/get=200`、`dir/list=403` 留下脱敏的根因和回归证据。
4. 全量非 WinFsp test/vet/build 通过，明确 WinFsp job 的独立阻塞状态。

要提升到“可以演示自动同步/可写 WinFsp”，还必须先完成真实 SQLite、daemon 装配、持久任务恢复、WinFsp SDK 构建以及 Explorer 断网/卸载验收。

## 本次可复现命令状态

```text
go list ./...        PASS（伴随 Go telemetry 权限警告）
gofmt -l .           输出 internal/cloud/anyshare/auth/login_cdp.go
go test ./...        FAIL（WinFsp 头文件；Named Pipe 权限）
go test -race ./...  FAIL（同上；其余运行包未报告 race）
go vet ./...         FAIL（WinFsp 头文件）
go build ./cmd/...   FAIL（WinFsp 头文件）
```

因此当前答辩准备度不能高于“只能展示 Mock”。

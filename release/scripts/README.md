# 脚本说明

本目录包含 PowerShell 辅助脚本，用于启动/停止挂载环境以及安全文件复制。

### start.ps1 — 一键启动脚本

完整的自动启动入口，执行以下步骤：

1. 检查三个 EXE 是否在 `bin/` 目录中
2. 检测 WinFsp 是否已安装
3. 查找可用盘符
4. 执行 `hddctl remote ls /` 探测认证状态；必要时调用 `hddctl login`
5. 选择幂等登录器（优先 WebView2 CDP，降级为 CAS 模拟）
6. 隐藏启动 `hddsyncd.exe`（daemon）和 `hddfs.exe`（挂载程序）
7. 等待盘符出现后自动打开资源管理器
8. 控制终端显示停止命令，保持打开

```powershell
.\scripts\start.ps1
.\scripts\start.ps1 -Mount T: -Pipe \\.\pipe\huadian-drive
```

daemon 和 hddfs 的标准输出/错误均写入 `logs/` 目录。直接按 Enter 不会停止进程，必须输入终端显示的完整停止命令才会执行 `Stop-Process`。

### Copy-ToHddMount.ps1 — 安全复制上传脚本

将本地文件复制到挂载盘目录，支持冲突策略选择：

| 参数 | 行为 |
|---|---|
| `-OnConflict Fail` | 目标存在时立即抛出错误，不执行复制，原文件不变 |
| `-OnConflict AutoRename` | 目标存在时自动生成唯一文件名（如 `a (1).txt`），保留原文件 |
| `-OnConflict Prompt` | 目标存在时显示复选框，允许用户选择跳过/AutoRename |
| `-OnConflict Overwrite` | 当前不支持，脚本会明确报错 |

```powershell
.\scripts\Copy-ToHddMount.ps1 -Source "D:\local\report.pdf" -Destination "S:\docs\report.pdf" -OnConflict AutoRename
```

上传完成后输出目标路径、SHA256 和文件大小。AutoRename 模式会挂载验证目标可见性（最多重试 5 秒）后再校验哈希。

### 占位脚本

- `start-daemon.ps1`：单独启动 daemon 的占位脚本，当前不会执行进程操作。
- `mount.ps1`：单独挂载的占位脚本，当前不会执行进程操作。
- `stop.ps1`：停止 daemon 和 hddfs 的占位脚本，当前不会执行进程操作。

推荐直接使用 `start.ps1` 一键启动，其输出包含精确停止命令。执行策略限制可临时解除：

```powershell
Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass
```

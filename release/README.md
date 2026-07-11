# 华电云盘挂载客户端

## 当前版本

v0.1.0，课程项目/原型发行版（安全上传版本）。项目使用 Go 与 WinFsp，将华电云盘挂载为 Windows 盘符，主要通过资源管理器和命令行使用。当前没有 GUI。

## 运行环境

- Windows 10 或 Windows 11；
- 已安装 WinFsp；
- 可访问华电云盘的网络；
- 可用账号以及已准备好的登录状态或 token；
- PowerShell。

请勿把 token、cookie、密码或账号信息放进发行包、日志或截图。

## 发行目录

```
release/
├── bin/        可执行文件目录
├── scripts/    辅助脚本目录
├── config/     配置文件目录
├── docs/       文档目录
├── logs/       运行日志目录
├── data/       本地数据目录（未实现）
├── VERSION     版本号文件
├── CHANGELOG.md     变更记录
└── SHA256SUMS.txt   校验和文件
```

### bin/

存放三个可执行文件：`hddsyncd.exe`（daemon）、`hddfs.exe`（WinFsp 挂载程序）、`hddctl.exe`（远程维护工具）。发行包中此目录为空，运行时由用户将构建产物放入。

### scripts/

辅助 PowerShell 脚本：

- `start.ps1`：一键启动 daemon 和挂载。自动检测 WinFsp、可用盘符、认证状态，隐藏启动后台进程，打开资源管理器。停用命令在控制终端显示。
- `Copy-ToHddMount.ps1`：安全复制上传脚本。支持 `-OnConflict Fail`、`AutoRename`、`Prompt` 三种冲突策略，上传后校验 SHA256。Overwrite 模式当前不支持。

### config/

推荐配置文件示例。当前程序主要通过命令行参数运行（`--provider`、`--pipe`、各模式标志等），配置示例为后续支持配置文件启动预留。

### docs/

完整文档集：

- `QUICKSTART.md`：快速开始，从安装到挂载的完整步骤。
- `USER_GUIDE.md`：用户操作指南，覆盖浏览、重命名、移动、删除、上传及冲突处理。
- `FEATURE_LIST.md`：功能矩阵，列出所有已实现和未实现的能力。
- `KNOWN_LIMITATIONS.md`：已知限制，包括不支持在线编辑、不支持覆盖上传等技术原因。
- `TEST_REPORT.md`：测试报告，汇总 Mock E2E 和真实云盘隔离目录测试结果。
- `TROUBLESHOOTING.md`：排障指南，常见错误码、日志位置和诊断方法。

### logs/

运行日志输出目录。daemon 和 hddfs 的 stdout/stderr 及 hddfs debug 日志均写入此目录。发行包中为空，首次运行后自动生成日志文件。故障排查时优先检查此目录。

### data/

本地运行数据目录。计划用于存放 SQLite 元数据库（`metadata.db`）、本地缓存文件和 staging 暂存目录。

**当前状态：数据库功能未实现。** 以下为后续补充计划：

- 元数据缓存（远端目录/文件信息本地存储）
- 同步任务队列（upload/download/remove 持久化，重启后恢复）
- 同步根注册（本地目录与远端路径的映射关系）
- 本地文件缓存（已下载文件的本地副本管理）
- Staging 暂存区（上传前本地临时文件目录）

目前 daemon 运行不依赖本目录。发行包中此目录仅含 `.gitkeep` 占位文件。

### 根目录文件

- `VERSION`：当前版本号（v0.1.0）。
- `CHANGELOG.md`：各版本变更摘要。
- `SHA256SUMS.txt`：正式发布时记录三个 EXE 的 SHA256 校验和，当前为空模板。

本目录不包含私人 token、cookie、metadata.db、云盘数据、真实日志或 EXE。

## 可执行文件

- hddsyncd.exe：后台 daemon，执行真实云盘 API 操作。
- hddfs.exe：WinFsp 文件系统挂载程序。
- hddctl.exe：remote ls/stat/upload/download 等维护工具。

若构建产物仍名为 *.newXX.exe，正式发布前建议重命名为以上标准名称，或相应修改命令。

## 快速开始

1. 安装 WinFsp，解压发行目录，把三个 EXE 放入 bin/。
2. 在发行目录之外安全准备认证信息。
3. 在 PowerShell 进入目录：

       Set-Location D:\ncepupan\release

4. 启动 daemon（以下是一行命令）：

       .\bin\hddsyncd.exe run --provider huadian --pipe \\.\pipe\huadian-drive-release --mkdir-rename-move-file-rename-move-remove-copy-upload-only --no-background

5. 另开 PowerShell 挂载盘符：

       .\bin\hddfs.exe mount --daemon --pipe \\.\pipe\huadian-drive-release --mount S: --mkdir-rename-move-file-rename-move-remove-copy-upload-only --debug-log .\logs\hddfs.debug.log

6. 打开资源管理器中的 S:。
7. 停止时先关闭正在访问挂载盘的窗口，再运行：

       Get-Process hddfs,hddsyncd -ErrorAction SilentlyContinue | Stop-Process -Force

也可以运行 .\scripts\start.ps1。它会完成环境和认证检查，在后台隐藏启动 daemon 与挂载程序，只保留当前 PowerShell 界面，并自动打开资源管理器。运行输出保存在 logs/；成功后控制终端显示停止命令并保持打开，直接按 Enter 不会停止，输入完整停止命令才会结束两个后台程序。首次使用只在测试目录验证。

## 使用方式

### 浏览与下载

在资源管理器打开挂载盘即可浏览目录。打开远端文件或复制到本地会触发读取/下载。

### 目录操作

支持新建目录、文件和目录重命名、文件和目录移动、文件删除、空目录删除及非空目录递归删除。删除和移动是真实远端操作，执行前务必核对目标。

### 上传

把本地新文件复制到挂载盘即可执行单文件 staging upload。一次复制多个文件按多次单文件上传处理；复制目录会递归创建目录并逐文件上传。

### 同名冲突

同名文件不会自动覆盖。资源管理器出现冲突时请选择“跳过”或“取消”，不要选择“替换目标中的文件”。保留两份可运行：

       .\scripts\Copy-ToHddMount.ps1 -Source C:\local\a.txt -Destination S:\upload\a.txt -OnConflict AutoRename

helper 策略：

- Fail：目标存在立即失败。
- AutoRename：生成 a (1).txt 等新名称。
- Prompt：允许选择跳过或 AutoRename。
- Overwrite：当前不支持，脚本会明确失败，不会误报 Uploaded。

## 已实现功能

- 云盘目录浏览和文件读取/下载；
- 新建目录；
- 文件/目录重命名和移动；
- 文件删除、空目录删除、非空目录递归删除；
- 单文件、多文件以及目录递归复制上传；
- 同名文件默认不覆盖，跳过/取消安全；
- AutoRename 辅助上传；
- 拒绝写入或在线编辑已有远端文件，避免误修改。

## 未实现功能与限制

- 覆盖上传不支持：Explorer Replace、Copy-Item -Force 和 helper Overwrite 均不作为支持功能。cgofuse Go 回调不能可靠暴露 WinFsp create disposition/options，无法安全区分普通冲突和明确覆盖。
- 在线编辑不支持：已有文件可读和下载，但不能直接编辑保存。安全实现还需要截断、追加、临时替换、回滚及并发一致性设计。
- 无 GUI；资源管理器提供基础图形化体验。
- 不是实时双向同步盘，不保证网页端变化实时推送。
- 不支持断点续传、分片上传或服务端 batch upload。
- 多客户端同时修改同一路径时不提供强一致性保证。

## 安全设计

默认拒绝覆盖，避免资源管理器探测行为无提示替换远端文件。同名文件应跳过、取消或 AutoRename。上传只有经 staging、服务端调用和结果验证成功后才报告成功；失败不得先删除旧远端。

## 日志

建议分别保存 logs/daemon.stdout.log、daemon.stderr.log、hddfs.stdout.log、hddfs.stderr.log 和 hddfs.debug.log。故障时先检查 daemon stderr 和 hddfs debug log，再核对 pipe、盘符占用、WinFsp 和 token。对外提供日志前必须脱敏。

## 常见问题

问：为什么同名文件不能覆盖？

答：当前是安全上传版本，请跳过/取消或使用 AutoRename。

问：能直接编辑文件并保存吗？

答：不能。只支持读取已有文件和上传新路径。

问：为什么没有 GUI？

答：项目重点是挂载和文件系统映射，资源管理器已提供基础界面。

问：盘符没有出现怎么办？

答：检查 WinFsp、hddfs 进程、盘符占用、pipe 和日志。

问：访问被拒绝是不是 bug？

答：不一定。同名冲突、覆盖和在线编辑被拒绝是当前设计行为。

问：目录复制支持吗？

答：支持，本质是递归 mkdir 加多次单文件上传。

## 开发与构建

发行用户通常无需构建。开发者可在源码仓库执行下列命令；本轮未执行：

       go build -p=1 -o bin\hddsyncd.exe ./cmd/hddsyncd
       go build -p=1 -o bin\hddfs.exe ./cmd/hddfs
       go build -p=1 -o bin\hddctl.exe ./cmd/hddctl

## 免责声明

本项目用于课程、学习和原型验证。请先在测试目录验证，不建议直接在重要云盘目录批量操作。删除和移动会作用于真实远端，使用者需自行确认风险。

# 华电云盘 Windows 客户端开发报告（Codex 部分）

## 1. 报告范围

本文只记录 2026-07-06 本 Codex 会话中由 Codex 直接分析、修改和验证的工作，不代表项目全部开发历史，也不认领其他编程体、用户或既有提交完成的功能。

用户执行的 Mock/真实环境手工测试在本文中仅作为需求输入或外部验证证据；Codex 未执行真实云盘操作。删除、重命名、移动、认证、云端 provider 等既有能力若不是本会话实现，不列为 Codex 的开发成果。

## 2. 时间口径与证据

- 时区：Asia/Shanghai（UTC+8）。
- 阶段开始时间：以对应需求附件的创建时间为准。
- 阶段结束时间：以本阶段最后修改文件的 LastWriteTime 或最后一次验证记录为准。
- 文件系统时间只能证明记录/落盘时间，不能精确表示全部思考过程，因此表内时间为可审计的近似工作区间。
- 本会话没有 Git 提交，不能使用 commit 时间划分这些阶段。

| 阶段 | 开始时间 | 结束时间 | 时间证据 |
|---|---:|---:|---|
| new18 默认覆盖安全修复 | 2026-07-06 20:23:01 | 2026-07-06 20:46:07 | new18 需求附件至 new19 测试反馈附件 |
| new19 覆盖能力调查与安全收敛 | 2026-07-06 20:46:07 | 2026-07-06 20:51:39 | new19 附件及 ipcfs 测试文件修改时间 |
| 外发 release 目录整理 | 2026-07-06 21:48:02 | 2026-07-06 约 22:14 | release 需求附件、README 创建时间及启动脚本创建时间 |
| 单终端启动控制脚本 | 2026-07-06 22:14:15 | 2026-07-06 22:25:46 | start.ps1 创建时间及 README 最后修改时间 |

## 3. 阶段一：new18 默认同名复制自动覆盖修复

### 目标

消除资源管理器或 PowerShell 向已存在路径复制文件时可能自动覆盖远端文件的数据安全风险，同时保持新路径上传和 AutoRename 能力。

### 遇到的问题

new18 的 staged upload 路由中存在以下判定：

    destination exists && isCreate => conflictPolicy=overwrite

资源管理器在普通同名冲突探测中也可能调用 Create，因此“进入 Create 回调”不等价于“用户明确选择覆盖”。这会让尚未确认替换的请求直接进入 overwrite。

### 原因

1. 将 FUSE Create 回调错误地当成 Windows CREATE_ALWAYS/Replace 的充分证据。
2. 覆盖意图判断散落在 createStagedUpload 内部。
3. 当前 cgofuse 回调只提供 path、flags、mode，没有直接提供 Windows create disposition/options。

### 最终解决办法

1. 将覆盖判定集中到 isExplicitOverwriteRequest。
2. existing Create 默认返回 EEXIST，不创建 staging handle，不调用上传。
3. Open(existing) 只有带明确 O_TRUNC 时才允许 staged overwrite。
4. O_WRONLY、O_RDWR、O_APPEND、O_CREATE、W_OK 和 isCreate 单独都不能触发 overwrite。
5. 增加日志字段：flags、trunc、isCreate、explicitOverwrite、conflictPolicy 和拒绝原因。
6. 增加回归测试，覆盖：
   - 新路径 Create 使用 conflictPolicy=fail；
   - existing Create 返回 EEXIST；
   - write flags 不能单独触发 overwrite；
   - Open(existing)+O_TRUNC 使用 overwrite；
   - new16/new17 旧模式继续拒绝上传。
7. 改进 Copy-ToHddMount.ps1：非 Overwrite 分支不再无条件使用 Force；上传后重试读取并比较源/目标 SHA256，验证失败不报告 Uploaded。

### 验证

Codex 执行并通过：

- go test -p=1 -count=1 -timeout=120s ./internal/mount/winfsp/...
- go test -p=1 -count=1 -timeout=120s ./cmd/hddfs/...
- go test -p=1 -count=1 -timeout=120s ./cmd/hddsyncd/...
- go test -p=1 -count=1 -timeout=120s ./internal/ipc/...
- go test -p=1 -count=1 -timeout=120s ./internal/cloud/mock/...
- go vet ./internal/mount/winfsp/...
- PowerShell 静态语法检查
- git diff --check

用户随后提供的外部手工测试结果表明：新文件复制、默认同名不覆盖、Fail 和 AutoRename 通过。该手工测试由用户执行，不属于 Codex 执行的测试。

## 4. 阶段二：new19 覆盖能力调查与安全收敛

### 目标

调查 Explorer“替换目标中的文件”、Copy-Item -Force 和 helper Overwrite 是否能在不牺牲默认冲突安全的前提下得到支持。

### 遇到的问题

1. Explorer Replace 仍返回访问被拒绝。
2. Copy-Item -Force 和 helper Overwrite 仍失败。
3. 没有可用的 new19 Explorer Replace 实际 debug log。
4. cgofuse v1.6.0 的 FileSystemInterface.Create 签名只有：

       Create(path string, flags int, mode uint32)

   Go 层拿不到原始 WinFsp create disposition/options/access mask。

### 原因

Windows 的普通冲突探测和显式 Replace 最可靠的区别应来自 CREATE_NEW、CREATE_ALWAYS、TRUNCATE_EXISTING、FILE_OVERWRITE 等 disposition。当前绑定没有暴露这些字段，单凭 Create、write access 或 W_OK 推断覆盖都会重新引入数据损失风险。

调查中还发现 callback flags 属于 cgofuse 常量域，不能混用 Go os.O_EXCL 和 fuse.O_EXCL；不同常量域可能导致排他标志判断错误。

### 尝试与纠正

Codex 曾在局部测试中尝试把 Create+O_TRUNC 视为覆盖，但复核后发现：没有实际回调日志就不能证明普通同名 Create 不携带 O_TRUNC。根据“宁可 Replace 不可用，也不能默认覆盖”的安全底线，该放宽没有作为最终方案保留。

### 最终解决办法

1. 保持 fail-closed：existing Create 继续返回 EEXIST。
2. 只保留 Open(existing)+O_TRUNC 为显式 overwrite。
3. Create/Open debug log 明确输出 disposition=unavailable、options=unavailable、accessMask=unavailable，避免日志让人误以为已经取得这些字段。
4. Release staged commit 日志增加 staging SHA256、size、conflictPolicy 和 verifiedSuccess。
5. 使用 fuse.O_EXCL 等 cgofuse 常量解释 callback flags。

### 阶段结论

本阶段没有宣称 Explorer Replace 已实现。安全版本继续明确不支持：

- Explorer Replace；
- Copy-Item -Force 覆盖；
- helper Overwrite；
- 在线编辑已有远端文件。

后续若要实现，必须先取得实际 Replace 回调序列，或 fork/扩展 cgofuse/WinFsp 绑定，把 create disposition/options 暴露到 Go 层。

## 5. 阶段三：外发 release 目录整理

### 目标

在 D:\ncepupan\release 创建可供外部人员阅读和使用的发行目录，不包含 EXE、凭据、数据库、真实日志或测试缓存。

### 遇到的问题

1. 仓库工作树已有大量无关未提交文件和 .gocache 噪声，不能清理或误纳入发行目录。
2. 首次生成补丁时，Markdown/PowerShell 反引号与补丁载体冲突，补丁未应用。
3. Windows PowerShell 5 将无 BOM 的 UTF-8 中文占位脚本按本地编码解析，导致字符串语法错误。

### 原因

1. release 是新建未跟踪目录，普通 git diff --stat 不统计其内容。
2. 补丁工具输入本身使用 JavaScript 模板字符串，未转义反引号会提前结束字符串。
3. Windows PowerShell 5 对无 BOM UTF-8 的兼容性较差；中文字符误解码后可能破坏引号解析。

### 最终解决办法

1. 使用 apply_patch 创建独立 release 结构，只写入公开文档、JSON、PS1 和 .gitkeep。
2. 文档代码示例改用缩进格式，避免补丁载体反引号冲突。
3. 可执行占位脚本使用 ASCII 文本；中文说明保留在 Markdown。
4. 创建内容：
   - README.md、VERSION、CHANGELOG.md、SHA256SUMS.txt；
   - bin/README.md；
   - config/config.example.json 和配置说明；
   - QUICKSTART、USER_GUIDE、FEATURE_LIST、KNOWN_LIMITATIONS、TEST_REPORT、TROUBLESHOOTING；
   - logs/.gitkeep、data/.gitkeep；
   - scripts 文档和安全 helper。
5. README 明确已实现功能、安全边界、覆盖/在线编辑限制、日志位置、FAQ、构建示例和免责声明。
6. Copy-ToHddMount.ps1 的 Overwrite 分支明确失败；Fail/AutoRename 保留，成功后校验 SHA256。

### 验证

- release 初始共 20 个文件；
- config.example.json 可被 ConvertFrom-Json 正确解析；
- PowerShell 文件静态语法检查通过；
- EXE 数量为 0；
- metadata.db、.gocache、.tmp、mock-root 等禁止文件数量为 0；
- git diff --check 通过；
- 未运行 go build、go install 或真实云盘命令。

## 6. 阶段四：单终端启动控制脚本

### 目标

提供 release/scripts/start.ps1，实现：

1. 环境与 EXE 检查；
2. WinFsp 和盘符检查；
3. hddctl remote ls / 认证探测；
4. 探测失败时执行 hddctl login，并再次探测；
5. 隐藏启动 daemon 和 hddfs；
6. 挂载成功后打开目标盘符的资源管理器；
7. 从启动到停止只保留一个可见 PowerShell 控制终端。

### 遇到的问题

需求经历三次交互收敛：

1. 初版设计让 daemon 和 hddfs 各自显示 PowerShell 窗口，不符合“只有一个终端”。
2. 第二版将两个进程隐藏，但 start.ps1 完成后会返回，无法保证控制终端持续存在。
3. 用户要求直接按 Enter 不自动停止，同时唯一终端又要负责最终停止两个隐藏进程。

### 原因

Start-Process 默认窗口策略、脚本生命周期和交互控制是三个独立问题：

- WindowStyle Normal 会增加可见窗口；
- WindowStyle Hidden 可隐藏子进程，但父脚本若结束，控制界面不再承担生命周期管理；
- Read-Host 若直接绑定停止动作，会让 Enter 误触发停止。

### 最终解决办法

1. start.ps1 是唯一控制终端。
2. hddsyncd.exe 和 hddfs.exe 使用 Start-Process -WindowStyle Hidden。
3. 两个后台程序 stdout/stderr 分别重定向到 release/logs。
4. hddctl remote ls 和必要的 login 在当前唯一终端执行。
5. 等待盘符出现后启动 explorer.exe 打开挂载盘。
6. 控制终端显示精确停止命令：

       Get-Process hddfs,hddsyncd -ErrorAction SilentlyContinue | Stop-Process -Force

7. 直接按 Enter 只重复显示停止命令，不执行停止。
8. 只有输入完整停止命令时，控制脚本才执行 Stop-Process。
9. 停止后台进程后终端仍保持；输入 EXIT 才结束控制脚本。

### 验证

- 使用 PowerShell AST Parser 静态检查 start.ps1，语法通过；
- 检查到两个后台启动点均为 WindowStyle Hidden；
- 检查到 stdout/stderr 重定向；
- 检查到空输入分支先 continue，不会执行 Stop-Process；
- git diff --check 通过；
- Codex 未实际登录、启动 daemon、挂载盘符或访问真实云盘。

## 7. Codex 修改的主要文件

- internal/mount/winfsp/ipcfs.go
- internal/mount/winfsp/ipcfs_test.go
- scripts/Copy-ToHddMount.ps1
- release/README.md
- release/VERSION
- release/CHANGELOG.md
- release/SHA256SUMS.txt
- release/bin/README.md
- release/config/*
- release/docs/*
- release/scripts/*
- release/logs/.gitkeep
- release/data/.gitkeep

说明：ipcfs.go、ipcfs_test.go 和仓库其他文件在本会话开始前已有大量未提交修改。本报告只描述本会话中明确完成的增量，不表示这些文件的全部内容均由 Codex 编写。

## 8. 未解决事项与建议

1. Explorer Replace 仍应保持不支持，直到取得 disposition/options 或可靠回调证据。
2. start.ps1 目前只做过静态检查；应由用户在 release EXE 就位后执行完整手工测试。
3. 正式发布前应为 bin 下三个 EXE 生成 SHA256SUMS.txt。
4. 应确认关闭控制器窗口时的清理策略；当前强制关闭窗口不会自动执行停止命令。
5. 正式发行前应从干净工作树制作包，避免仓库现有数千个缓存/临时状态项进入发布流程。
6. 不应把 release/logs、release/data 中的运行数据提交或对外分发。

## 9. 合规声明

本会话中 Codex：

- 未访问真实云盘；
- 未执行登录；
- 未构建或覆盖 EXE；
- 未运行 go install；
- 未提交 Git；
- 未改写 Git 历史；
- 未清理用户现有未提交文件；
- 未将 token、cookie、metadata.db 或真实日志写入 release。

# 配置文件

当前版本主要通过命令行参数配置，本目录提供的配置文件为后续版本预留。

### config.example.json

推荐配置示例，包含以下可配置项说明：

- `provider`：云盘后端（`huadian` 或 `mock`）
- `pipe`：Named Pipe 路径（默认 `\\.\pipe\huadian-drive`）
- `mount`：挂载盘符（如 `S:`）
- `mode`：运行模式（如 `mkdir-rename-move-file-rename-move-remove-copy-upload-only`）
- `dataDir`：数据目录路径
- `debugLog`：hddfs debug 日志路径

程序当前不保证直接读取此文件。配置文件仅供 start.ps1 或后续配置加载功能参考。

**安全提示**：不要在此文件或任何配置文件中保存 token、cookie、密码、授权头或真实账号信息。认证凭据由 `hddctl login` 独立管理。

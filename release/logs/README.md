# 日志目录

运行日志输daemon 和 hddfs 的标准输出/错误及 debug 日志均写入此目录。

### 日志文件

| 文件 | 来源 | 内容 |
|---|---|---|
| `daemon.stdout.log` | hddsyncd 标准输出 | daemon 启动信息、就绪状态、关闭提示 |
| `daemon.stderr.log` | hddsyncd 标准错误 | 结构化操作日志：`fs_rename`、`fs_remove`、`fs_upload_staged` 等，含 writePolicy、path、providerMethod、postcheck 和 result |
| `hddfs.stdout.log` | hddfs 标准输出 | 挂载信息和程序输出 |
| `hddfs.stderr.log` | hddfs 标准错误 | FUSE 错误和异常信息 |
| `hddfs.debug.log` | hddfs debug 日志 | 逐回调跟踪：Getattr、Access、Open、Create、Release、Unlink 等，含 mode、flags、mask、errno 和耗时 |

### 使用建议

- 发行包中日志目录为空，首次运行后自动生成。
- 故障排查优先级：`daemon.stderr.log` → `hddfs.debug.log` → 其他日志。
- 提供日志给他人排查前必须脱敏，删除 token、cookie、授权头、密码和账号信息。
- 长期运行建议定期清理或轮转日志文件。

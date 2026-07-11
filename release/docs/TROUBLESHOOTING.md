# 故障排查

## PowerShell 禁止脚本

       Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass

该设置仅对当前进程生效。不要运行不可信脚本。

## WinFsp、盘符和 daemon

确认 WinFsp 已安装并重启终端。检查盘符是否被本地磁盘、网络盘或旧挂载占用。先启动 daemon，确认没有立即退出；daemon 与 hddfs 的 --pipe 必须逐字一致。

## 访问被拒绝或同名冲突

同名冲突、覆盖和在线编辑被拒绝属于安全策略。若新路径也失败，检查 daemon stderr、权限、认证和远端父目录。

## token 失效

重新执行项目支持的认证流程。不要把 token 写入 issue、聊天记录或发行包。

## 残留进程

       Get-Process hddfs,hddsyncd -ErrorAction SilentlyContinue | Stop-Process -Force

## 日志

优先查看 logs/daemon.stderr.log 和 logs/hddfs.debug.log，再查看 stdout。对外提供日志前删除 token、cookie、账号、签名 URL 和私人路径。

# 用户指南

## 启动 daemon 和挂载

确认 WinFsp 和 EXE 已准备好。进入 release 目录，按主 README 先启动 hddsyncd，再用完全相同的 named pipe 启动 hddfs。daemon 应保持运行；挂载盘符必须空闲。

## 浏览和下载

在资源管理器打开挂载盘。双击或复制远端文件到本地会下载内容，速度受网络影响。

## 文件和目录上传

只向不存在的新路径复制文件。上传先写本地 staging，关闭后提交。多选复制等价于多次单文件上传。目录复制会递归创建目录并逐文件上传，结束后应抽查数量和哈希。

## 冲突

同名路径默认拒绝。选择跳过/取消，或使用 Copy-ToHddMount.ps1 -OnConflict AutoRename。不要使用 Explorer Replace、Copy-Item -Force 或 Overwrite。

## 重命名、移动和删除

支持文件/目录重命名和移动，也支持删除文件、空目录和非空目录。这些是真实远端修改，不承诺回收站。

## 停止

关闭访问挂载盘的程序和窗口，然后停止 hddfs 与 hddsyncd。残留进程可使用主 README 的 Get-Process 命令处理。

# 文档目录

本目录包含用户操作和故障排查所需的全部文档。

| 文档 | 内容 |
|---|---|
| `QUICKSTART.md` | 快速开始指南：从安装 WinFsp 到成功挂载的完整步骤，含常见模式示例 |
| `USER_GUIDE.md` | 用户操作指南：浏览目录、新建目录、重命名、移动、删除、上传及冲突处理 |
| `FEATURE_LIST.md` | 功能矩阵：按模式列出已实现和未实现的能力，区分 Mock 和真实云盘验证状态 |
| `KNOWN_LIMITATIONS.md` | 已知限制：不支持在线编辑、不支持覆盖上传（原因说明）、无 GUI 等 |
| `TEST_REPORT.md` | 测试报告：Mock WinFsp E2E 和真实云盘隔离目录测试结果汇总 |
| `TROUBLESHOOTING.md` | 排障指南：常见错误码、日志位置、pipe/盘符占用、认证过期等诊断方法 |

故障排查时建议按以下顺序检查：`logs/` 目录中的 daemon stderr → hddfs debug log → 本文档中的 `TROUBLESHOOTING.md`。

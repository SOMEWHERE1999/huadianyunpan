# 华电云盘 Windows 客户端开发报告（Codex2 部分）

## 1. 报告范围

本文只记录本线程中由 Codex 直接分析、修改和验证的工作，不代表项目全部开发历史，也不归属其他编程体、用户或既有提交完成的功能。

本线程未提交 Git，未访问真实云盘，未执行真实登录或上传，也未读取或修改真实 `metadata.db`。真实云盘测试结果均由用户提供，本文将其作为外部验证证据。

时间采用 Asia/Shanghai（UTC+8）。由于本线程没有 Git 提交点，开始和结束时间根据会话、命令输出及用户提供的真实测试日志整理，部分时间为近似值。

| 阶段 | 开始时间 | 结束时间 | 结果 |
|---|---:|---:|---|
| 文件和目录 copy/move 冲突策略 | 2026-07-04 约 18:45 | 2026-07-04 约 19:11 | 完成，定向测试通过；随后用户确认真实云盘测试通过 |
| 首次上传协议修复与 upload-dir 收敛 | 2026-07-04 约 19:11 | 2026-07-04 约 19:39 | 自动测试通过，但真实云盘仍失败 |
| 基于 HAR 和真实错误的上传纠正 | 2026-07-04 19:39 | 2026-07-04 约 20:18 | 完成代码与严格测试；仍需真实云盘人工复验 |

## 2. 阶段一：copy/move 冲突策略

### 目标

在现有 `hddctl remote copy` 和 `hddctl remote move` 基础上，实现文件与目录按类型区分的冲突策略，并严格遵循已经由 HAR 确认的接口语义：

- 文件 copy：`fail`、`auto-rename`、`overwrite`；
- 文件 move：`fail`、`auto-rename`、`overwrite`；
- 目录 copy：仅 `auto-rename`，且作为默认策略；
- 目录 move：`fail`、`merge`；
- 不扩展未经 HAR 证明的目录 overwrite 或 auto-rename 语义。

### 遇到的问题

1. `TransferConflictPolicy` 只有 `fail` 和 `auto-rename`。
2. CLI 的 move 只接受 `fail`。
3. Provider 在请求前自行判断同名冲突，导致 overwrite/merge 无法直接交给服务端处理。
4. 文件和目录共用不完整的 `ondup` 映射，没有按对象类型限制策略。
5. auto-rename 缺少响应 `name` 时，旧实现通过“新增名称”猜测最终对象，没有优先使用响应 DocID。
6. overwrite、merge 的后置验证和目标目录前缀缓存失效不完整。

### 原因

原实现基于早期、较窄的协议假设，只覆盖了 fail 和部分 auto-rename。后续 HAR 已证明文件与目录接口的 `ondup` 含义不同，不能继续通过统一字符串映射处理。

此外，服务端 overwrite 响应可能只有目标 DocID，目录 merge 返回的也通常是目标已有目录 DocID，因此仅依赖源 basename 或客户端生成后缀会得到错误的最终路径和对象身份。

### 最终解决办法

1. 扩展传输冲突策略：`fail`、`auto-rename`、`overwrite`、`merge`。
2. Provider 在获得源对象类型后执行类型专属校验：
   - 文件 copy/move：`ondup=1/2/3`；
   - 目录 copy：只允许 `ondup=2`；
   - 目录 move：fail 使用 `ondup=1`，merge 使用 `ondup=3`。
3. 移除会阻断 overwrite/merge 的客户端同名短路，最终冲突判定交给服务端。
4. 写操作后强制 fresh List，并按响应 DocID 定位最终对象；响应包含 `name` 时校验该名称与 DocID 对应对象一致。
5. overwrite 验证源文件仍存在、目标同名文件数量为 1、类型和大小一致。
6. move 验证旧源对象消失；目录 merge 只验证根目录状态和响应 DocID，不宣称递归内容语义已验证。
7. 完善源路径、源父目录、目标父目录、最终路径和 merge 目标前缀的缓存失效。
8. 保留根目录、同父移动、移动/复制到自身或后代、跨文档库等安全限制。

### 验证与结论

Codex 使用 `httptest.Server` 和 fake Provider 增加了请求映射、响应解析、大小写、中文和空格路径、后置验证失败不重试等测试，并执行了定向 test、vet、build 和 `git diff --check`。

用户随后报告文件 copy/move 三种策略、目录 copy 自动改名、目录 move fail/merge 均已在真实云盘测试通过。因此后续上传修复没有修改 copy/move 实现和帮助语义。

## 3. 阶段二：首次上传协议修复与 upload-dir 收敛

### 目标

修复所有非秒传文件在对象存储 POST 阶段返回 403 的问题，同时将 upload-dir 简化为根目录同名即失败、只允许 fail。

### 遇到的问题

1. 非秒传流程在 `osbeginupload` 成功后，对 storage URL 直接发送原始文件字节。
2. `authrequest[2:]` 被当作 HTTP Header 使用。
3. 外层 HTTP `Content-Type` 被设置为 `application/octet-stream`。
4. `UploadFile` 和 upload-dir 子文件使用 `os.ReadFile`，会整体读入内存。
5. upload-dir 仍支持 auto-rename 和 merge，不符合“根冲突立即失败”的最终要求。

### 原因

HAR 表明对象存储请求采用 `multipart/form-data`，`AWSAccessKeyId`、`Content-Type`、`Policy`、`Signature` 和 `key` 是普通表单字段，文件字段为 `file`。旧实现错误地把签名字段当作请求头，导致对象存储无法验证表单签名。

### 首次解决办法

1. 新增有序 authrequest 字段解析，仅按首个冒号分割，保持 `+`、`/`、`=` 和尾部内容不变。
2. 使用 multipart preamble、文件流和 epilogue 组成 `io.MultiReader`，避免把文件正文放入内存。
3. 精确计算并设置 Content-Length，避免 chunked。
4. 使用独立 storage `http.Client`，不携带控制面 Cookie、Token、Authorization 或 CSRF Header。
5. storage 只有返回 204 才调用 `osendupload`；失败不重试、不 finalize。
6. upload-dir 固定使用根目录和子目录 `ondup=1`，子文件固定 fail；其他策略在网络写操作前拒绝。
7. 增加 authrequest、multipart、零字节文件、失败不 finalize、敏感信息不泄漏和 upload-dir 部分失败测试。

### 首次修复为什么仍不完整

自动测试证明了 multipart 在 Go 标准解析器下结构有效，但没有完全复刻 HAR 的原始请求形态：

1. dupload fake server 只把 JSON 解码到结构体，然后无条件成功，没有检查 CRC32 的真实类型和格式。
2. storage 测试主要检查字段可解析、值一致和无凭据泄漏，没有严格要求 HAR 中的浏览器/WAF请求头。
3. file part 的 Content-Type 被固定为 `application/octet-stream`，测试也按这个推测编写；成功 HAR 中实际根据文件类型发送，例如 JPG 为 `image/jpeg`。
4. multipart boundary、原始 Content-Disposition、字段重复和请求头集合没有做到足够严格的 HAR 对照。

用户于 2026-07-04 19:39 和 19:40 提供真实结果：upload-dir 秒传分支在 `/file/dupload` 返回 400，单文件非秒传在 storage POST 返回 403。这证明首次自动测试通过不能等价于真实上传修复。

## 4. 阶段三：基于 HAR 和真实错误的上传纠正

### 目标

分别修复两个独立故障：

- `predupload match=true` 后 dupload HTTP 400；
- `predupload match=false` 后 storage POST HTTP 403。

同时保持 `osendupload` 调用条件、upload-dir 根冲突即停止和上传错误安全分类。

### HAR 对照结果

分析 `D:\ncepupan-har\upload.har`、`upload2.har` 和 `upload3.har` 后确认：

1. dupload 的 `crc32` 是 8 位小写十六进制字符串，例如 `34466b33`，而不是十进制字符串。
2. dupload 的字段为 `client_mtime`、`crc32`、`docid`、`length`、`md5`、`name`、`ondup`、`csflevel`；时间为微秒，length 为 JSON 数字。
3. 成功 storage 请求使用 multipart/form-data，字段顺序为 `AWSAccessKeyId`、`Content-Type`、`Policy`、`Signature`、`key`、`file`。
4. file part 的 MIME 来自文件类型；HAR 中 JPG 为 `image/jpeg`。
5. 成功 storage 请求包含 Accept、Accept-Language、Origin、Referer、User-Agent、Sec-Fetch 和 sec-ch 等浏览器/WAF请求头，但不包含控制面 Token 或 Cookie。
6. storage 成功状态为 HTTP 204，之后才调用 `osendupload`。

### dupload 400 的原因与解决

当前代码使用：

```go
fmt.Sprintf("%d", crcHash.Sum32())
```

这会生成十进制 CRC32 字符串，与 HAR 的 8 位小写十六进制格式不一致。

修正为：

```go
fmt.Sprintf("%08x", crcHash.Sum32())
```

同时新增专用 dupload 请求函数：

- 保持 JSON Content-Type；
- 使用目标父目录 DocID和 basename；
- MD5 为完整文件的 32 位小写十六进制；
- length 为 int64 JSON 数字；
- client_mtime 使用微秒；
- ondup 和 csflevel 保持已确认语义；
- HTTP 400 安全解析有限长度 JSON code；
- 不返回 message、完整 DocID或原始响应正文；
- 写请求失败不重试。

严格测试使用 `hello world` golden 值：

- MD5：`5eb63bbbe01eeed093cb22bb8f5acdc3`；
- CRC32：`0d4a1185`；
- 服务端逐字比较原始 JSON，任一字段不同即返回 400。

旧十进制 CRC32 实现无法通过该测试。

### storage 403 的原因与解决

首次 multipart 实现仍与成功 HAR 有两项关键差异：

1. file part Content-Type 固定为 `application/octet-stream`；
2. 独立 storage 请求删除了 HAR 中用于通过站点 WAF的非认证浏览器请求头。

纠正后：

1. 普通签名表单字段 `Content-Type` 继续逐字使用 authrequest 的 `application/octet-stream`。
2. file part Content-Type 根据本地扩展名确定，例如 `.txt` 为 `text/plain`、`.jpg` 为 `image/jpeg`，未知类型回退为 `application/octet-stream`。
3. multipart boundary 使用 WebKit 风格随机边界；字段顺序、CRLF 和 closing boundary 由 `multipart.Writer` 生成。
4. Policy、Signature、key 和 URL 均不 decode、不重编码、不清理或重排。
5. 显式发送 HAR 证明存在的非认证浏览器/WAF Header。
6. storage client 仍与控制面隔离，不继承 Cookie、Token、Authorization 或 CSRF Header。
7. 保持精确 Content-Length，不使用 chunked。
8. 禁止自动重定向；即使 Go 因请求体不可重放而直接返回 3xx 响应，也明确映射为 `storage_redirect_not_allowed`。
9. 非 204 响应最多读取 64 KiB，安全解析 XML 或 JSON code，不输出 Message 或原始正文。

### upload-dir 根冲突保证

upload-dir 在递归之前必须成功执行根 `/dir/create`，且固定 `ondup=1`。服务端返回 `403002039` 时直接映射 `already_exists`，不包装为 `partial_directory_upload`，也不会调用 predupload、dupload、osbeginupload、storage、osendupload或创建子目录。

只有新根目录创建成功后才开始排序遍历本地子项。内部任一步失败立即停止并返回带安全相对路径的 `partial_directory_upload`，不进入已有根目录，也不自动回滚已成功内容。

### 自动测试

新增或加强：

- dupload 原始 JSON golden 测试；
- CRC32 8 位小写十六进制测试；
- dupload 400 安全 code 和不重试测试；
- storage 原始 path/query、WebKit multipart、字段顺序、字段数量和重复字段测试；
- authrequest 特殊字符逐字保持测试；
- file part filename、MIME和文件字节测试；
- Content-Length 与实际正文长度一致、无 chunked 测试；
- 控制面 Cookie、Token、Authorization、CSRF不泄漏测试；
- storage 403 XML/JSON、空正文、非 XML、500和超长正文测试；
- storage 失败不调用 osendupload测试；
- 307/308 类重定向不跟随测试；
- upload-dir 非 fail 策略在网络前拒绝测试；
- 根冲突后无任何子请求测试；
- 递归目录和子文件固定 fail 测试；
- 部分失败立即停止、再次执行根冲突测试；
- CLI help 不再宣传 upload-dir 多种冲突策略测试。

## 5. 验证记录

本阶段没有运行 `go test ./...`，没有执行真实登录或上传。

实际完成：

- `gofmt`：通过；
- `go test -count=1 ./internal/cloud/huadian/...`：通过（0.946s）；
- `go test -count=1 ./cmd/hddctl/...`：通过（0.778s）；
- `go build -o .\hddctl.new.exe .\cmd\hddctl`：通过；
- `.\hddctl.new.exe --help`：通过，无凭据或网络访问；
- `.\hddctl.new.exe remote --help`：通过，upload-dir help 为 fail-only；
- `git diff --check`：通过。

`go vet` 在本机环境中受到大量遗留 `go/compile/vet` 子进程和缓存争用影响，完整命令未在本阶段取得可靠的最终退出结果。为完成测试，曾在用户批准后终止残留 Go 工具链进程。该环境问题不应被描述为 vet 已通过。

## 6. 本线程涉及的主要文件

- `internal/cloud/cloud.go`
- `internal/cloud/huadian/provider.go`
- `internal/cloud/huadian/provider_test.go`
- `cmd/hddctl/main.go`
- `cmd/hddctl/remote.go`
- `cmd/hddctl/remote_test.go`
- `docs/buildlog-codex2.md`

工作树原本已有其他编程体和用户的修改；本线程没有清理、回退或提交这些内容。

## 7. 当前限制和人工验证建议

自动测试通过不等于真实云盘上传已经修复。当前仍需由用户在隔离测试目录中人工验证：

1. 上传一个已知可能命中秒传的小文件，确认 dupload 成功且不调用 storage/osendupload。
2. 上传一个全新随机小文件，确认 storage 返回 204、随后 osendupload 成功，并用 `remote stat` 核对大小。
3. 对远端已存在同名根目录执行 upload-dir，确认立即返回 `already_exists`，且已有目录内容不被进入或修改。

若真实 storage 仍返回 403，应安全记录响应中的白名单错误 code，并重新抓取该次 CLI 请求与成功网页请求的原始 Header 和 multipart part 头进行逐项比较；不得继续仅根据标准 multipart 可解析性推断协议完全一致。

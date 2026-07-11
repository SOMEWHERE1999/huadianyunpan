# 华电云盘 AnyShare API 完整参考文档

> 基于 `internal/cloud/huadian/provider.go` 对 `https://pan.ncepu.edu.cn` 的真实 HTTP API 分析。

---

## 一、总览

### 通信特征

- **协议**：HTTPS
- **HTTP 方法**：全部为 `POST`（非 RESTful），只有对象存储上/下载是 `POST`/`GET` 到签名 URL
- **基址**：`https://pan.ncepu.edu.cn`
- **路径前缀**：`/api/efast/v1/`
- **内容类型**：`Content-Type: application/json`
- **最大响应**：2 MiB（`maxJSONResp = 2 << 20`），超过返回 `ErrResponseTooLarge`

### 对象定位

所有 API 通过 **DocID** 定位对象，不是路径字符串：

```
resolvePath("/test/source/file.txt")
  → rootDocID("/") 出发
  → 逐层 POST /api/efast/v1/dir/list → 匹配名称 → 缓存 DocID
  → 返回 "file.txt" 的 DocID (格式: gns://<库名>/<对象ID>)
```

`docidCache`（内存 map[string]string）缓存已解析的 路径→DocID 映射，永不过期，
仅在写入操作后失效受影响的子树。`dirAndName()` 把 `/a/b/c.txt` 拆为父目录 `/a/b` 和名称 `c.txt`。

---

## 二、认证机制

### 三种认证方式（同时生效）

| 方式 | Header | 来源 |
|---|---|---|
| Session Cookie | `Cookie`（cookiejar 自动注入） | CDP 登录从浏览器 Network.getCookies 捕获 |
| CSRF Token | `X-CSRF-TOKEN` | Cookie 中的 `_csrf` 字段值 |
| Bearer Token | `Authorization: Bearer <jwt>` | CDP 登录拦截 Network.requestWillBeSent 中 Authorization header |

### 认证全链路时序

```
┌─ hddctl login ───────────────────────────────────────────────────┐
│                                                                   │
│ [1] CDP 启动 Chrome/Edge                                         │
│     → 导航到 CAS 统一认证页面 (netID + 密码 + 验证码)            │
│     → 认证成功后重定向到 https://pan.ncepu.edu.cn                 │
│                                                                   │
│ [2] CDP Network.getCookies                                       │
│     → 捕获 domain=.ncepu.edu.cn 的全部 cookies                   │
│     → 存入 StoredCookie 持久化                                    │
│                                                                   │
│ [3] CDP Network.requestWillBeSent 事件                            │
│     → 拦截任意 API 请求的 Authorization: Bearer <token> header   │
│     → 存入 Session.AccessToken                                    │
│                                                                   │
│ [4] CDP Network.responseReceived 事件                             │
│     → 拦截 POST /api/efast/v1/dir/list 响应体                    │
│     → 提取 dirs[0].docid 作为 RootDocID                           │
│     → 存入 Session.RootDocID                                      │
│                                                                   │
│ [5] SessionManager.SaveSession(sess)                              │
│     → JSON 写入 %APPDATA%\hdd\session.json                        │
└───────────────────────────────────────────────────────────────────┘

┌─ hddsyncd run ───────────────────────────────────────────────────┐
│                                                                   │
│ [1] NewFileCredentialStore → LoadSession()                       │
│     → 从 session.json 加载完整 Session{Cookies, Token, RootDocID} │
│                                                                   │
│ [2] huadian.New(token) → SetCookies → SetCSRFToken → Connect()   │
│     → cookiejar 初始化（不实际请求）                              │
│                                                                   │
│ [3] Health Check                                                  │
│     → Stat("/") + List("/") 验证会话有效性                        │
│     → 401 → 提示用户重新 login                                    │
└───────────────────────────────────────────────────────────────────┘
```

### Console 模式（降级方案）

```
hddctl --console login
  → 提示用户粘贴 Bearer token
  → Session{AccessToken: token}（没有 Cookies，没有 RootDocID）
  → CheckSession() 做 Bearer token 验证
  → 缺 RootDocID → daemon 挂载前会报 "root docid not set"
```

### Token 脱敏

日志输出时 `RedactToken(token)` 只显示前 6 位 + `...`，防止泄露。

---

## 三、浏览器伪装（WAF 绕过）

所有 API 请求头自动添加以下字段，模拟 Microsoft Edge 浏览器：

```
User-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36
            (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36 Edg/149.0.0.0
sec-ch-ua: "Microsoft Edge";v="149", "Chromium";v="149", "Not)A;Brand";v="24"
sec-ch-ua-mobile: ?0
sec-ch-ua-platform: "Windows"
Date: <当前UTC时间 RFC1123>
```

**缺失 User-Agent 或 sec-ch-ua 中任何一个，WAF 直接拒绝请求。** 此外还有：

```
Accept: application/json, text/plain, */*
Accept-Language: zh-CN,zh;q=0.9
X-Language: zh-CN
Referer: https://pan.ncepu.edu.cn/anyshare/
Origin: https://pan.ncepu.edu.cn
X-Requested-With: XMLHttpRequest
Sec-Fetch-Site: same-origin
Sec-Fetch-Mode: cors
Sec-Fetch-Dest: empty
```

---

## 四、API 端点详细清单

> 所有请求均为 `POST`，`Content-Type: application/json`。对象存储端点例外。

### 1. 列目录 — `/api/efast/v1/dir/list`

**请求**：
```json
{"docid": "gns://library/abc123", "count": 100}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| docid | string | 目录的 DocID |
| count | int | 每页数量（固定 100） |
| sort | string | 可选排序字段（未使用） |
| by | string | 可选排序方向（未使用） |

**响应**：
```json
{
  "dirs": [
    {"docid": "gns://lib/def456", "name": "subdir", "modified": 1719500000000000, "isdir": true}
  ],
  "files": [
    {
      "docid": "gns://lib/ghi789",
      "name": "readme.txt",
      "size": 1024,
      "rev": "v3",
      "modified": 1719500000000000,
      "editor": "张三",
      "isdir": false
    }
  ]
}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| docid | string | 对象 DocID |
| name | string | 文件名（不含路径） |
| size | int64 | 文件大小（字节），目录为 0 |
| rev | string | 版本/ETag |
| modified | int64 | 修改时间（**微秒** Unix 时间戳） |
| editor | string | 最后编辑者 |
| isdir | bool | 是否为目录 |

**解析**：用 `parentPathJoin(parentDir, e.Name)` 构造完整远端路径，缓存到 `entryCache`。
**分页**：`Count: 100` 固定，无 offset/continuation token——当前只取首页。

---

### 2. 查属性 — `/api/efast/v1/file/metadata`

**请求**：
```json
{"docid": "gns://library/abc123"}
```

**响应**：
```json
{
  "docid": "gns://library/abc123",
  "name": "readme.txt",
  "size": 2048,
  "rev": "v5",
  "modified": 1719500000000000,
  "isdir": false
}
```

**根路径处理**：`Stat("/")` 不调 API，直接返回 `FileInfo{Path: "/", IsDir: true}`。
**缓存策略**：优先查 `entryCache`，未命中再走 `resolvePath → StatByDocID`。

---

### 3. 创建目录 — `/api/efast/v1/dir/create`

**请求**：
```json
{"docid": "gns://parent/abc123", "name": "newdir", "ondup": 1}
```

**响应**：
```json
{"docid": "gns://library/xyz789", "id": "xyz789", "name": "newdir"}
```

- `ondup` 固定为 `1`（fail），不支持 auto-rename 或 overwrite
- 优先使用 `docid` 字段，为空时降级使用 `id` 字段
- 两者都为空 → `ErrMalformedResponse`
- 失效父目录的 entryCache + docidCache

---

### 4. 删除 — `/api/efast/v1/file/delete`

**请求**：
```json
{"docid": "gns://library/abc123"}
```

无返回体。删除后失效目标路径 + 子树 + 父目录缓存。

---

### 5. 重命名 — `/api/efast/v1/file/rename`

**请求**：
```json
{"docid": "gns://library/abc123", "name": "newname.txt"}
```

无返回体。`Rename(ctx, oldPath, newPath)` 调用此 API，传入旧路径解析出的 DocID 和新路径的 basename。
**仅同父目录**——不能改 parent docid。
失效旧路径子树 + 新路径父目录。

---

### 6. 文件复制 — `/api/efast/v1/file/copy`

**请求**：
```json
{"docid": "gns://lib/src123", "destparent": "gns://lib/dst456", "ondup": 2}
```

**响应**：
```json
{"docid": "gns://lib/new789", "name": "readme.txt"}
```

支持的 ondup: `1`(fail), `2`(auto-rename), `3`(overwrite)

---

### 7. 文件移动 — `/api/efast/v1/file/move`

请求/响应格式同上。额外限制：`merge`（ondup=3）不支持文件夹移动。同父目录移动返回 `ErrInvalidMoveSameParent`（应改用 rename）。

---

### 8. 目录复制 — `/api/efast/v1/dir/copy`

请求格式同上。**唯一策略**：`ondup=2`（auto-rename），其他策略报 `ErrUnsupportedDirCopyFail`。

---

### 9. 目录移动 — `/api/efast/v1/dir/move`

请求格式同上。支持策略：`ondup=1`(fail)、`ondup=3`(merge)。auto-rename 和 overwrite 不支持。

---

### 10. 文件上传（三步流程）

#### Step 1: 预检 — `/api/efast/v1/file/predupload`

**请求**：
```json
{"slice_md5": "d41d8cd98f00b204e9800998ecf8427e", "length": 2048}
```

`slice_md5` 是**全文内容的 MD5 十六进制编码**（不是分片），用于服务端秒传去重。

**响应**：
```json
{"match": true}
```

`match=true` → 走直传（dupload），`match=false` → 走对象存储上传。

#### Step 2a: 直传（秒传）— `/api/efast/v1/file/dupload`

**请求**：
```json
{
  "docid": "gns://parent/abc",
  "name": "upload.txt",
  "length": 2048,
  "md5": "d41d8cd98f00b204e9800998ecf8427e",
  "crc32": "00000000",
  "client_mtime": 1719500000000000,
  "ondup": 1,
  "csflevel": 0
}
```

**响应**：
```json
{"docid": "gns://lib/new999", "name": "upload.txt", "rev": "v1", "success": true}
```

#### Step 2b: 对象存储开始 — `/api/efast/v1/file/osbeginupload`

**请求**：
```json
{
  "usehttps": true,
  "reqmethod": "POST",
  "name": "upload.txt",
  "docid": "gns://parent/abc",
  "ondup": 1,
  "length": 2048,
  "client_mtime": 1719500000000000
}
```

**响应**：
```json
{
  "authrequest": ["POST", "https://storage.example.com/bucket/key", "Authorization: AWS...", "x-amz-date: ...", "Content-Type: ..."],
  "docid": "gns://lib/tmp999",
  "rev": "v1",
  "name": "upload.txt"
}
```

`authrequest` 是 JSON 数组：`["METHOD", "URL", "Header: Value", ...]`，遵循 AWS S3 签名协议。

#### Step 3: 对象存储上传 — 签名 URL POST

用 Step 2b 的签名 URL 发送 `multipart/form-data`：

```
POST https://storage.example.com/bucket/key
Content-Type: multipart/form-data; boundary=----WebKitFormBoundary<random12>

------WebKitFormBoundary<random12>
Content-Disposition: form-data; name="AWSAccessKeyId"

AKIA...
------WebKitFormBoundary<random12>
Content-Disposition: form-data; name="Policy"

<base64 policy>
------WebKitFormBoundary<random12>
Content-Disposition: form-data; name="Signature"

<base64 signature>
------WebKitFormBoundary<random12>
Content-Disposition: form-data; name="key"

<object key>
------WebKitFormBoundary<random12>
Content-Disposition: form-data; name="file"; filename="upload.txt"
Content-Type: application/octet-stream

<file binary content>
------WebKitFormBoundary<random12>--
```

**响应**：`204 No Content`。**禁止 HTTP 重定向**（checkRedirect 返回 `ErrStorageRedirect`）。

#### Step 4: 上传结束 — `/api/efast/v1/file/osendupload`

**请求**：
```json
{"docid": "gns://lib/tmp999", "rev": "v1", "csflevel": 0}
```

无返回体。此步骤通知服务端文件已完整上传到对象存储。

#### 上传后校验

`verifyUploadedFile()` 再次 List 父目录：
1. 确认**恰好一个**目标名称存在（大小写不敏感）
2. 确认 Size 匹配
3. 确认 Rev（ETag）匹配
4. overwrite 策略下确认精确单条（`countSameName(updated, name, false) == 1`）

---

### 11. 建议名 — `/api/efast/v1/file/getsuggestname`

**请求**：
```json
{"docid": "gns://parent/abc", "name": "conflict.txt"}
```

**响应**：
```json
{"name": "conflict (1).txt"}
```

auto-rename 策略上传时，先调用此 API 获取服务端生成的唯一名，再用 `ondup=1`（非覆盖）上传到新名。

---

### 12. 文件下载 — `/api/efast/v1/file/osdownload`

**请求**：
```json
{"docid": "gns://library/abc123"}
```

**响应**：
```json
{
  "authrequest": ["GET", "https://storage.example.com/bucket/key", "Authorization: AWS...", "x-amz-date: ..."],
  "name": "readme.txt",
  "size": 2048,
  "rev": "v5",
  "modified": 1719500000000000,
  "client_mtime": 1719500000000000,
  "editor": "张三",
  "siteid": "1",
  "need_watermark": false
}
```

`authrequest` 支持三种格式：
1. **数组格式**：`["GET", "url", "Header: Value", ...]`
2. **对象格式**：`{"method": "GET", "url": "...", "headers": {"x-amz-date": "..."}}`
3. **对象数组格式**：`[{"method": "GET", ...}]`（取第一个元素）

然后用 `GET` + Auth headers 访问签名 URL → `io.Copy` 流式写入本地。
**禁止重定向**，禁止覆盖 `Date` header（AWS 签名依赖）。

---

## 五、冲突策略（ondup）

`ondup` 是整数参数，贯穿所有写入操作。

| ondup 值 | 含义 | 使用场景 |
|---|---|---|
| `1` | **fail** — 同名已存在则返回 `403002039`(ErrAlreadyExists) | 默认值。文件上传、目录创建、Mkdir、文件 copy/move 默认、目录 move 默认 |
| `2` | **auto-rename** — 自动生成无冲突的新名称 | 文件 copy/move、目录 copy（唯一策略） |
| `3` | **overwrite** — 覆盖已有文件（或目录 merge） | 文件上传 overwrite、文件 copy/move overwrite、目录 move merge |

### 按操作类型的策略矩阵

| 操作 | 允许的策略 | ondup |
|---|---|---|
| **文件上传** | fail, auto-rename, overwrite | fail=1, auto-rename=先调 suggestName 再 ondup=1, overwrite=3（直传和 osbeginupload 均设 3）|
| **目录上传** | **fail 仅** | 1（不支持 auto-rename、overwrite、merge） |
| **文件 copy** | fail, auto-rename, overwrite | 1 / 2 / 3 |
| **文件 move** | fail, auto-rename, overwrite | 1 / 2 / 3（**merge 不支持**） |
| **目录 copy** | **auto-rename 仅** | 2（传 fail 报 `ErrUnsupportedDirCopyFail`） |
| **目录 move** | fail, merge | 1 / 3（auto-rename 和 overwrite 不支持） |
| **Mkdir** | fail 仅 | 1 |
| **Rename** | 无 ondup 参数 | — |

### `validateTransferPolicy()` 严格校验

```go
func validateTransferPolicy(move bool, isDir bool, policy) error {
    if isDir && !move && policy != auto_rename → ErrUnsupportedDirCopyFail
    if isDir && move && policy != fail && policy != merge → ErrUnsupportedDirMove
    if !isDir && move && policy == merge → ErrUnsupportedFileMove
    if !isDir && !move && policy not in {fail, auto_rename, overwrite} → ErrUnsupportedConflict
    ...
}
```

---

## 六、Copy/Move 全流程

```
transfer(ctx, move, sourcePath, destinationDirectory, policy)
  │
  ├── [前置检查]
  │   源不能是 "/"
  │   Stat 源 → 获取源 DocID + 大小 + 类型
  │   Stat 目的目录 → 必须存在且为目录
  │
  ├── [防穿越]
  │   检查 destinationDirectory 是否为 sourcePath 的前缀 → ErrInvalidMoveDescendant
  │
  ├── [同父检查]
  │   remoteParent(sourcePath) == destinationDirectory && move → ErrInvalidMoveSameParent
  │
  ├── [跨库检查]
  │   docLibrary(srcDocID) != docLibrary(dstDocID) → ErrCrossLibrary
  │   docLibrary("gns://LIB/abc123") = "LIB"
  │
  ├── [冲突探测]
  │   List 目标目录 → 大小写不敏感查找同名条目
  │   已有条目存在 + policy=fail → 返回冲突给调用方
  │
  ├── [endpoint 选择]
  │   文件 copy: /api/efast/v1/file/copy
  │   文件 move: /api/efast/v1/file/move
  │   目录 copy: /api/efast/v1/dir/copy
  │   目录 move: /api/efast/v1/dir/move
  │
  ├── [策略校验]
  │   validateTransferPolicy(move, isDir, policy)
  │
  ├── [API 调用]
  │   POST {docid: srcDocID, destparent: dstDocID, ondup}
  │   → transferResponse{docid, name, isdirexist}
  │
  └── [后置校验]
       List 目标目录 → 按响应 DocID 找条目
       验证：文件大小匹配、IsDir 匹配
       overwrite/merge：确认单条同名条目存在
       move：确认源目录不再有 sourcePath 条目
       copy：确认源目录仍保留 sourcePath 条目
```

---

## 七、错误码分类

### HTTP 级错误

| HTTP 状态 | 特殊 code | Go 错误 | 含义 |
|---|---|---|---|
| 401 | — | `ErrUnauthorized` | Token 过期 |
| 403 | `403002039` | `ErrAlreadyExists` | 同名文件夹或文件已存在 |
| 403 | 其他 | `ErrForbidden` | 权限不足 |
| 404 | — | `ErrNotFound` | 对象不存在 |
| 网络错误 | — | `fmt.Errorf`(wrapped) | 连接失败 |

### 本地校验错误

| Go 错误 | 触发条件 |
|---|---|
| `ErrInvalidMoveDescendant` | 目标目录在源路径子树内 |
| `ErrInvalidMoveSameParent` | 同父目录移动（应用 Rename 而非 Move） |
| `ErrCrossLibrary` | 源和目标不在同一文档库 |
| `ErrUnsupportedConflict` | 不支持该操作 + 策略组合 |
| `ErrUnsupportedDirCopyFail` | 目录 copy 不能用 fail |
| `ErrUnsupportedDirMove` | 目录 move 用了 auto-rename 或 overwrite |
| `ErrUnsupportedFileMove` | 文件 move 用了 merge |
| `ErrMalformedResponse` | API 返回的 JSON 缺少必要字段 |
| `ErrVerification` | 后置校验失败（size 不匹配、条目不存在等） |
| `ErrStorageUpload` | 对象存储上传失败 |
| `ErrStorageRedirect` | 对象存储 URL 触发了 HTTP 重定向 |
| `ErrUploadFinalize` | osendupload 调用失败 |
| `ErrResponseTooLarge` | API 响应超过 2 MiB |

### 快速检索表

| 场景 | 错误变量 | HTTP 状态 | 用户可见 |
|---|---|---|---|
| Token 过期 | `ErrUnauthorized` | 401 | "session expired — run hddctl login" |
| 目标已存在 | `ErrAlreadyExists` | 403 | "target already exists" |
| 权限不足 | `ErrForbidden` | 403 | "forbidden" |
| 路径不存在 | `ErrNotFound` | 404 | "not found" |
| 移入子树 | `ErrInvalidMoveDescendant` | 本地 | "cannot move into descendant" |
| 同父 move | `ErrInvalidMoveSameParent` | 本地 | "use remote rename instead" |
| 跨文档库 | `ErrCrossLibrary` | 本地 | "cross doc library unsupported" |
| 上传后校验 | `ErrVerification` | 本地 | "remote verification failed" |

---

## 八、DocID 格式与生命周期

### 格式

```
gns://<文档库名>/<对象UUID>
```

示例：`gns://zhangsan/abc123-def456-789`

`docLibrary("gns://zhangsan/abc123")` = `"zhangsan"`

### 生命周期

| 阶段 | 来源 |
|---|---|
| 首次登录 | CDP 拦截 POST `/dir/list(/)` 响应 → `dirs[0].docid` 作为 RootDocID |
| 后续使用 | LoadSession → `session.RootDocID` 缓存于内存 |
| 路径解析 | `resolvePath("/test/file.txt")` 逐层 dir/list 缓存中间 DocID |
| 有效期 | 与 Token 绑定；Token 过期后 DocID 仍有效（它是云盘对象的唯一标识） |

### docidCache

内存 `map[string]string`，**永不过期**。仅在以下写入操作后失效受影响的子树：

| 操作 | 失效范围 |
|---|---|
| Mkdir | 父目录 entry + docid |
| Remove | 目标路径及其子树 + 父目录 |
| Rename | 旧路径及其子树 + 新路径父目录 |
| Copy | 目标目录 + 冲突的已有条目 |
| Move | 源路径及其子树 + 目标目录 + 冲突的已有条目 |
| Upload | 父目录 |
| List | 只写入缓存，不失效 |

---

## 九、缓存策略总结

Provider 维护两个缓存层：

| 缓存 | 类型 | Key | Value | 用途 |
|---|---|---|---|---|
| `docidCache` | `map[string]string` | 规范化的远端路径 | DocID | API 调用时用 DocID 定位对象 |
| `entryCache` | `map[string]domain.FileInfo` | 规范化的远端路径 | 文件元数据 | List/Stat 时直接返回，避免重复 API |

**失效策略**：写入操作（Upload/Mkdir/Rename/Remove/Copy/Move）失效被影响的子树，读取操作（List/Stat）只写缓存不失效。这意味着如果一个外部客户端（或网页端）修改了远端的文件，daemon 缓存的元数据可能已过时。当前实现依赖 `hddfs` 的 `dirCacheTTL = 30s` 来定期拉取，但没有主动失效远端变更的机制。

---

## 十、安全设计

| 策略 | 位置 | 说明 |
|---|---|---|
| 全文 MD5 去重 | predupload | 上传前必调，防止重复上传相同内容的文件 |
| 禁止 HTTP 重定向 | uploadToStorage, downloadFromStorage | 对象存储上传/下载阶段禁止 302 |
| 2 MiB 响应限制 | post() | 恶意大响应会被截断并返回 `ErrResponseTooLarge` |
| 路径规范 | cleanRemote() | 强制 `/` 前缀 + 去首尾空白 |
| 跨库保护 | transfer() | `gns://` 前缀不同拒绝跨库操作 |
| 后置校验 | verifyUploadedFile(), transfer() | 每个写操作后重查远端确认实际状态 |
| 浏览器伪装 | setBrowserHeaders() | 缺失 User-Agent 或 sec-ch-ua 或被 WAF 拦截 |
| Token 脱敏 | RedactToken() | 日志仅输出 token 前 6 位 |
| 路径脱敏 | RedactPath() | SHA256 哈希后仅输出前 6 字节 |

---

## 十一、端点速查表

| 操作 | 端点 | 主要字段 | ondup 支持 |
|---|---|---|---|
| 列目录 | `POST /dir/list` | docid, count=100 | — |
| 查属性 | `POST /file/metadata` | docid | — |
| 创建目录 | `POST /dir/create` | docid, name | 1 |
| 删除 | `POST /file/delete` | docid | — |
| 重命名 | `POST /file/rename` | docid, name | — |
| 上传预检 | `POST /file/predupload` | slice_md5, length | — |
| 直传（秒传） | `POST /file/dupload` | docid, name, md5, crc32, ondup | 1,3 |
| 上传开始 | `POST /file/osbeginupload` | docid, name, ondup, length | 1,3 |
| 上传存储 | `POST <签名URL>` | multipart/form-data | — |
| 上传结束 | `POST /file/osendupload` | docid, rev | — |
| 建议名 | `POST /file/getsuggestname` | docid, name | — |
| 下载 | `POST /file/osdownload` | docid | — |
| 存储下载 | `GET <签名URL>` | Auth headers | — |
| 文件复制 | `POST /file/copy` | docid, destparent, ondup | 1,2,3 |
| 文件移动 | `POST /file/move` | docid, destparent, ondup | 1,2,3 |
| 目录复制 | `POST /dir/copy` | docid, destparent, ondup | 2 |
| 目录移动 | `POST /dir/move` | docid, destparent, ondup | 1,3 |

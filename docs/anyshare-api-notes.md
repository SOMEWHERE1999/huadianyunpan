# AnyShare API 调研文档（第一版）

> 项目：华电云盘客户端（Windows 优先）
>
> 产品：AnyShare 私有化部署
>
> 服务地址：https://pan.ncepu.edu.cn

------------------------------------------------------------------------

# 1. 文档目的

本文档根据浏览器 HAR 抓包结果整理，用于指导 `AnyShareProvider` 的实现。

> **安全说明**
>
> -   不保存账号、密码、短信验证码。
> -   不记录 Token、Cookie、Authorization。
> -   临时签名 URL (`authrequest`) 不写入日志。

------------------------------------------------------------------------

# 2. 系统架构

``` text
浏览器
    │
    ▼
AnyShare Web
    │
    ├── /api/efast
    ├── /api/eacp
    ├── /api/open-doc
    └── Object Storage(S3/Ceph)
```

------------------------------------------------------------------------

# 3. 对象模型

  字段           说明
  -------------- -------------------
  docid          文件/目录唯一标识
  rev            文件版本号
  editor         最后修改者
  modified       服务端修改时间
  client_mtime   客户端修改时间
  storage_name   存储后端
  doc_lib_type   文库类型

------------------------------------------------------------------------

# 4. 已确认接口

## 4.1 列目录

POST `/api/efast/v1/dir/list`

作用：获取目录内容。

------------------------------------------------------------------------

## 4.2 文件元数据

POST `/api/efast/v1/file/metadata`

典型响应字段：

-   docid
-   name
-   size
-   rev
-   editor
-   modified
-   storage_name

对应：

``` go
Stat(docID string)
```

------------------------------------------------------------------------

## 4.3 上传流程

### predupload

POST `/api/efast/v1/file/predupload`

Request：

``` json
{
  "slice_md5":"<md5>",
  "length":920941
}
```

Response：

``` json
{
  "match": true
}
```

说明：

上传前进行内容检查。

------------------------------------------------------------------------

### osbeginupload

POST `/api/efast/v1/file/osbeginupload`

返回：

-   authrequest
-   docid
-   rev
-   name

authrequest 中包含：

-   上传方法
-   临时上传 URL
-   Policy
-   Signature
-   key

------------------------------------------------------------------------

### 对象存储上传

按照 authrequest 指示上传二进制文件。

Content-Type：

application/octet-stream

------------------------------------------------------------------------

### osendupload

POST `/api/efast/v1/file/osendupload`

Response：

``` json
{
  "editor":"用户",
  "modified":1782564435765157
}
```

表示上传正式完成。

------------------------------------------------------------------------

## 上传时序

``` text
计算MD5
    │
predupload
    │
osbeginupload
    │
上传对象存储
    │
osendupload
    │
完成
```

------------------------------------------------------------------------

# 4.4 下载流程

POST `/api/efast/v1/file/osdownload`

返回：

``` json
{
  "authrequest":[
    "GET",
    "临时下载URL"
  ]
}
```

客户端随后访问临时 URL 获取文件。

------------------------------------------------------------------------

# 4.5 创建目录

POST `/api/efast/v1/dir/create`

参数：

-   docid
-   name
-   ondup

------------------------------------------------------------------------

# 4.6 重命名

POST `/api/efast/v1/file/rename`

参数：

-   docid
-   name
-   ondup

------------------------------------------------------------------------

# 4.7 删除

POST `/api/efast/v1/file/delete`

参数：

-   docid

------------------------------------------------------------------------

# 5. Provider 映射

  Provider   API
  ---------- -----------------------------------------------------------
  List       dir/list
  Stat       file/metadata
  Upload     predupload → osbeginupload → Object Storage → osendupload
  Download   osdownload → 临时URL
  Mkdir      dir/create
  Rename     file/rename
  Remove     file/delete

------------------------------------------------------------------------

# 6. 已确认事实

-   AnyShare 使用对象存储（storage_name=eceph_s3）。
-   上传采用两阶段授权。
-   下载采用临时授权 URL。
-   文件唯一标识为 docid，而非路径。
-   rev 用于版本控制。

------------------------------------------------------------------------

# 7. 待确认内容

-   登录接口完整请求参数
-   Token 刷新机制
-   WebDAV 是否开放
-   秒传(match=true)完整业务逻辑
-   错误码定义
-   大文件分片上传

------------------------------------------------------------------------

# 8. Codex 开发约束

1.  不猜测未确认接口。
2.  未确认接口返回 ErrNotImplemented。
3.  不记录 Token/Cookie。
4.  保持 TLS 校验开启。
5.  所有 HTTP 请求使用 context。
6.  上传下载均设置超时。
7.  cloud.Provider 保持稳定，不泄露 AnyShare 细节。

# provider-design.md

# AnyShareProvider 设计文档

## 1. 模块结构

``` text
internal/cloud/anyshare/
├── provider.go
├── client.go
├── auth.go
├── files.go
├── upload.go
├── download.go
├── dto.go
└── errors.go
```

## 2. Provider 接口

``` go
type Provider interface {
    List(ctx context.Context, path string) error
    Stat(ctx context.Context, docID string) error
    Upload(ctx context.Context, parentID string, name string, r io.Reader, size int64) error
    Download(ctx context.Context, docID string, w io.Writer) error
    Mkdir(ctx context.Context, parentID,name string) error
    Rename(ctx context.Context, docID,name string) error
    Remove(ctx context.Context, docID string) error
}
```

## 3. 上传流程

``` text
MD5
 ↓
predupload
 ↓
osbeginupload
 ↓
POST authrequest URL
 ↓
osendupload
```

## 4. 下载流程

``` text
osdownload
 ↓
authrequest
 ↓
GET 临时URL
```

## 5. DTO

建议：

-   MetadataResponse
-   OSBeginUploadResponse
-   OSDownloadResponse
-   DirListResponse

## 6. 与同步引擎关系

``` text
SyncEngine
    ↓
Provider
    ↓
HTTP Client
    ↓
AnyShare
```

## 7. Codex 开发规范

1.  不猜测接口。
2.  未确认接口返回 ErrNotImplemented。
3.  所有请求支持 context。
4.  设置超时。
5.  不输出敏感认证信息。
6.  保持 cloud.Provider 抽象稳定。

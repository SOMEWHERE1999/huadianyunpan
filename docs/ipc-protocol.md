# IPC Protocol

## Overview

hddfs communicates with hddsyncd via Windows Named Pipes. The pipe path is:

```
\\.\pipe\huadian-drive
```

## Wire Format

Each message is prefixed with a 4-byte big-endian length, followed by JSON.

```
[4 bytes: length][JSON body]
```

## Request / Response

Every request has `type` and `id` fields. The server responds with the same `type` and `id`.

### Request

```json
{"type":"<command>","id":"<unique-id>","data":{...}}
```

### Response

```json
{"type":"<command>","id":"<unique-id>","data":{...}}
```

## Commands

### fs.list

List directory contents.

- **Timeout**: 5 seconds
- **Request**:
  ```json
  {"type":"fs.list","id":"1","data":{"path":"/"}}
  ```
- **Response**:
  ```json
  {"type":"fs.list","id":"1","data":{
    "entries":[
      {"path":"/hello.txt","size":17,"is_dir":false,"mod_time":"2026-01-01T00:00:00Z"},
      {"path":"/sub","size":0,"is_dir":true,"mod_time":"2026-01-01T00:00:00Z"}
    ]
  }}
  ```

### fs.stat

Get file/directory attributes.

- **Timeout**: 5 seconds
- **Request**:
  ```json
  {"type":"fs.stat","id":"1","data":{"path":"/hello.txt"}}
  ```
- **Response**:
  ```json
  {"type":"fs.stat","id":"1","data":{"entry":{"path":"/hello.txt","size":17,"is_dir":false,"mod_time":"2026-01-01T00:00:00Z"}}}
  ```

### fs.open

Prepare a file for reading by caching it locally.

- **Timeout**: 60 seconds
- **Request**:
  ```json
  {"type":"fs.open","id":"1","data":{"path":"/hello.txt"}}
  ```
- **Response** (success):
  ```json
  {"type":"fs.open","id":"1","data":{"cache_path":"C:\\...\\cache\\hello.txt","size":17}}
  ```
- **Response** (error):
  ```json
  {"type":"fs.open","id":"1","error":"not found"}
  ```

### fs.close

Notify the daemon that a file handle is closed (fire-and-forget).

- **Request**:
  ```json
  {"type":"fs.close","id":"1","data":{"path":"/hello.txt"}}
  ```
- **Response**:
  ```json
  {"type":"fs.close","id":"1"}
  ```

## Data Flow for File Reads

```
hddfs                          hddsyncd
  |                                |
  |--- fs.open(path="/hello") ---->|
  |                                | Provider.Download → cache/hello
  |<-- {cache_path, size} ---------|
  |                                |
  |  os.Open(cache_path)           |
  |  ReadAt from local file        |
  |                                |
  |--- fs.close(path="/hello") --->| (fire-and-forget)
```

## Error Codes

| Error | Meaning |
|---|---|
| `"not found"` | File or directory does not exist |
| `"unknown command"` | Unrecognized type |
| `"is a directory"` | Attempt to open a directory as a file |

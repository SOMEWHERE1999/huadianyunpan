//go:build windows && cgo

// Package winfsp provides WinFsp-based userspace filesystems.
//
// ipcfs.go implements a read-write FUSE filesystem that communicates
// with hddsyncd via Windows Named Pipe IPC. Write operations use a
// write-back cache: writes go to local cache files; flush/release
// notifies the daemon to enqueue an upload task.
package winfsp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"ncepupan/hdd/internal/ipc"
	"ncepupan/hdd/internal/platform/windows/npipe"

	"github.com/winfsp/cgofuse/fuse"
)

// ---------------------------------------------------------------------------
// IPCClient
// ---------------------------------------------------------------------------

type IPCClient struct {
	pipePath string
	mu       sync.Mutex
	conn     *npipe.ClientConn
	nextID   uint64
}

func NewIPCClient(pipePath string) *IPCClient {
	return &IPCClient{pipePath: pipePath}
}

func (c *IPCClient) call(req ipc.Request) (ipc.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		conn, err := npipe.Dial(c.pipePath, 5*time.Second)
		if err != nil {
			return ipc.Response{}, fmt.Errorf("ipc dial: %w", err)
		}
		c.conn = conn
	}
	resp, err := c.conn.Call(req)
	if err != nil {
		c.conn.Close()
		c.conn = nil
	}
	return resp, err
}

func (c *IPCClient) close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

func (c *IPCClient) nextIDStr() string {
	n := atomic.AddUint64(&c.nextID, 1)
	return fmt.Sprintf("%d", n)
}

// ---------------------------------------------------------------------------
// ipcFileHandle
// ---------------------------------------------------------------------------

type ipcFileHandle struct {
	fh        uint64
	path      string
	localPath string
	size      int64
	dirty     bool
	ready     chan struct{}
	dlErr     error
	mu        sync.Mutex

	// Staged upload fields (used only in copy-upload mode).
	staged         bool
	committed      bool
	conflictPolicy string // "fail", "overwrite", "auto_rename"
}

// ---------------------------------------------------------------------------
// IPCFileSystem
// ---------------------------------------------------------------------------

type cachedDir struct {
	entries  []ipc.FSEntry
	cachedAt time.Time
}

type IPCFileSystem struct {
	fuse.FileSystemBase

	client                                            *IPCClient
	cacheDir                                          string
	ctx                                               context.Context
	nextFH                                            uint64
	readOnly                                          bool
	mkdirOnly                                         bool
	mkdirRenameOnly                                   bool
	mkdirRenameMoveOnly                               bool
	mkdirRenameMoveFileRenameOnly                     bool
	mkdirRenameMoveFileRenameMoveOnly                 bool
	mkdirRenameMoveFileRenameMoveRemoveOnly           bool
	mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly bool
	debugLog                                          *os.File
	instID                                            uint64
	pipePath                                          string
	logPath                                           string
	callFn                                            func(ipc.Request) (ipc.Response, error)

	mu     sync.RWMutex
	files  map[string]*ipcFileHandle
	dirs   map[string]*cachedDir
	dirsMu sync.RWMutex

	pathLocks   map[string]*sync.Mutex
	pathLocksMu sync.Mutex
}

const dirCacheTTL = 30 * time.Second

// IPCFileSystemConfig holds startup configuration embedded in the
// filesystem object so that WinFsp child processes receive it.
type IPCFileSystemConfig struct {
	PipePath                                          string
	ReadOnly                                          bool
	MkdirOnly                                         bool
	MkdirRenameOnly                                   bool
	MkdirRenameMoveOnly                               bool
	MkdirRenameMoveFileRenameOnly                     bool
	MkdirRenameMoveFileRenameMoveOnly                 bool
	MkdirRenameMoveFileRenameMoveRemoveOnly           bool
	MkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly bool
	LogPath                                           string
}

func NewIPCFileSystem(cfg IPCFileSystemConfig) *IPCFileSystem {
	fs := &IPCFileSystem{
		client:                                  NewIPCClient(cfg.PipePath),
		cacheDir:                                "",
		ctx:                                     context.Background(),
		files:                                   make(map[string]*ipcFileHandle),
		dirs:                                    make(map[string]*cachedDir),
		pathLocks:                               make(map[string]*sync.Mutex),
		readOnly:                                cfg.ReadOnly,
		mkdirOnly:                               cfg.MkdirOnly,
		mkdirRenameOnly:                         cfg.MkdirRenameOnly,
		mkdirRenameMoveOnly:                     cfg.MkdirRenameMoveOnly,
		mkdirRenameMoveFileRenameOnly:           cfg.MkdirRenameMoveFileRenameOnly,
		mkdirRenameMoveFileRenameMoveOnly:       cfg.MkdirRenameMoveFileRenameMoveOnly,
		mkdirRenameMoveFileRenameMoveRemoveOnly: cfg.MkdirRenameMoveFileRenameMoveRemoveOnly,
		mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly: cfg.MkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly,
		pipePath: cfg.PipePath,
		logPath:  cfg.LogPath,
	}
	if cfg.LogPath != "" {
		f, err := os.Create(cfg.LogPath)
		if err == nil {
			fs.debugLog = f
			fs.instID = atomic.AddUint64(&ipcFSInstCounter, 1)
			fmt.Fprintf(f, "%s PID=%d instance=%d mkdirOnly=%v readOnly=%v pipe=%s\n",
				time.Now().Format("15:04:05.000"), os.Getpid(), fs.instID,
				fs.mkdirOnly, fs.readOnly, fs.pipePath)
		}
	}
	// Resolve daemon cache dir lazily.
	if dir, err := fs.queryCacheDir(); err == nil {
		fs.cacheDir = dir
	}
	return fs
}

func (fs *IPCFileSystem) queryCacheDir() (string, error) {
	req := ipc.Request{Type: "fs.cacheDir", ID: fs.client.nextIDStr()}
	resp, err := fs.ipcCall(req)
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("fs.cacheDir: %s", resp.Error)
	}
	var result struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(resp.Data, &result); err != nil {
		return "", err
	}
	return result.Path, nil
}

var ipcFSInstCounter uint64

// SetDebugLog enables per-callback diagnostic logging to the given file.
func (fs *IPCFileSystem) SetDebugLog(path string) {
	if path == "" || fs.debugLog != nil {
		return // already set via constructor
	}
	if fs.logPath != "" {
		return
	}
	fs.logPath = path
	f, err := os.Create(path)
	if err != nil {
		return
	}
	fs.debugLog = f
	fs.instID = atomic.AddUint64(&ipcFSInstCounter, 1)
	fmt.Fprintf(f, "%s PID=%d instance=%d mkdirOnly=%v readOnly=%v pipe=%s\n",
		time.Now().Format("15:04:05.000"), os.Getpid(), fs.instID,
		fs.mkdirOnly, fs.readOnly, fs.pipePath)
}

func (fs *IPCFileSystem) trace(format string, args ...any) {
	if fs.debugLog == nil {
		return
	}
	line := time.Now().Format("15:04:05.000") + " "
	line += fmt.Sprintf("PID=%d ", os.Getpid())
	if fs.instID > 0 {
		line += fmt.Sprintf("fs=%d ", fs.instID)
	}
	line += fmt.Sprintf(format, args...)
	if !strings.HasSuffix(line, "\n") {
		line += "\n"
	}
	fs.debugLog.WriteString(line)
	fs.debugLog.Sync() // ensure writes are visible even on crash
}

// LogMountConfiguration records the exact arguments passed to host.Mount.
func (fs *IPCFileSystem) LogMountConfiguration(mountPoint string, options []string) {
	fs.trace("Mount configuration mountPoint=%s options=%q readOnly=%v mkdirOnly=%v",
		mountPoint, options, fs.readOnly, fs.mkdirOnly)
}

// RequiresDeleteAccessCheck marks this filesystem as implementing the Windows
// DELETE_OK access contract used by FileSystemHost.SetCapDeleteAccess.
func (fs *IPCFileSystem) RequiresDeleteAccessCheck() {}

func (fs *IPCFileSystem) closeDebugLog() {
	if fs.debugLog != nil {
		fs.debugLog.Close()
		fs.debugLog = nil
	}
}

// SetReadOnly enables or disables write rejection at the FUSE level.
func (fs *IPCFileSystem) SetReadOnly(ro bool) { fs.readOnly = ro }

// SetMkdirOnly enables directory-creation-only mode at the FUSE level.
func (fs *IPCFileSystem) SetMkdirOnly(v bool) { fs.mkdirOnly = v }

// SetMkdirRenameOnly enables mkdir+rename-only mode.
func (fs *IPCFileSystem) SetMkdirRenameOnly(v bool) { fs.mkdirRenameOnly = v }

func (fs *IPCFileSystem) SetMkdirRenameMoveOnly(v bool) { fs.mkdirRenameMoveOnly = v }

func (fs *IPCFileSystem) SetMkdirRenameMoveFileRenameOnly(v bool) {
	fs.mkdirRenameMoveFileRenameOnly = v
}

func (fs *IPCFileSystem) SetMkdirRenameMoveFileRenameMoveOnly(v bool) {
	fs.mkdirRenameMoveFileRenameMoveOnly = v
}

func (fs *IPCFileSystem) SetMkdirRenameMoveFileRenameMoveRemoveOnly(v bool) {
	fs.mkdirRenameMoveFileRenameMoveRemoveOnly = v
}

func (fs *IPCFileSystem) SetMkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly(v bool) {
	fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly = v
}

// SetContext sets the context for all FUSE callbacks.
func (fs *IPCFileSystem) SetContext(ctx context.Context) { fs.ctx = ctx }

func (fs *IPCFileSystem) Close() {
	req := ipc.Request{Type: "shutdown", ID: fs.client.nextIDStr()}
	fs.ipcCall(req)
	fs.client.close()
	fs.closeDebugLog()
}

func (fs *IPCFileSystem) pathLock(path string) func() {
	fs.pathLocksMu.Lock()
	mu, ok := fs.pathLocks[path]
	if !ok {
		mu = &sync.Mutex{}
		fs.pathLocks[path] = mu
	}
	fs.pathLocksMu.Unlock()
	mu.Lock()
	return mu.Unlock
}

// ---------------------------------------------------------------------------
// IPC helpers
// ---------------------------------------------------------------------------

func (fs *IPCFileSystem) ipcCall(req ipc.Request) (ipc.Response, error) {
	if fs.callFn != nil {
		return fs.callFn(req)
	}
	return fs.client.call(req)
}

func jsonPayload(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}

func (fs *IPCFileSystem) ipcList(ctx context.Context, path string) ([]ipc.FSEntry, error) {
	req := ipc.Request{Type: "fs.list", ID: fs.client.nextIDStr(), Data: jsonPayload(map[string]string{"path": path})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		// fs.list is idempotent. The connection opened during filesystem
		// construction can become stale while WinFsp finishes mounting; call
		// once more so IPCClient reconnects. Never apply this to write calls.
		fs.trace("ipcList retry path=%s firstErr=%v", path, err)
		resp, err = fs.ipcCall(req)
		if err != nil {
			return nil, err
		}
	}
	if resp.Error != "" {
		return nil, fmt.Errorf("fs.list: %s", resp.Error)
	}
	var data ipc.FSListData
	json.Unmarshal(resp.Data, &data)
	return data.Entries, nil
}

func (fs *IPCFileSystem) ipcStat(ctx context.Context, path string) (ipc.FSEntry, error) {
	req := ipc.Request{Type: "fs.stat", ID: fs.client.nextIDStr(), Data: jsonPayload(map[string]string{"path": path})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		// fs.stat is idempotent — retry once after reconnect, same as fs.list.
		fs.trace("ipcStat retry path=%s firstErr=%v", path, err)
		resp, err = fs.ipcCall(req)
		if err != nil {
			return ipc.FSEntry{}, err
		}
	}
	if resp.Error != "" {
		// Business error (not_found, permission_denied, etc.) — do not retry.
		return ipc.FSEntry{}, fmt.Errorf("fs.stat: %s", resp.Error)
	}
	var data ipc.FSStatData
	json.Unmarshal(resp.Data, &data)
	return data.Entry, nil
}

func (fs *IPCFileSystem) ipcOpen(ctx context.Context, path string) (string, int64, error) {
	req := ipc.Request{Type: "fs.open", ID: fs.client.nextIDStr(), Data: jsonPayload(map[string]string{"path": path, "flags": "read"})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		// fs.open for read is idempotent — retry once after reconnect.
		fs.trace("ipcOpen retry path=%s firstErr=%v", path, err)
		resp, err = fs.ipcCall(req)
		if err != nil {
			return "", 0, err
		}
	}
	if resp.Error != "" {
		return "", 0, fmt.Errorf("fs.open: %s", resp.Error)
	}
	var data ipc.FSOpenData
	json.Unmarshal(resp.Data, &data)
	return data.CachePath, data.Size, nil
}

func (fs *IPCFileSystem) ipcCreate(ctx context.Context, path string) (string, error) {
	req := ipc.Request{Type: "fs.create", ID: fs.client.nextIDStr(), Data: jsonPayload(map[string]string{"path": path})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		return "", err
	}
	if resp.Error != "" {
		return "", fmt.Errorf("fs.create: %s", resp.Error)
	}
	var data ipc.FSCreateData
	json.Unmarshal(resp.Data, &data)
	return data.CachePath, nil
}

func (fs *IPCFileSystem) ipcMkdir(ctx context.Context, path string) error {
	req := ipc.Request{Type: "fs.mkdir", ID: fs.client.nextIDStr(), Data: jsonPayload(map[string]string{"path": path})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("fs.mkdir: %s", resp.Error)
	}
	return nil
}

func (fs *IPCFileSystem) ipcRename(ctx context.Context, oldPath, newPath string) error {
	req := ipc.Request{Type: "fs.rename", ID: fs.client.nextIDStr(), Data: jsonPayload(ipc.FSRenameRequest{OldPath: oldPath, NewPath: newPath})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("fs.rename: %s", resp.Error)
	}
	return nil
}

func (fs *IPCFileSystem) ipcRemove(ctx context.Context, path string) error {
	req := ipc.Request{Type: "fs.remove", ID: fs.client.nextIDStr(), Data: jsonPayload(ipc.FSRemoveRequest{Path: path})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("fs.remove: %s", resp.Error)
	}
	return nil
}

func (fs *IPCFileSystem) ipcMarkDirty(ctx context.Context, path string) error {
	req := ipc.Request{Type: "fs.markDirty", ID: fs.client.nextIDStr(), Data: jsonPayload(map[string]string{"path": path})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("fs.markdirty: %s", resp.Error)
	}
	return nil
}

func (fs *IPCFileSystem) ipcCloseDirty(ctx context.Context, path string, dirty bool) error {
	type closePayload struct {
		Path  string `json:"path"`
		Dirty bool   `json:"dirty"`
	}
	req := ipc.Request{Type: "fs.close", ID: fs.client.nextIDStr(), Data: jsonPayload(closePayload{Path: path, Dirty: dirty})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		return err
	}
	if resp.Error != "" {
		return fmt.Errorf("fs.close: %s", resp.Error)
	}
	return nil
}

func (fs *IPCFileSystem) ipcUploadStaged(ctx context.Context, remotePath, stagingRel string, size int64, conflictPolicy string) (ipc.FSUploadStagedResponse, error) {
	req := ipc.Request{Type: "fs.uploadStaged", ID: fs.client.nextIDStr(), Data: jsonPayload(ipc.FSUploadStagedRequest{
		RemotePath:     remotePath,
		StagingPath:    stagingRel,
		Size:           size,
		ConflictPolicy: conflictPolicy,
	})}
	resp, err := fs.ipcCall(req)
	if err != nil {
		return ipc.FSUploadStagedResponse{}, err
	}
	if resp.Error != "" {
		return ipc.FSUploadStagedResponse{}, fmt.Errorf("fs.uploadStaged: %s", resp.Error)
	}
	var result ipc.FSUploadStagedResponse
	json.Unmarshal(resp.Data, &result)
	return result, nil
}

// ---------------------------------------------------------------------------
// FUSE: read-only
// ---------------------------------------------------------------------------

func (fs *IPCFileSystem) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	started := time.Now()
	path = cleanPath(path)
	ctx := fs.ctx
	rc := 0
	defer func() {
		fs.traceStatResult("Getattr", path, rc, stat, started)
	}()

	if path == "/" {
		dirStat(stat)
		fs.projectMode(stat)
		return 0
	}

	fs.mu.RLock()
	h, hasHandle := fs.files[path]
	fs.mu.RUnlock()

	if hasHandle {
		h.mu.Lock()
		stat.Mode = syscall.S_IFREG | 0644
		stat.Size = h.size
		stat.Nlink = 1
		now := fuse.Now()
		stat.Atim, stat.Mtim, stat.Ctim, stat.Birthtim = now, now, now, now
		h.mu.Unlock()
		fs.projectMode(stat)
		return 0
	}

	entry, err := fs.ipcStat(ctx, path)
	if err != nil {
		rc = ipcErrToFuse(err)
		return rc
	}
	fillStatFromEntry(entry, stat)
	fs.projectMode(stat)
	return 0
}

func (fs *IPCFileSystem) traceStatResult(operation, path string, rc int, stat *fuse.Stat_t, started time.Time) {
	if rc != 0 {
		fs.trace("%s result path=%s err=%d elapsed=%s readOnly=%v mkdirOnly=%v",
			operation, path, rc, time.Since(started), fs.readOnly, fs.mkdirOnly)
		return
	}
	typeBits := stat.Mode & fuse.S_IFMT
	fs.trace("%s result path=%s err=0 mode=0x%x octal=%#o type=0%o perm=0%o uid=%d gid=%d nlink=%d size=%d isDir=%v elapsed=%s readOnly=%v mkdirOnly=%v",
		operation, path, stat.Mode, stat.Mode, typeBits, stat.Mode&0777, stat.Uid, stat.Gid,
		stat.Nlink, stat.Size, typeBits == fuse.S_IFDIR, time.Since(started), fs.readOnly, fs.mkdirOnly)
}

// projectMode exposes virtual permissions to WinFsp. Callback-level checks and
// the daemon allowlist remain authoritative for every modifying operation.
func (fs *IPCFileSystem) isRestrictedMode() bool {
	return fs.readOnly ||
		fs.mkdirOnly ||
		fs.mkdirRenameOnly ||
		fs.mkdirRenameMoveOnly ||
		fs.mkdirRenameMoveFileRenameOnly ||
		fs.mkdirRenameMoveFileRenameMoveOnly ||
		fs.mkdirRenameMoveFileRenameMoveRemoveOnly ||
		fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly
}

func (fs *IPCFileSystem) projectMode(stat *fuse.Stat_t) {
	isDir := stat.Mode&fuse.S_IFMT == fuse.S_IFDIR
	if isDir {
		switch {
		case fs.readOnly:
			stat.Mode = fuse.S_IFDIR | 0555
		case fs.isRestrictedMode():
			stat.Mode = fuse.S_IFDIR | 0777
		default:
			stat.Mode = fuse.S_IFDIR | 0755
		}
		return
	}
	if fs.mkdirRenameMoveFileRenameMoveRemoveOnly || fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		stat.Mode = fuse.S_IFREG | 0666
	} else if fs.isRestrictedMode() {
		stat.Mode = fuse.S_IFREG | 0444
	} else {
		stat.Mode = fuse.S_IFREG | 0644
	}
}

func (fs *IPCFileSystem) Access(path string, mask uint32) int {
	started := time.Now()
	var stat fuse.Stat_t
	rc := fs.Getattr(path, &stat, ^uint64(0))
	if rc == 0 && mask&(fuse.W_OK|fuse.DELETE_OK) != 0 {
		isDir := stat.Mode&fuse.S_IFMT == fuse.S_IFDIR
		if mask&fuse.DELETE_OK != 0 && (fs.readOnly || fs.mkdirOnly) {
			rc = -fuse.EPERM
		} else if mask&fuse.DELETE_OK != 0 && (fs.mkdirRenameOnly || fs.mkdirRenameMoveOnly) && isDir {
		} else if mask&fuse.DELETE_OK != 0 && (fs.mkdirRenameMoveFileRenameOnly || fs.mkdirRenameMoveFileRenameMoveOnly || fs.mkdirRenameMoveFileRenameMoveRemoveOnly || fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly) {
			// Allow DELETE_OK for both dirs and files (rename/move/remove/copy-upload allowed by policy).
		} else if fs.readOnly || ((fs.mkdirOnly || fs.mkdirRenameOnly || fs.mkdirRenameMoveOnly || fs.mkdirRenameMoveFileRenameOnly || fs.mkdirRenameMoveFileRenameMoveOnly || fs.mkdirRenameMoveFileRenameMoveRemoveOnly) && !isDir) {
			rc = -fuse.EROFS
		}
	}
	fs.trace("Access result path=%s mask=%d maskHex=0x%x R_OK=%v W_OK=%v X_OK=%v DELETE_OK=%v mode=%#o err=%d elapsed=%s ro=%v md=%v mdr=%v mdrm=%v mdrfr=%v mdrfrm=%v mdrfrmr=%v mdrfrcu=%v",
		cleanPath(path), mask, mask, mask&fuse.R_OK != 0, mask&fuse.W_OK != 0,
		mask&fuse.X_OK != 0, mask&fuse.DELETE_OK != 0, stat.Mode, rc,
		time.Since(started), fs.readOnly, fs.mkdirOnly, fs.mkdirRenameOnly, fs.mkdirRenameMoveOnly, fs.mkdirRenameMoveFileRenameOnly, fs.mkdirRenameMoveFileRenameMoveOnly, fs.mkdirRenameMoveFileRenameMoveRemoveOnly, fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly)
	return rc
}

func (fs *IPCFileSystem) Opendir(path string) (int, uint64) {
	path = cleanPath(path)
	if path == "/" {
		return 0, 0
	}
	ctx := fs.ctx
	_, err := fs.ipcStat(ctx, path)
	return ipcErrToFuse(err), 0
}

func (fs *IPCFileSystem) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64, fh uint64) int {

	started := time.Now()
	path = cleanPath(path)
	ctx := fs.ctx
	rc := 0
	defer func() {
		fs.trace("Readdir result path=%s err=%d elapsed=%s", path, rc, time.Since(started))
	}()

	fs.dirsMu.RLock()
	cd, ok := fs.dirs[path]
	fs.dirsMu.RUnlock()
	if !ok || time.Since(cd.cachedAt) > dirCacheTTL {
		entries, err := fs.ipcList(ctx, path)
		if err != nil {
			rc = ipcErrToFuse(err)
			fs.trace("Readdir ipcList error path=%s err=%v errno=%d", path, err, rc)
			return rc
		}
		fs.dirsMu.Lock()
		fs.dirs[path] = &cachedDir{entries: entries, cachedAt: time.Now()}
		fs.dirsMu.Unlock()
		cd = fs.dirs[path]
	}
	var dotStat fuse.Stat_t
	dirStat(&dotStat)
	fs.projectMode(&dotStat)
	if !fill(".", &dotStat, 0) || !fill("..", &dotStat, 0) {
		return 0
	}

	for _, entry := range cd.entries {
		name := basename(entry.Path)
		var st fuse.Stat_t
		fillStatFromEntry(entry, &st)
		fs.projectMode(&st)
		if !fill(name, &st, 0) {
			break
		}
	}
	return 0
}

func (fs *IPCFileSystem) Releasedir(path string, fh uint64) int {
	return 0
}

func (fs *IPCFileSystem) Open(path string, flags int) (int, uint64) {
	path = cleanPath(path)
	ctx := fs.ctx

	if flags&(os.O_WRONLY|os.O_RDWR|os.O_TRUNC|os.O_APPEND) != 0 {
		explicitOverwrite := isExplicitOverwriteRequest("open", flags)
		fs.trace("Open path=%s flags=%d flagsHex=0x%x write=%v rdwr=%v trunc=%v append=%v explicitOverwrite=%v disposition=unavailable options=unavailable accessMask=unavailable new19=%v",
			path, flags, flags, flags&(os.O_WRONLY|os.O_RDWR) != 0, flags&os.O_RDWR != 0, flags&os.O_TRUNC != 0, flags&os.O_APPEND != 0, explicitOverwrite, fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly)
		if fs.isRestrictedMode() {
			if fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
				fs.trace("Open decision=staged path=%s explicitOverwrite=%v", path, explicitOverwrite)
				return fs.createStagedUpload(path, flags, false)
			}
			fs.trace("Open write-rejected path=%s flags=0x%x err=%d", path, flags, -fuse.EROFS)
			return -fuse.EROFS, ^uint64(0)
		}
		return fs.openWriteable(path, flags, ctx)
	}

	unlock := fs.pathLock(path)
	defer unlock()

	fs.mu.RLock()
	_, cached := fs.files[path]
	fs.mu.RUnlock()
	if cached {
		return 0, 0
	}

	fh := atomic.AddUint64(&fs.nextFH, 1)
	h := &ipcFileHandle{fh: fh, path: path, ready: make(chan struct{})}
	fs.mu.Lock()
	fs.files[path] = h
	fs.mu.Unlock()

	go func() {
		cachePath, size, err := fs.ipcOpen(ctx, path)
		if err != nil {
			h.dlErr = err
		} else {
			if fs.cacheDir != "" {
				if verr := validateCachePath(cachePath, fs.cacheDir); verr != nil {
					h.dlErr = verr
				}
			}
			if h.dlErr == nil {
				h.localPath = cachePath
				h.size = size
			}
		}
		close(h.ready)
	}()
	return 0, fh
}

func (fs *IPCFileSystem) Read(path string, buff []byte, ofst int64, fh uint64) int {
	path = cleanPath(path)

	fs.mu.RLock()
	h, ok := fs.files[path]
	fs.mu.RUnlock()
	if !ok {
		code, _ := fs.Open(path, os.O_RDONLY)
		if code != 0 {
			return code
		}
		fs.mu.RLock()
		h, ok = fs.files[path]
		fs.mu.RUnlock()
		if !ok {
			return -fuse.EIO
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.ready != nil {
		<-h.ready
		if h.dlErr != nil {
			return ipcErrToFuse(h.dlErr)
		}
		h.ready = nil
	}

	if ofst >= h.size {
		return 0
	}
	f, err := os.Open(h.localPath)
	if err != nil {
		return -fuse.EIO
	}
	defer f.Close()
	n, err := f.ReadAt(buff, ofst)
	if err != nil && err != io.EOF {
		return -fuse.EIO
	}
	return n
}

func (fs *IPCFileSystem) Release(path string, fh uint64) int {
	path = cleanPath(path)

	fs.mu.Lock()
	h, ok := fs.files[path]
	delete(fs.files, path)
	fs.mu.Unlock()

	if ok {
		// Staged upload: commit on release.
		if h.staged {
			h.mu.Lock()
			alreadyCommitted := h.committed
			h.committed = true
			localPath := h.localPath
			stagingSize := h.size
			conflictPolicy := h.conflictPolicy
			h.mu.Unlock()

			if !alreadyCommitted {
				info, err := os.Stat(localPath)
				if err != nil {
					os.Remove(localPath)
					fs.trace("staged_commit path=%s result=staging_not_found", path)
					return -fuse.EIO
				}
				if stagingSize == 0 {
					stagingSize = info.Size()
				}
				stagingRel, _ := filepath.Rel(filepath.Join(fs.cacheDir, "staging"), localPath)
				stagingSHA256 := fileSHA256(localPath)
				_, uploadErr := fs.ipcUploadStaged(fs.ctx, path, stagingRel, stagingSize, conflictPolicy)
				os.Remove(localPath)
				if uploadErr != nil {
					fs.trace("Release staged_commit path=%s bytes=%d sha256=%s conflictPolicy=%s providerMethod=Upload verifiedSuccess=false result=upload_error err=%v",
						path, stagingSize, stagingSHA256, conflictPolicy, uploadErr)
					return ipcErrToFuse(uploadErr)
				}
				fs.trace("Release staged_commit path=%s bytes=%d sha256=%s conflictPolicy=%s providerMethod=Upload verifiedSuccess=true result=success",
					path, stagingSize, stagingSHA256, conflictPolicy)
			}
			fs.flushDirCacheFor(path)
			return 0
		}

		// Normal (non-staged) handle release.
		h.mu.Lock()
		dirty := h.dirty
		h.mu.Unlock()
		if dirty {
			if err := fs.ipcCloseDirty(fs.ctx, path, dirty); err != nil {
				return ipcErrToFuse(err)
			}
		}
		fs.flushDirCacheFor(path)
	}
	return 0
}

// declaredCapacityBytes is the Huadian Drive declared capacity shown to Windows.
// This is a declared commitment (200 GiB), not a real-time quota value.
// TODO: replace with server-provided quota once a suitable API is confirmed.
const declaredCapacityBytes = 200 * 1024 * 1024 * 1024 // 200 GiB
const declaredBlockSize = 4096

func (fs *IPCFileSystem) Statfs(path string, stat *fuse.Statfs_t) int {
	started := time.Now()
	stat.Bsize = declaredBlockSize
	stat.Frsize = declaredBlockSize
	blocks := uint64(declaredCapacityBytes) / declaredBlockSize
	stat.Blocks = blocks
	stat.Bfree = blocks
	stat.Bavail = blocks
	stat.Files = 100000
	stat.Ffree = 99900
	fs.trace("Statfs result path=%s err=0 bsize=%d frsize=%d blocks=%d bfree=%d bavail=%d elapsed=%s",
		cleanPath(path), stat.Bsize, stat.Frsize, stat.Blocks, stat.Bfree, stat.Bavail, time.Since(started))
	return 0
}

// ---------------------------------------------------------------------------
// FUSE: write operations
// ---------------------------------------------------------------------------

func (fs *IPCFileSystem) openWriteable(path string, flags int, ctx context.Context) (int, uint64) {
	if err := rejectReserved(path); err != nil {
		return -fuse.EINVAL, 0
	}
	unlock := fs.pathLock(path)
	defer unlock()

	fs.mu.RLock()
	h, exists := fs.files[path]
	fs.mu.RUnlock()

	if !exists {
		cachePath, err := fs.ipcCreate(ctx, path)
		if err != nil {
			return ipcErrToFuse(err), 0
		}
		h = &ipcFileHandle{path: path, localPath: cachePath, size: 0, dirty: true}
		if flags&os.O_TRUNC != 0 {
			os.Truncate(cachePath, 0)
		}
	} else {
		h.mu.Lock()
		if flags&os.O_TRUNC != 0 {
			os.Truncate(h.localPath, 0)
			h.size = 0
		}
		h.dirty = true
		h.mu.Unlock()
	}

	fs.mu.Lock()
	fs.files[path] = h
	fs.mu.Unlock()
	return 0, 0
}

func (fs *IPCFileSystem) createStagedUpload(path string, flags int, isCreate bool) (int, uint64) {
	op := "open"
	if isCreate {
		op = "create"
	}
	explicitOverwrite := isExplicitOverwriteRequest(op, flags)
	fs.trace("createStagedUpload enter path=%s flags=0x%x flagsHex=0x%x isCreate=%v trunc=%v explicitOverwrite=%v",
		path, flags, flags, isCreate, flags&os.O_TRUNC != 0, explicitOverwrite)
	if err := rejectReserved(path); err != nil {
		fs.trace("createStagedUpload path=%s rejected=reserved err=%v", path, err)
		return -fuse.EINVAL, ^uint64(0)
	}

	unlock := fs.pathLock(path)
	defer unlock()

	// Check if destination already exists (for conflict handling).
	conflictPolicy := "fail"
	ctx := fs.ctx
	_, statErr := fs.ipcStat(ctx, path)
	exists := statErr == nil
	if exists {
		// The current cgofuse callback does not expose the Windows create
		// disposition/options. A Create callback by itself is therefore not
		// evidence of replace intent: Explorer also uses it while probing a
		// same-name copy. Fail closed unless the available flags explicitly
		// request truncation.
		if explicitOverwrite {
			conflictPolicy = "overwrite"
		} else {
			fs.trace("createStagedUpload path=%s exists=true isCreate=%v trunc=%v explicitOverwrite=false conflictPolicy=fail rejected=EEXIST reason=destination_exists_without_explicit_overwrite", path, isCreate, flags&os.O_TRUNC != 0)
			return -fuse.EEXIST, ^uint64(0)
		}
	}
	fs.trace("createStagedUpload path=%s exists=%v isCreate=%v trunc=%v explicitOverwrite=%v conflictPolicy=%s", path, exists, isCreate, flags&os.O_TRUNC != 0, explicitOverwrite, conflictPolicy)

	stagingDir := filepath.Join(fs.cacheDir, "staging")
	os.MkdirAll(stagingDir, 0700)
	stagingFile, err := os.CreateTemp(stagingDir, "upload-*.staging")
	if err != nil {
		return -fuse.EIO, ^uint64(0)
	}
	stagingPath := stagingFile.Name()
	stagingFile.Close()

	fh := atomic.AddUint64(&fs.nextFH, 1)
	h := &ipcFileHandle{
		fh:             fh,
		path:           path,
		localPath:      stagingPath,
		size:           0,
		staged:         true,
		conflictPolicy: conflictPolicy,
	}
	fs.mu.Lock()
	fs.files[path] = h
	fs.mu.Unlock()

	fs.trace("staged_create path=%s stagingPath=%s conflictPolicy=%s result=success", path, stagingPath, conflictPolicy)
	return 0, fh
}

// isExplicitOverwriteRequest reports whether the POSIX information exposed by
// cgofuse proves replace intent. cgofuse v1.6.0 does not expose the Windows
// create disposition/options. O_TRUNC is its only equivalent replace signal;
// O_EXCL is an explicit no-replace signal and always wins.
func isExplicitOverwriteRequest(op string, flags int) bool {
	return op == "open" && flags&fuse.O_TRUNC != 0 && flags&fuse.O_EXCL == 0
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return "unavailable"
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "unavailable"
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func (fs *IPCFileSystem) Create(path string, flags int, mode uint32) (int, uint64) {
	fs.trace("Create begin path=%s flags=%d flagsHex=0x%x mode=0x%x octal=%#o type=0%o isDir=%v disposition=unavailable options=unavailable accessMask=unavailable explicitOverwrite=%v ro=%v md=%v mdr=%v mdrm=%v",
		path, flags, flags, mode, mode, mode&fuse.S_IFMT, mode&fuse.S_IFMT == fuse.S_IFDIR, isExplicitOverwriteRequest("create", flags),
		fs.readOnly, fs.mkdirOnly, fs.mkdirRenameOnly, fs.mkdirRenameMoveOnly)
	if fs.readOnly || fs.mkdirOnly {
		fs.trace("Create result path=%s branch=blocked err=%d fh=%d", path, -fuse.EROFS, ^uint64(0))
		return -fuse.EROFS, ^uint64(0)
	}
	if fs.isRestrictedMode() {
		if mode&fuse.S_IFMT == fuse.S_IFDIR {
			rc := fs.Mkdir(path, mode)
			fs.trace("Create result path=%s branch=restricted->Mkdir err=%d", path, rc)
			if rc != 0 {
				return rc, ^uint64(0)
			}
			return 0, 0
		}
		// new18 copy-upload mode: create staged upload handle for file copy.
		if fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
			fs.trace("Create new18 staged path=%s flags=0x%x trunc=%v create=%v", path, flags, flags&os.O_TRUNC != 0, flags&os.O_CREATE != 0)
			return fs.createStagedUpload(path, flags, true)
		}
		fs.trace("Create result path=%s branch=blocked-restricted err=%d", path, -fuse.EROFS)
		return -fuse.EROFS, ^uint64(0)
	}
	path = cleanPath(path)
	ctx := fs.ctx
	return fs.openWriteable(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, ctx)
}

func (fs *IPCFileSystem) Write(path string, buff []byte, ofst int64, fh uint64) int {
	if fs.isRestrictedMode() {
		// Allow write only on staged upload handles in copy-upload mode.
		if !fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
			return -fuse.EROFS
		}
		path = cleanPath(path)
		fs.mu.RLock()
		h, ok := fs.files[path]
		fs.mu.RUnlock()
		if !ok || !h.staged || h.committed {
			return -fuse.EROFS
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		f, err := os.OpenFile(h.localPath, os.O_WRONLY, 0644)
		if err != nil {
			return -fuse.EIO
		}
		defer f.Close()
		n, err := f.WriteAt(buff, ofst)
		if err != nil {
			return -fuse.EIO
		}
		if ofst+int64(n) > h.size {
			h.size = ofst + int64(n)
		}
		fs.trace("staged_write path=%s offset=%d length=%d result=success", path, ofst, n)
		return n
	}
	// Fully writable mode: unchanged behavior.
	path = cleanPath(path)

	fs.mu.RLock()
	h, ok := fs.files[path]
	fs.mu.RUnlock()
	if !ok {
		code, _ := fs.openWriteable(path, os.O_WRONLY, context.Background())
		if code != 0 {
			return code
		}
		fs.mu.RLock()
		h, ok = fs.files[path]
		fs.mu.RUnlock()
		if !ok {
			return -fuse.EIO
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	f, err := os.OpenFile(h.localPath, os.O_WRONLY, 0644)
	if err != nil {
		return -fuse.EIO
	}
	defer f.Close()

	n, err := f.WriteAt(buff, ofst)
	if err != nil {
		return -fuse.EIO
	}
	if ofst+int64(n) > h.size {
		h.size = ofst + int64(n)
	}
	h.dirty = true
	return n
}

func (fs *IPCFileSystem) Flush(path string, fh uint64) int {
	path = cleanPath(path)

	fs.mu.RLock()
	h, ok := fs.files[path]
	fs.mu.RUnlock()
	if !ok {
		return 0
	}

	// Staged handles: local sync only, no upload.
	if h.staged {
		fs.trace("Flush staged path=%s (no upload)", path)
		return 0
	}

	h.mu.Lock()
	dirty := h.dirty
	h.mu.Unlock()
	if dirty {
		if err := fs.ipcMarkDirty(fs.ctx, path); err != nil {
			return ipcErrToFuse(err)
		}
		h.mu.Lock()
		h.dirty = false
		h.mu.Unlock()
	}
	return 0
}

func (fs *IPCFileSystem) Fsync(path string, datasync bool, fh uint64) int {
	return fs.Flush(path, fh)
}

func (fs *IPCFileSystem) Mkdir(path string, mode uint32) int {
	started := time.Now()
	fs.trace("Mkdir begin path=%s mode=0x%x octal=%#o readOnly=%v mkdirOnly=%v mkdirRenameOnly=%v",
		path, mode, mode, fs.readOnly, fs.mkdirOnly, fs.mkdirRenameOnly)
	if fs.readOnly {
		fs.trace("Mkdir result path=%s err=%d elapsed=%s", path, -fuse.EROFS, time.Since(started))
		return -fuse.EROFS
	}
	path = cleanPath(path)
	if err := rejectReserved(path); err != nil {
		return -fuse.EINVAL
	}
	ctx := fs.ctx
	if err := fs.ipcMkdir(ctx, path); err != nil {
		rc := ipcErrToFuse(err)
		fs.trace("Mkdir ipcMkdir end path=%s ipcErr=%v err=%d elapsed=%s", path, err, rc, time.Since(started))
		return rc
	}
	fs.flushDirCacheFor(parentPath(path))
	fs.trace("Mkdir ipcMkdir end path=%s ipcErr=<nil> err=0 elapsed=%s", path, time.Since(started))
	return 0
}

func (fs *IPCFileSystem) Rename(oldPath, newPath string) int {
	started := time.Now()
	fs.trace("Rename begin old=%s new=%s ro=%v md=%v mdr=%v mdrm=%v mdrfr=%v mdrfrm=%v mdrfrmr=%v",
		oldPath, newPath, fs.readOnly, fs.mkdirOnly, fs.mkdirRenameOnly, fs.mkdirRenameMoveOnly, fs.mkdirRenameMoveFileRenameOnly, fs.mkdirRenameMoveFileRenameMoveOnly, fs.mkdirRenameMoveFileRenameMoveRemoveOnly)
	if fs.readOnly || fs.mkdirOnly {
		rc := fs.rejectMutation("Rename", oldPath+" -> "+newPath)
		fs.trace("Rename result old=%s new=%s err=%d elapsed=%s", oldPath, newPath, rc, time.Since(started))
		return rc
	}
	oldPath = cleanPath(oldPath)
	newPath = cleanPath(newPath)

	if fs.mkdirRenameOnly || fs.mkdirRenameMoveOnly {
		allowCross := fs.mkdirRenameMoveOnly
		rc := fs.renameOrMoveDirectory(oldPath, newPath, allowCross)
		fs.trace("Rename result old=%s new=%s err=%d elapsed=%s", oldPath, newPath, rc, time.Since(started))
		return rc
	}

	if fs.mkdirRenameMoveFileRenameOnly || fs.mkdirRenameMoveFileRenameMoveOnly || fs.mkdirRenameMoveFileRenameMoveRemoveOnly || fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		rc := fs.renameInFileRenameMode(oldPath, newPath)
		fs.trace("Rename result old=%s new=%s err=%d elapsed=%s", oldPath, newPath, rc, time.Since(started))
		return rc
	}

	if err := rejectReserved(newPath); err != nil {
		return -fuse.EINVAL
	}
	ctx := fs.ctx
	first, second := oldPath, newPath
	if first > second {
		first, second = second, first
	}
	unlock1 := fs.pathLock(first)
	var unlock2 func()
	if oldPath == newPath {
		unlock2 = unlock1
	} else {
		unlock2 = fs.pathLock(second)
	}
	defer unlock2()
	defer unlock1()
	if err := fs.ipcRename(ctx, oldPath, newPath); err != nil {
		return ipcErrToFuse(err)
	}
	fs.mu.Lock()
	if h, ok := fs.files[oldPath]; ok {
		h.path = newPath
		fs.files[newPath] = h
		delete(fs.files, oldPath)
	}
	fs.mu.Unlock()
	fs.flushDirCacheFor(parentPath(oldPath))
	fs.flushDirCacheFor(parentPath(newPath))
	return 0
}

// renameInFileRenameMode handles Rename in mkdirRenameMoveFileRenameOnly and
// mkdirRenameMoveFileRenameMoveOnly modes. Directories follow the same rules as
// mkdirRenameMoveOnly. Files: same-parent rename always allowed; cross-parent
// move with same basename is allowed only when mkdirRenameMoveFileRenameMoveOnly
// is active.
func (fs *IPCFileSystem) renameInFileRenameMode(oldPath, newPath string) int {
	ctx := fs.ctx

	if oldPath == "" {
		return -fuse.ENOENT
	}
	if err := rejectReserved(newPath); err != nil {
		return -fuse.EINVAL
	}

	// Determine source type.
	oldEntry, err := fs.ipcStat(ctx, oldPath)
	if err != nil {
		fs.trace("renameInFileRenameMode: oldPath stat failed old=%s err=%v", oldPath, err)
		return ipcErrToFuse(err)
	}

	if oldEntry.IsDir {
		// Directory: use the existing move-capable logic.
		return fs.renameOrMoveDirectory(oldPath, newPath, true)
	}

	// File dispatch.
	oldParent := parentPath(oldPath)
	newParent := parentPath(newPath)
	oldName := oldPath[strings.LastIndexByte(oldPath, '/')+1:]
	newName := newPath[strings.LastIndexByte(newPath, '/')+1:]
	sameParent := oldParent == newParent
	sameName := oldName == newName

	fs.trace("renameInFileRenameMode: file dispatch old=%s new=%s oldParent=%s newParent=%s oldName=%s newName=%s sameParent=%v sameName=%v allowFileMove=%v stage=validated",
		oldPath, newPath, oldParent, newParent, oldName, newName, sameParent, sameName, fs.mkdirRenameMoveFileRenameMoveOnly)

	if !sameParent {
		// Determine if same basename.
		oldName := oldPath[strings.LastIndexByte(oldPath, '/')+1:]
		newName := newPath[strings.LastIndexByte(newPath, '/')+1:]
		sameName := oldName == newName

		// Cross-parent with different basename: always rejected.
		if !sameName {
			fs.trace("renameInFileRenameMode: file cross-parent rename rejected old=%s new=%s reason=file-cross-parent-rename-not-supported",
				oldPath, newPath)
			return -fuse.EOPNOTSUPP
		}

		// Same-name cross-parent move: only allowed with mkdirRenameMoveFileRenameMoveOnly.
		if !fs.mkdirRenameMoveFileRenameMoveOnly && !fs.mkdirRenameMoveFileRenameMoveRemoveOnly && !fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
			fs.trace("renameInFileRenameMode: file cross-parent move rejected old=%s new=%s oldParent=%s newParent=%s reason=file-cross-parent-move-not-enabled",
				oldPath, newPath, oldParent, newParent)
			return -fuse.EROFS
		}
		// Fall through to IPC rename (daemon handles the Move API call).
	}

	if oldPath == newPath {
		return 0
	}

	// Verify target parent exists.
	newParentEntry, statErr := fs.ipcStat(ctx, newParent)
	if statErr != nil {
		fs.trace("renameInFileRenameMode: newParent stat failed newParent=%s err=%v", newParent, statErr)
		return ipcErrToFuse(statErr)
	}
	if !newParentEntry.IsDir {
		return -fuse.ENOTDIR
	}

	// Verify target does not exist.
	newEntry, statErr := fs.ipcStat(ctx, newPath)
	if statErr == nil && newEntry.Path != "" {
		return -fuse.EEXIST
	}

	// Execute rename via IPC (single call, daemon handles post-verification).
	fs.trace("renameInFileRenameMode: stage=before-ipcRename old=%s new=%s", oldPath, newPath)
	if err := fs.ipcRename(ctx, oldPath, newPath); err != nil {
		fs.trace("renameInFileRenameMode: stage=after-ipcRename old=%s new=%s ipcErr=%v errno=%d", oldPath, newPath, err, ipcErrToFuse(err))
		return ipcErrToFuse(err)
	}
	fs.trace("renameInFileRenameMode: stage=after-ipcRename old=%s new=%s ipcErr=<nil> errno=0", oldPath, newPath)

	// Update local file handle map.
	fs.mu.Lock()
	if h, ok := fs.files[oldPath]; ok {
		h.path = newPath
		fs.files[newPath] = h
		delete(fs.files, oldPath)
	}
	fs.mu.Unlock()

	// Invalidate caches.
	fs.flushDirCacheFor(oldParent)
	if oldParent != newParent {
		fs.flushDirCacheFor(newParent)
	}
	return 0
}

func (fs *IPCFileSystem) renameOrMoveDirectory(oldPath, newPath string, allowCross bool) int {
	ctx := fs.ctx

	if oldPath == "/" || oldPath == "" {
		return -fuse.EROFS
	}
	if err := rejectReserved(newPath); err != nil {
		return -fuse.EINVAL
	}

	oldEntry, err := fs.ipcStat(ctx, oldPath)
	if err != nil {
		return ipcErrToFuse(err)
	}
	if !oldEntry.IsDir {
		return -fuse.EROFS
	}

	oldParent := parentPath(oldPath)
	newParent := parentPath(newPath)
	sameParent := oldParent == newParent

	if !sameParent && !allowCross {
		return -fuse.EROFS
	}

	if !sameParent {
		// Cross-parent move validation.
		// Check destination parent exists and is a directory.
		newParentEntry, statErr := fs.ipcStat(ctx, newParent)
		if statErr != nil {
			return ipcErrToFuse(statErr)
		}
		if !newParentEntry.IsDir {
			return -fuse.ENOTDIR
		}
		// Reject move into own subtree: oldPath must not be a prefix of newPath.
		if strings.HasPrefix(newPath, oldPath+"/") {
			return -fuse.EINVAL
		}
		// Check same doc library (assume single-library mount for now).
		fs.trace("Rename cross-parent oldParent=%s newParent=%s sameLibrary=true", oldParent, newParent)
	}

	// Target must not exist.
	newEntry, statErr := fs.ipcStat(ctx, newPath)
	if statErr == nil && newEntry.Path != "" {
		return -fuse.EEXIST
	}

	if err := fs.ipcRename(ctx, oldPath, newPath); err != nil {
		return ipcErrToFuse(err)
	}

	fs.flushDirCacheFor(oldParent)
	fs.flushDirCacheFor(newParent)
	return 0
}

func (fs *IPCFileSystem) Unlink(path string) int {
	if fs.isRestrictedMode() && !fs.mkdirRenameMoveFileRenameMoveRemoveOnly && !fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return fs.rejectMutation("Unlink", path)
	}
	path = cleanPath(path)
	ctx := fs.ctx

	unlock := fs.pathLock(path)
	defer unlock()

	if err := fs.ipcRemove(ctx, path); err != nil {
		return ipcErrToFuse(err)
	}

	fs.mu.Lock()
	delete(fs.files, path)
	fs.mu.Unlock()

	fs.flushDirCacheFor(parentPath(path))
	return 0
}

func (fs *IPCFileSystem) Rmdir(path string) int {
	if fs.isRestrictedMode() && !fs.mkdirRenameMoveFileRenameMoveRemoveOnly && !fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return fs.rejectMutation("Rmdir", path)
	}
	path = cleanPath(path)
	ctx := fs.ctx

	unlock := fs.pathLock(path)
	defer unlock()

	if err := fs.ipcRemove(ctx, path); err != nil {
		return ipcErrToFuse(err)
	}
	fs.flushDirCacheFor(parentPath(path))
	return 0
}

func (fs *IPCFileSystem) Truncate(path string, size int64, fh uint64) int {
	if fs.isRestrictedMode() {
		// Allow truncate only on staged handles in copy-upload mode.
		if fs.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
			path = cleanPath(path)
			fs.mu.RLock()
			h, ok := fs.files[path]
			fs.mu.RUnlock()
			if ok && h.staged && !h.committed {
				h.mu.Lock()
				defer h.mu.Unlock()
				if err := os.Truncate(h.localPath, size); err != nil {
					return -fuse.EIO
				}
				h.size = size
				return 0
			}
		}
		return fs.rejectMutation("Truncate", path)
	}
	path = cleanPath(path)

	fs.mu.RLock()
	h, ok := fs.files[path]
	fs.mu.RUnlock()
	if !ok {
		return 0
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if err := os.Truncate(h.localPath, size); err != nil {
		return -fuse.EIO
	}
	h.size = size
	h.dirty = true
	return 0
}

func (fs *IPCFileSystem) Utimens(path string, tmsp []fuse.Timespec) int {
	if fs.isRestrictedMode() {
		return fs.rejectMutation("Utimens", path)
	}
	return 0
}

func (fs *IPCFileSystem) Chmod(path string, mode uint32) int {
	if fs.isRestrictedMode() {
		return fs.rejectMutation("Chmod", path)
	}
	return -fuse.ENOSYS
}

func (fs *IPCFileSystem) Chown(path string, uid uint32, gid uint32) int {
	if fs.isRestrictedMode() {
		return fs.rejectMutation("Chown", path)
	}
	return -fuse.ENOSYS
}

func (fs *IPCFileSystem) Setxattr(path, name string, value []byte, flags int) int {
	if fs.isRestrictedMode() {
		return fs.rejectMutation("Setxattr", path)
	}
	return -fuse.ENOSYS
}

func (fs *IPCFileSystem) Removexattr(path, name string) int {
	if fs.isRestrictedMode() {
		return fs.rejectMutation("Removexattr", path)
	}
	return -fuse.ENOSYS
}

func (fs *IPCFileSystem) rejectMutation(operation, path string) int {
	rc := -fuse.EROFS
	fs.trace("operation=%s path=%s readOnly=%v mkdirOnly=%v returned_errno=%d",
		operation, cleanPath(path), fs.readOnly, fs.mkdirOnly, rc)
	return rc
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (fs *IPCFileSystem) flushDirCacheFor(parent string) {
	fs.dirsMu.Lock()
	delete(fs.dirs, parent)
	if parent != "/" {
		delete(fs.dirs, "/")
	}
	fs.dirsMu.Unlock()
}

func fillStatFromEntry(entry ipc.FSEntry, stat *fuse.Stat_t) {
	modTime, _ := time.Parse(time.RFC3339, entry.ModTime)
	if entry.IsDir {
		stat.Mode = syscall.S_IFDIR | 0555
		stat.Size = 0
		stat.Nlink = 2
	} else {
		stat.Mode = syscall.S_IFREG | 0444
		stat.Size = entry.Size
		stat.Nlink = 1
	}
	if !modTime.IsZero() {
		stat.Atim = fuse.NewTimespec(modTime)
		stat.Mtim = fuse.NewTimespec(modTime)
		stat.Ctim = fuse.NewTimespec(modTime)
		stat.Birthtim = fuse.NewTimespec(modTime)
	} else {
		n := fuse.Now()
		stat.Atim = n
		stat.Mtim = n
		stat.Ctim = n
		stat.Birthtim = n
	}
}

func ipcErrToFuse(err error) int {
	if err == nil {
		return 0
	}
	if errors.Is(err, os.ErrNotExist) {
		return -fuse.ENOENT
	}
	msg := err.Error()
	if strings.Contains(msg, "not found") || strings.Contains(msg, "no such file") {
		return -fuse.ENOENT
	}
	if strings.Contains(msg, "is a directory") {
		return -fuse.EISDIR
	}
	if strings.Contains(msg, "read_only_filesystem") || strings.Contains(msg, "not allowed") {
		return -fuse.EROFS
	}
	if strings.Contains(msg, "target already exists") || strings.Contains(msg, "already exists") {
		return -fuse.EEXIST
	}
	if strings.Contains(msg, "not a directory") || strings.Contains(msg, "target parent is not a directory") {
		return -fuse.ENOTDIR
	}
	if strings.Contains(msg, "not supported") || strings.Contains(msg, "is not enabled") {
		return -fuse.EOPNOTSUPP
	}
	if strings.Contains(msg, "not empty") {
		return -fuse.ENOTEMPTY
	}
	if strings.Contains(msg, "unauthorized") || strings.Contains(msg, "forbidden") {
		return -fuse.EACCES
	}
	if strings.Contains(msg, "verification failed") {
		return -fuse.EIO
	}
	return -fuse.EIO
}

func validateCachePath(cachePath, cacheDir string) error {
	clean := filepath.Clean(cachePath)
	cleanDir := filepath.Clean(cacheDir) + string(filepath.Separator)
	if !strings.HasPrefix(clean, cleanDir) {
		return fmt.Errorf("cache path outside cache dir: %q", cachePath)
	}
	rel, err := filepath.Rel(cleanDir, clean)
	if err != nil || strings.Contains(rel, "..") {
		return fmt.Errorf("cache path traversal: %q", cachePath)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("cache file not found: %q", cachePath)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cache path is not a regular file: %q", cachePath)
	}
	return nil
}

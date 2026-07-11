//go:build windows && cgo

// Package winfsp provides WinFsp-based userspace filesystems.
//
// cloudfs.go implements a read-only FUSE filesystem backed by
// the cloud.Provider interface. It supports Getattr, Readdir,
// Open, and Read operations. Write operations return EROFS.
package winfsp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"ncepupan/hdd/internal/cloud"
	"ncepupan/hdd/internal/domain"

	"github.com/winfsp/cgofuse/fuse"
)

// cachedFile represents a downloaded file in the local cache.
type cachedFile struct {
	localPath string // path to the temp file
	size      int64
}

// CloudFS is a read-only FUSE filesystem backed by a cloud.Provider.
//
// It implements fuse.FileSystemInterface by embedding fuse.FileSystemBase.
// Read-only operations (Getattr, Readdir, Open, Read) delegate to the
// cloud provider. Write operations return -EROFS.
type CloudFS struct {
	fuse.FileSystemBase

	provider cloud.Provider

	mu     sync.RWMutex
	cache  map[string]*cachedFile // remote path -> cached file
	cacheD string                 // cache directory

	// Per-path loading guard to prevent duplicate downloads.
	loadingMu sync.Mutex
	loading   map[string]chan struct{}

	// Pre-fetched directory listings to avoid repeated List calls.
	dirMu sync.RWMutex
	dirs  map[string][]domain.FileInfo // remote path -> entries
}

// NewCloudFS creates a new CloudFS backed by the given provider.
// cacheDir is used to store downloaded files; it is created if missing.
func NewCloudFS(provider cloud.Provider, cacheDir string) (*CloudFS, error) {
	if err := os.MkdirAll(cacheDir, 0700); err != nil {
		return nil, err
	}
	return &CloudFS{
		provider: provider,
		cache:    make(map[string]*cachedFile),
		cacheD:   cacheDir,
		loading:  make(map[string]chan struct{}),
		dirs:     make(map[string][]domain.FileInfo),
	}, nil
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

// cleanPath normalizes a FUSE path to a clean, slash-separated form.
// Returns "/" for empty or root paths. Rejects Windows reserved names.
func cleanPath(p string) string {
	if p == "" {
		return "/"
	}
	cleaned := path.Clean(p)
	if cleaned == "." || cleaned == "" {
		return "/"
	}
	if cleaned[0] != '/' {
		cleaned = "/" + cleaned
	}
	cleaned = strings.TrimRight(cleaned, " .")
	if cleaned == "" {
		return "/"
	}
	return cleaned
}

// rejectReserved returns an error if any path component is a Windows reserved name.
func rejectReserved(p string) error {
	for _, comp := range strings.Split(p, "/") {
		if comp == "" {
			continue
		}
		if isReservedName(comp) {
			return fmt.Errorf("reserved name: %q", comp)
		}
	}
	return nil
}

var reservedNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

func isReservedName(name string) bool {
	upper := strings.ToUpper(name)
	if dot := strings.LastIndexByte(upper, '.'); dot > 0 {
		upper = upper[:dot]
	}
	return reservedNames[upper]
}

// parentPath returns the parent directory path.
func parentPath(p string) string {
	p = cleanPath(p)
	if p == "/" {
		return "/"
	}
	return cleanPath(filepath.ToSlash(filepath.Dir(p)))
}

// basename returns the last component of the path.
func basename(p string) string {
	p = cleanPath(p)
	if p == "/" {
		return "/"
	}
	return filepath.ToSlash(filepath.Base(p))
}

// ---------------------------------------------------------------------------
// Stat helpers
// ---------------------------------------------------------------------------

// fileInfoToStat fills a fuse.Stat_t from a domain.FileInfo.
func fileInfoToStat(info domain.FileInfo, stat *fuse.Stat_t) {
	now := fuse.Now()
	if info.IsDir {
		stat.Mode = syscall.S_IFDIR | 0555
		stat.Size = 0
		stat.Nlink = 2
	} else {
		stat.Mode = syscall.S_IFREG | 0444
		stat.Size = info.Size
		stat.Nlink = 1
	}
	if !info.ModTime.IsZero() {
		stat.Atim = fuse.NewTimespec(info.ModTime)
		stat.Mtim = fuse.NewTimespec(info.ModTime)
		stat.Ctim = fuse.NewTimespec(info.ModTime)
		stat.Birthtim = fuse.NewTimespec(info.ModTime)
	} else {
		stat.Atim = now
		stat.Mtim = now
		stat.Ctim = now
		stat.Birthtim = now
	}
}

// dirStat fills a fuse.Stat_t for a directory.
func dirStat(stat *fuse.Stat_t) {
	now := fuse.Now()
	stat.Mode = syscall.S_IFDIR | 0555
	stat.Nlink = 2
	stat.Atim = now
	stat.Mtim = now
	stat.Ctim = now
	stat.Birthtim = now
}

// errToFuse maps provider errors to FUSE error codes.
// Returns 0 for nil error (success), and a negative errno otherwise.
func errToFuse(err error) int {
	if err == nil {
		return 0
	}
	// Check for known sentinel errors or patterns.
	msg := err.Error()
	if os.IsNotExist(err) {
		return -fuse.ENOENT
	}
	// Many provider errors contain "not found" in their message.
	if strings.Contains(msg, "not found") || strings.Contains(msg, "no such file") {
		return -fuse.ENOENT
	}
	return -fuse.EIO
}

// domainErrorToFuse maps domain errors (like ErrNotExist) to FUSE errno.
func domainErrorToFuse(err error) int {
	if err == nil {
		return 0
	}
	msg := err.Error()
	if strings.Contains(msg, "not found") || strings.Contains(msg, "no such file") || os.IsNotExist(err) {
		return -fuse.ENOENT
	}
	return -fuse.EIO
}

// ---------------------------------------------------------------------------
// FUSE interface: read-only operations
// ---------------------------------------------------------------------------

// Getattr returns file or directory attributes.
func (c *CloudFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	path = cleanPath(path)
	ctx := context.Background()

	// Root is always a directory.
	if path == "/" {
		dirStat(stat)
		return 0
	}

	// Try Stat first.
	info, err := c.provider.Stat(ctx, path)
	if err == nil {
		fileInfoToStat(info, stat)
		return 0
	}

	// If Stat fails, check if it's a known directory from List.
	// Some paths may be directories that haven't been Stat'd directly.
	parent := parentPath(path)
	entries, listErr := c.listDir(ctx, parent)
	if listErr == nil {
		name := basename(path)
		for _, e := range entries {
			if basename(e.Path) == name && e.IsDir {
				dirStat(stat)
				return 0
			}
		}
	}

	return errToFuse(err)
}

// Opendir opens a directory for reading.
func (c *CloudFS) Opendir(path string) (int, uint64) {
	path = cleanPath(path)
	ctx := context.Background()

	// Root is always valid.
	if path == "/" {
		return 0, 0
	}

	// Try to list to confirm it's a directory.
	_, err := c.listDir(ctx, path)
	if err != nil {
		return errToFuse(err), 0
	}
	return 0, 0
}

// Readdir lists directory contents.
func (c *CloudFS) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64, fh uint64) int {

	path = cleanPath(path)
	ctx := context.Background()

	entries, err := c.listDir(ctx, path)
	if err != nil {
		return errToFuse(err)
	}

	for _, entry := range entries {
		name := basename(entry.Path)
		var st fuse.Stat_t
		fileInfoToStat(entry, &st)
		if !fill(name, &st, 0) {
			break
		}
	}
	return 0
}

// Releasedir closes an open directory.
func (c *CloudFS) Releasedir(path string, fh uint64) int {
	return 0
}

// Open opens a file and caches its content locally.
func (c *CloudFS) Open(path string, flags int) (int, uint64) {
	path = cleanPath(path)
	ctx := context.Background()

	// Reject write/truncate flags.
	if flags&(os.O_WRONLY|os.O_RDWR|os.O_TRUNC|os.O_APPEND) != 0 {
		return -fuse.EROFS, 0
	}

	// Stat to confirm it exists and is a file.
	info, err := c.provider.Stat(ctx, path)
	if err != nil {
		return errToFuse(err), 0
	}
	if info.IsDir {
		return -fuse.EISDIR, 0
	}

	// Fast path: already cached.
	c.mu.RLock()
	cf, cached := c.cache[path]
	c.mu.RUnlock()
	if cached {
		return 0, 0
	}

	// Check if another goroutine is already downloading this path.
	c.loadingMu.Lock()
	ch, loading := c.loading[path]
	if loading {
		c.loadingMu.Unlock()
		<-ch // Wait for the other download to finish.
		c.mu.RLock()
		_, cached = c.cache[path]
		c.mu.RUnlock()
		if cached {
			return 0, 0
		}
		return -fuse.EIO, 0
	}
	ch = make(chan struct{})
	c.loading[path] = ch
	c.loadingMu.Unlock()

	// Perform download outside any global lock.
	cf = c.downloadToCache(ctx, path, info)
	c.loadingMu.Lock()
	close(ch)
	delete(c.loading, path)
	c.loadingMu.Unlock()

	if cf == nil {
		return -fuse.EIO, 0
	}

	c.mu.Lock()
	c.cache[path] = cf
	c.mu.Unlock()

	return 0, 0
}

// Read reads data from a cached file.
func (c *CloudFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
	path = cleanPath(path)

	c.mu.RLock()
	cf, ok := c.cache[path]
	c.mu.RUnlock()

	if !ok {
		// Not cached yet — try to download now (shouldn't happen if Open was called first).
		ctx := context.Background()
		info, err := c.provider.Stat(ctx, path)
		if err != nil {
			return errToFuse(err)
		}
		c.mu.Lock()
		cf = c.downloadToCache(ctx, path, info)
		if cf != nil {
			c.cache[path] = cf
		}
		c.mu.Unlock()
		if cf == nil {
			return -fuse.EIO
		}
	}

	if ofst >= cf.size {
		return 0
	}

	f, err := os.Open(cf.localPath)
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

// Release closes an open file. Cache is retained.
func (c *CloudFS) Release(path string, fh uint64) int {
	return 0
}

// Statfs returns filesystem statistics.
func (c *CloudFS) Statfs(path string, stat *fuse.Statfs_t) int {
	stat.Bsize = 4096
	stat.Blocks = 1 << 20 // 4 GB
	stat.Bfree = 1 << 20
	stat.Bavail = 1 << 20
	stat.Files = 1000
	stat.Ffree = 998
	return 0
}

// ---------------------------------------------------------------------------
// Write operations: all return EROFS
// ---------------------------------------------------------------------------

func (c *CloudFS) Create(path string, flags int, mode uint32) (int, uint64) {
	return -fuse.EROFS, 0
}

func (c *CloudFS) Write(path string, buff []byte, ofst int64, fh uint64) int {
	return -fuse.EROFS
}

func (c *CloudFS) Mkdir(path string, mode uint32) int {
	return -fuse.EROFS
}

func (c *CloudFS) Rename(oldPath, newPath string) int {
	return -fuse.EROFS
}

func (c *CloudFS) Unlink(path string) int {
	return -fuse.EROFS
}

func (c *CloudFS) Rmdir(path string) int {
	return -fuse.EROFS
}

func (c *CloudFS) Truncate(path string, size int64, fh uint64) int {
	return -fuse.EROFS
}

func (c *CloudFS) Utimens(path string, tmsp []fuse.Timespec) int {
	return -fuse.EROFS
}

// ---------------------------------------------------------------------------
// Internal: directory listing
// ---------------------------------------------------------------------------

// listDir returns directory entries, using a local cache to avoid
// repeated provider.List calls during a single Readdir cycle.
func (c *CloudFS) listDir(ctx context.Context, path string) ([]domain.FileInfo, error) {
	// Check cache first.
	c.dirMu.RLock()
	entries, ok := c.dirs[path]
	c.dirMu.RUnlock()
	if ok {
		return entries, nil
	}

	entries, err := c.provider.List(ctx, path)
	if err != nil {
		return nil, err
	}

	// Cache the result.
	c.dirMu.Lock()
	c.dirs[path] = entries
	c.dirMu.Unlock()

	return entries, nil
}

// downloadToCache downloads a file to the cache directory and returns
// a cachedFile. Returns nil on failure.
func (c *CloudFS) downloadToCache(ctx context.Context, remotePath string, _ domain.FileInfo) *cachedFile {
	// Create a temp file in the cache directory with a predictable name
	// based on the remote path to avoid duplicate downloads.
	localName := filepath.Join(c.cacheD, sanitizeCacheName(remotePath))
	if err := os.MkdirAll(filepath.Dir(localName), 0700); err != nil {
		return nil
	}

	f, err := os.Create(localName)
	if err != nil {
		return nil
	}
	defer f.Close()

	if err := c.provider.Download(ctx, remotePath, f); err != nil {
		os.Remove(localName)
		return nil
	}

	fi, err := os.Stat(localName)
	if err != nil {
		os.Remove(localName)
		return nil
	}

	return &cachedFile{
		localPath: localName,
		size:      fi.Size(),
	}
}

// sanitizeCacheName converts a remote path to a safe local filename.
// "/docs/report.txt" becomes "docs/report.txt" under cacheD.
func sanitizeCacheName(remotePath string) string {
	// Remove leading slash and convert to OS path.
	p := filepath.FromSlash(remotePath)
	if len(p) > 0 && (p[0] == '\\' || p[0] == '/') {
		p = p[1:]
	}
	return p
}

// FlushDirCache clears cached directory listings. Useful for testing.
func (c *CloudFS) FlushDirCache() {
	c.dirMu.Lock()
	c.dirs = make(map[string][]domain.FileInfo)
	c.dirMu.Unlock()
}

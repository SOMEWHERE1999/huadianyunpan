//go:build windows && cgo

// Package winfsp provides WinFsp-based userspace filesystems.
//
// memfs.go implements a minimal read-only in-memory filesystem
// backed by github.com/winfsp/cgofuse/fuse.
package winfsp

import (
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

// memFile holds a single in-memory file.
type memFile struct {
	name    string
	content []byte
	mode    uint32
}

// MemFS is a minimal read-only in-memory FUSE filesystem.
//
// It implements fuse.FileSystemInterface by embedding fuse.FileSystemBase
// and overriding only the methods required for read-only access:
// Getattr, Readdir, Open, Read.
type MemFS struct {
	fuse.FileSystemBase

	mu    sync.RWMutex
	files map[string]*memFile // path -> file
	dirs  map[string][]string // path -> entry names
	now   time.Time
}

// NewMemFS creates a MemFS pre-populated with hello.txt and README.txt.
func NewMemFS() *MemFS {
	now := time.Now()
	m := &MemFS{
		files: make(map[string]*memFile),
		dirs:  make(map[string][]string),
		now:   now,
	}

	// Root directory
	m.dirs["/"] = []string{"hello.txt", "README.txt"}

	// hello.txt
	m.files["/hello.txt"] = &memFile{
		name:    "hello.txt",
		content: []byte("Hello from Huadian Drive!\n"),
		mode:    syscall.S_IFREG | 0444,
	}

	// README.txt
	m.files["/README.txt"] = &memFile{
		name: "README.txt",
		content: []byte(
			"Huadian Drive - WinFsp MemFS Demo\n" +
				"================================\n" +
				"\n" +
				"This is a minimal read-only in-memory filesystem\n" +
				"backed by github.com/winfsp/cgofuse/fuse.\n" +
				"\n" +
				"It verifies that WinFsp and cgofuse are correctly\n" +
				"installed and functional on this Windows system.\n"),
		mode: syscall.S_IFREG | 0444,
	}

	return m
}

// statFromMemFile fills a Stat_t from a memFile.
func (m *MemFS) statFromMemFile(f *memFile, stat *fuse.Stat_t) {
	stat.Mode = f.mode
	stat.Size = int64(len(f.content))
	stat.Nlink = 1
	stat.Atim = fuse.NewTimespec(m.now)
	stat.Mtim = fuse.NewTimespec(m.now)
	stat.Ctim = fuse.NewTimespec(m.now)
	stat.Birthtim = fuse.NewTimespec(m.now)
}

// statDir fills a Stat_t for a directory.
func (m *MemFS) statDir(stat *fuse.Stat_t) {
	stat.Mode = syscall.S_IFDIR | 0555
	stat.Nlink = 2
	stat.Atim = fuse.NewTimespec(m.now)
	stat.Mtim = fuse.NewTimespec(m.now)
	stat.Ctim = fuse.NewTimespec(m.now)
	stat.Birthtim = fuse.NewTimespec(m.now)
}

// Getattr returns file attributes.
func (m *MemFS) Getattr(path string, stat *fuse.Stat_t, fh uint64) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if path == "/" {
		m.statDir(stat)
		return 0
	}

	f, ok := m.files[path]
	if !ok {
		return -fuse.ENOENT
	}
	m.statFromMemFile(f, stat)
	return 0
}

// Opendir opens a directory.
func (m *MemFS) Opendir(path string) (int, uint64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if path == "/" {
		return 0, 0
	}
	return -fuse.ENOENT, 0
}

// Readdir lists directory contents.
func (m *MemFS) Readdir(path string,
	fill func(name string, stat *fuse.Stat_t, ofst int64) bool,
	ofst int64, fh uint64) int {

	m.mu.RLock()
	defer m.mu.RUnlock()

	if path != "/" {
		return -fuse.ENOENT
	}

	entries, ok := m.dirs[path]
	if !ok {
		return 0
	}

	for _, name := range entries {
		full := "/" + name
		f, exists := m.files[full]
		if !exists {
			continue
		}
		var stat fuse.Stat_t
		m.statFromMemFile(f, &stat)
		if !fill(name, &stat, 0) {
			break
		}
	}
	return 0
}

// Releasedir closes an open directory.
func (m *MemFS) Releasedir(path string, fh uint64) int {
	return 0
}

// Open opens a file.
func (m *MemFS) Open(path string, flags int) (int, uint64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	_, ok := m.files[path]
	if !ok {
		return -fuse.ENOENT, 0
	}

	if flags&(os.O_WRONLY|os.O_RDWR|os.O_TRUNC|os.O_APPEND) != 0 {
		return -fuse.EROFS, 0
	}
	return 0, 0
}

// Read reads from an open file.
func (m *MemFS) Read(path string, buff []byte, ofst int64, fh uint64) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	f, ok := m.files[path]
	if !ok {
		return -fuse.ENOENT
	}

	if ofst >= int64(len(f.content)) {
		return 0
	}

	n := copy(buff, f.content[ofst:])
	return n
}

// Release closes an open file.
func (m *MemFS) Release(path string, fh uint64) int {
	return 0
}

// Statfs returns filesystem statistics.
func (m *MemFS) Statfs(path string, stat *fuse.Statfs_t) int {
	stat.Bsize = 4096
	stat.Blocks = 1 << 20
	stat.Bfree = 1 << 20
	stat.Bavail = 1 << 20
	stat.Files = 1000
	stat.Ffree = 998
	return 0
}

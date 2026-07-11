//go:build windows && cgo

package winfsp

import (
	"errors"
	"os"
	"syscall"
	"testing"
	"time"

	"ncepupan/hdd/internal/domain"

	"github.com/winfsp/cgofuse/fuse"
)

// ---------------------------------------------------------------------------
// Path normalization tests
// ---------------------------------------------------------------------------

func TestCleanPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"", "/"},
		{"/", "/"},
		{"/hello.txt", "/hello.txt"},
		{"/docs/report.txt", "/docs/report.txt"},
		{"//double//slash", "/double/slash"},
		{"/trailing/", "/trailing"},
		{"/./dot", "/dot"},
		{"/涓枃/鏂囦欢.txt", "/涓枃/鏂囦欢.txt"},
		{"/has spaces/file name.txt", "/has spaces/file name.txt"},
		{"no-leading-slash", "/no-leading-slash"},
		{"/..", "/"},
		{"/a/../b", "/b"},
	}

	for _, tt := range tests {
		got := cleanPath(tt.input)
		if got != tt.want {
			t.Errorf("cleanPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestParentPath(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/", "/"},
		{"/a", "/"},
		{"/a/b", "/a"},
		{"/a/b/c.txt", "/a/b"},
		{"/涓枃/鐩綍", "/涓枃"},
	}

	for _, tt := range tests {
		got := parentPath(tt.input)
		if got != tt.want {
			t.Errorf("parentPath(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestBasename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/", "/"},
		{"/a.txt", "a.txt"},
		{"/a/b.txt", "b.txt"},
		{"/涓枃/鏂囦欢.txt", "鏂囦欢.txt"},
		{"/has spaces/name with spaces.txt", "name with spaces.txt"},
	}

	for _, tt := range tests {
		got := basename(tt.input)
		if got != tt.want {
			t.Errorf("basename(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// fileInfoToStat tests
// ---------------------------------------------------------------------------

func TestFileInfoToStat_File(t *testing.T) {
	info := domain.FileInfo{
		Path:    "/docs/report.txt",
		Size:    1024,
		IsDir:   false,
		ModTime: time.Now(),
	}
	var stat fuse.Stat_t
	fileInfoToStat(info, &stat)

	if stat.Mode != uint32(syscall.S_IFREG|0444) {
		t.Errorf("file mode: got %o, want %o", stat.Mode, syscall.S_IFREG|0444)
	}
	if stat.Size != 1024 {
		t.Errorf("file size: got %d, want 1024", stat.Size)
	}
}

func TestFileInfoToStat_Dir(t *testing.T) {
	info := domain.FileInfo{
		Path:    "/docs",
		Size:    0,
		IsDir:   true,
		ModTime: time.Now(),
	}
	var stat fuse.Stat_t
	fileInfoToStat(info, &stat)

	if stat.Mode != uint32(syscall.S_IFDIR|0555) {
		t.Errorf("dir mode: got %o, want %o", stat.Mode, syscall.S_IFDIR|0555)
	}
	if stat.Nlink != 2 {
		t.Errorf("dir nlink: got %d, want 2", stat.Nlink)
	}
}

func TestDirStat(t *testing.T) {
	var stat fuse.Stat_t
	dirStat(&stat)

	if stat.Mode != uint32(syscall.S_IFDIR|0555) {
		t.Errorf("dir mode: got %o, want %o", stat.Mode, syscall.S_IFDIR|0555)
	}
}

// ---------------------------------------------------------------------------
// Error mapping tests
// ---------------------------------------------------------------------------

func TestErrToFuse_Nil(t *testing.T) {
	if errToFuse(nil) != 0 {
		t.Error("errToFuse(nil) should return 0")
	}
}

func TestErrToFuse_NotExist(t *testing.T) {
	err := os.ErrNotExist
	code := errToFuse(err)
	if code != -fuse.ENOENT {
		t.Errorf("errToFuse(os.ErrNotExist) = %d, want %d", code, -fuse.ENOENT)
	}
}

func TestErrToFuse_NotFoundInMessage(t *testing.T) {
	err := errors.New("mock: not found: \"/missing.txt\"")
	code := errToFuse(err)
	if code != -fuse.ENOENT {
		t.Errorf("errToFuse(not found) = %d, want %d", code, -fuse.ENOENT)
	}
}

func TestErrToFuse_Generic(t *testing.T) {
	err := errors.New("some other error")
	code := errToFuse(err)
	if code != -fuse.EIO {
		t.Errorf("errToFuse(generic) = %d, want %d", code, -fuse.EIO)
	}
}

func TestDomainErrorToFuse(t *testing.T) {
	if domainErrorToFuse(nil) != 0 {
		t.Error("domainErrorToFuse(nil) should return 0")
	}

	err := errors.New("not found: /x")
	if domainErrorToFuse(err) != -fuse.ENOENT {
		t.Error("domainErrorToFuse(not found) should return -ENOENT")
	}
}

// ---------------------------------------------------------------------------
// sanitizeCacheName tests
// ---------------------------------------------------------------------------

func TestSanitizeCacheName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/docs/report.txt", "docs\\report.txt"},
		{"/hello.txt", "hello.txt"},
		{"/涓枃/鏂囦欢.txt", "涓枃\\鏂囦欢.txt"},
		{"/name with spaces.txt", "name with spaces.txt"},
		{"//double", "\\double"},
	}

	for _, tt := range tests {
		got := sanitizeCacheName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeCacheName(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

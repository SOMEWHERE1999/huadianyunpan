// Package mock provides a filesystem-backed Provider for testing and development.
package mock

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ncepupan/hdd/internal/cloud"
	"ncepupan/hdd/internal/domain"
)

// Compile-time checks.
var _ cloud.Provider = (*MockProvider)(nil)
var _ cloud.DirectRemoteProvider = (*MockProvider)(nil)

// MockProvider is a filesystem-backed cloud provider for testing.
// Remote paths are mapped under the local root directory.
type MockProvider struct {
	root string
}

// New creates a MockProvider rooted at dir.
// The caller is responsible for cleaning up dir.
func New(dir string) *MockProvider {
	return &MockProvider{root: dir}
}

// Root returns the local root directory for inspection in tests.
func (m *MockProvider) Root() string { return m.root }

func (m *MockProvider) Name() string { return "mock" }

func (m *MockProvider) Connect(_ context.Context) error    { return nil }
func (m *MockProvider) Disconnect(_ context.Context) error { return nil }

// localPath converts a remote slash-separated path to a local filesystem path.
// It rejects paths that attempt to escape the root.
func (m *MockProvider) localPath(remotePath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(strings.TrimLeft(remotePath, "/")))
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("mock: path must be relative: %q", remotePath)
	}
	abs := filepath.Join(m.root, clean)
	// Guard against traversal: abs must be under root.
	rel, err := filepath.Rel(m.root, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("mock: path escapes root: %q", remotePath)
	}
	// Resolve any reparse points (junctions/symlinks) on the deepest
	// existing component, then re-validate the resolved path.
	resolved := m.resolveSymlinks(abs)
	if resolved != abs {
		rel2, err := filepath.Rel(m.root, resolved)
		if err != nil || strings.HasPrefix(rel2, "..") {
			return "", fmt.Errorf("mock: path escapes root via reparse point: %q", remotePath)
		}
		return resolved, nil
	}
	return abs, nil
}

// resolveSymlinks resolves reparse points on the deepest existing ancestor
// and reconstructs the full path.  Returns the original path if no component
// can be resolved.
func (m *MockProvider) resolveSymlinks(abs string) string {
	if r, err := filepath.EvalSymlinks(abs); err == nil && r != abs {
		return r
	}
	// Walk up to find the deepest existing ancestor that is a reparse point.
	parent := abs
	for {
		parent = filepath.Dir(parent)
		if parent == m.root || len(parent) < len(m.root) {
			return abs
		}
		if r, err := filepath.EvalSymlinks(parent); err == nil && r != parent {
			rel, _ := filepath.Rel(parent, abs)
			return filepath.Join(r, rel)
		}
		// If EvalSymlinks fails, the component doesn't exist; keep going up.
	}
}

// remotePath converts a local path back to a slash-separated remote path.
func (m *MockProvider) remotePath(local string) string {
	rel, err := filepath.Rel(m.root, local)
	if err != nil {
		return "/"
	}
	return "/" + filepath.ToSlash(rel)
}

func (m *MockProvider) List(_ context.Context, remotePath string) ([]domain.FileInfo, error) {
	abs, err := m.localPath(remotePath)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("mock: readdir %q: %w", remotePath, err)
	}

	results := make([]domain.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		results = append(results, domain.FileInfo{
			Path:    m.remotePath(filepath.Join(abs, e.Name())),
			Size:    info.Size(),
			ModTime: info.ModTime().Truncate(time.Second),
			IsDir:   info.IsDir(),
		})
	}
	return results, nil
}

func (m *MockProvider) Stat(_ context.Context, remotePath string) (domain.FileInfo, error) {
	abs, err := m.localPath(remotePath)
	if err != nil {
		return domain.FileInfo{}, err
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return domain.FileInfo{}, fmt.Errorf("mock: not found: %q", remotePath)
		}
		return domain.FileInfo{}, fmt.Errorf("mock: stat %q: %w", remotePath, err)
	}
	return domain.FileInfo{
		Path:    m.remotePath(abs),
		Size:    info.Size(),
		ModTime: info.ModTime().Truncate(time.Second),
		IsDir:   info.IsDir(),
	}, nil
}

func (m *MockProvider) Upload(_ context.Context, remotePath string, r io.Reader) error {
	abs, err := m.localPath(remotePath)
	if err != nil {
		return err
	}

	// Ensure parent directories exist.
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mock: mkdir parent %q: %w", remotePath, err)
	}

	f, err := os.Create(abs)
	if err != nil {
		return fmt.Errorf("mock: create %q: %w", remotePath, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("mock: write %q: %w", remotePath, err)
	}
	return nil
}

func (m *MockProvider) Download(_ context.Context, remotePath string, w io.Writer) error {
	abs, err := m.localPath(remotePath)
	if err != nil {
		return err
	}

	f, err := os.Open(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("mock: not found: %q", remotePath)
		}
		return fmt.Errorf("mock: open %q: %w", remotePath, err)
	}
	defer f.Close()

	if _, err := io.Copy(w, f); err != nil {
		return fmt.Errorf("mock: read %q: %w", remotePath, err)
	}
	return nil
}

func (m *MockProvider) Mkdir(_ context.Context, remotePath string) error {
	abs, err := m.localPath(remotePath)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(abs, 0700); err != nil {
		return fmt.Errorf("mock: mkdir %q: %w", remotePath, err)
	}
	return nil
}

func (m *MockProvider) Remove(_ context.Context, remotePath string) error {
	abs, err := m.localPath(remotePath)
	if err != nil {
		return err
	}

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("mock: not found: %q", remotePath)
		}
		return fmt.Errorf("mock: stat %q: %w", remotePath, err)
	}

	if info.IsDir() {
		entries, _ := os.ReadDir(abs)
		if len(entries) > 0 {
			return fmt.Errorf("mock: directory not empty: %q", remotePath)
		}
	}

	if err := os.Remove(abs); err != nil {
		return fmt.Errorf("mock: remove %q: %w", remotePath, err)
	}
	return nil
}

func (m *MockProvider) Move(_ context.Context, sourcePath, destinationDirectory string, policy cloud.TransferConflictPolicy) (cloud.TransferResult, error) {
	if policy != cloud.TransferConflictFail {
		return cloud.TransferResult{}, fmt.Errorf("mock: unsupported transfer conflict policy %q", policy)
	}
	name := sourcePath
	if idx := strings.LastIndexByte(sourcePath, '/'); idx >= 0 {
		name = sourcePath[idx+1:]
	}
	newPath := destinationDirectory
	if destinationDirectory != "/" {
		newPath += "/"
	}
	newPath += name
	if err := m.Rename(nil, sourcePath, newPath); err != nil {
		return cloud.TransferResult{}, err
	}
	return cloud.TransferResult{
		SourcePath:           sourcePath,
		DestinationDirectory: destinationDirectory,
		FinalPath:            newPath,
		FinalName:            name,
	}, nil
}

func (m *MockProvider) Copy(_ context.Context, sourcePath, destinationDirectory string, policy cloud.TransferConflictPolicy) (cloud.TransferResult, error) {
	if policy != cloud.TransferConflictFail {
		return cloud.TransferResult{}, fmt.Errorf("mock: unsupported transfer conflict policy %q", policy)
	}
	srcAbs, err := m.localPath(sourcePath)
	if err != nil {
		return cloud.TransferResult{}, err
	}
	srcInfo, err := os.Stat(srcAbs)
	if err != nil {
		return cloud.TransferResult{}, fmt.Errorf("mock: stat %q: %w", sourcePath, err)
	}
	name := sourcePath
	if idx := strings.LastIndexByte(sourcePath, '/'); idx >= 0 {
		name = sourcePath[idx+1:]
	}
	destPath := destinationDirectory
	if destinationDirectory != "/" {
		destPath += "/"
	}
	destPath += name
	destAbs, err := m.localPath(destPath)
	if err != nil {
		return cloud.TransferResult{}, err
	}
	if srcInfo.IsDir() {
		if err := copyDir(srcAbs, destAbs); err != nil {
			return cloud.TransferResult{}, err
		}
	} else {
		if err := copyFile(srcAbs, destAbs); err != nil {
			return cloud.TransferResult{}, err
		}
	}
	return cloud.TransferResult{
		SourcePath:           sourcePath,
		DestinationDirectory: destinationDirectory,
		FinalPath:            destPath,
		FinalName:            name,
	}, nil
}

func (m *MockProvider) UploadFile(_ context.Context, localPath, remotePath string, policy cloud.UploadConflictPolicy) (cloud.UploadResult, error) {
	f, err := os.Open(localPath)
	if err != nil {
		return cloud.UploadResult{}, fmt.Errorf("mock: open %q: %w", localPath, err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return cloud.UploadResult{}, err
	}
	if err := m.Upload(nil, remotePath, f); err != nil {
		return cloud.UploadResult{}, err
	}
	return cloud.UploadResult{
		RequestedPath: remotePath,
		FinalPath:     remotePath,
		FinalName:     remotePath[strings.LastIndexByte(remotePath, '/')+1:],
		Size:          info.Size(),
	}, nil
}

func (m *MockProvider) UploadDirectory(_ context.Context, localDirectory, remoteParent string, policy cloud.DirectoryUploadConflictPolicy) (cloud.UploadResult, error) {
	dirName := filepath.Base(localDirectory)
	remoteDir := remoteParent
	if remoteParent != "/" {
		remoteDir += "/"
	}
	remoteDir += dirName
	if err := m.Mkdir(nil, remoteDir); err != nil {
		return cloud.UploadResult{}, err
	}
	entries, err := os.ReadDir(localDirectory)
	if err != nil {
		return cloud.UploadResult{}, fmt.Errorf("mock: readdir %q: %w", localDirectory, err)
	}
	for _, e := range entries {
		entryLocal := filepath.Join(localDirectory, e.Name())
		entryRemote := remoteDir + "/" + e.Name()
		if e.IsDir() {
			if _, err := m.UploadDirectory(nil, entryLocal, remoteDir, policy); err != nil {
				return cloud.UploadResult{}, err
			}
		} else {
			if _, err := m.UploadFile(nil, entryLocal, entryRemote, cloud.UploadConflictFail); err != nil {
				return cloud.UploadResult{}, err
			}
		}
	}
	return cloud.UploadResult{
		RequestedPath: remoteParent + "/" + dirName,
		FinalPath:     remoteDir,
		FinalName:     dirName,
	}, nil
}

func (m *MockProvider) Rename(_ context.Context, oldPath, newPath string) error {
	oldAbs, err := m.localPath(oldPath)
	if err != nil {
		return err
	}
	newAbs, err := m.localPath(newPath)
	if err != nil {
		return err
	}

	if _, err := os.Stat(oldAbs); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("mock: not found: %q", oldPath)
		}
		return fmt.Errorf("mock: stat %q: %w", oldPath, err)
	}

	// Ensure parent of destination exists.
	dir := filepath.Dir(newAbs)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("mock: mkdir parent %q: %w", newPath, err)
	}

	if err := os.Rename(oldAbs, newAbs); err != nil {
		return fmt.Errorf("mock: rename %q -> %q: %w", oldPath, newPath, err)
	}
	return nil
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

func copyDir(src, dst string) error {
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		srcChild := filepath.Join(src, e.Name())
		dstChild := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyDir(srcChild, dstChild); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcChild, dstChild); err != nil {
				return err
			}
		}
	}
	return nil
}

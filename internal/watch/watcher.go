// Package watch provides poll-based directory monitoring with debouncing.
package watch

import (
	"context"
	"io/fs"
	"path/filepath"
	"strings"
	"sync"
	"time"

	securelog "ncepupan/hdd/internal/logging"
	"ncepupan/hdd/internal/store/sqlite"
)

const (
	DefaultPollInterval = 2 * time.Second
	DefaultDebounce     = 500 * time.Millisecond
)

// Watcher polls directories and emits file changes after debouncing.
type Watcher struct {
	store  *sqlite.Store
	roots  []syncRoot
	taskFn TaskFunc
	delFn  DeleteFunc

	poll     time.Duration
	debounce time.Duration

	mu       sync.Mutex
	lastSeen map[string]time.Time // path -> last mod time seen
	pending  map[string]time.Time // path -> latest change time (for debounce)

	ctx    context.Context
	cancel context.CancelFunc
}

type syncRoot struct {
	LocalPath  string
	RemotePath string
}

// TaskFunc is called for each file that should be uploaded.
type TaskFunc func(localPath, remotePath string)

// DeleteFunc is called for each file that was deleted locally and
// should be removed from the remote side.
type DeleteFunc func(localPath, remotePath string)

// New creates a Watcher.
func New(store *sqlite.Store, taskFn TaskFunc) *Watcher {
	return &Watcher{
		store:    store,
		taskFn:   taskFn,
		poll:     DefaultPollInterval,
		debounce: DefaultDebounce,
		lastSeen: make(map[string]time.Time),
		pending:  make(map[string]time.Time),
	}
}

// SetDeleteFunc sets the callback for detected local deletions.
func (w *Watcher) SetDeleteFunc(fn DeleteFunc) { w.delFn = fn }

// SetPollInterval overrides the poll interval (for tests).
func (w *Watcher) SetPollInterval(d time.Duration) { w.poll = d }

// SetDebounce overrides the debounce window (for tests).
func (w *Watcher) SetDebounce(d time.Duration) { w.debounce = d }

// AddRoot adds a directory to watch.
func (w *Watcher) AddRoot(localPath, remotePath string) {
	w.roots = append(w.roots, syncRoot{
		LocalPath:  localPath,
		RemotePath: remotePath,
	})
}

// Start begins polling.  Blocks until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) {
	w.ctx, w.cancel = context.WithCancel(ctx)
	defer w.cancel()

	ticker := time.NewTicker(w.poll)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			return
		case <-ticker.C:
			w.scan()
		}
	}
}

// Stop cancels the watcher context.
func (w *Watcher) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
}

// scan walks all roots and detects changes.
func (w *Watcher) scan() {
	now := time.Now()

	for _, root := range w.roots {
		seenThisScan := make(map[string]bool)

		filepath.WalkDir(root.LocalPath, func(localPath string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}

			info, err := d.Info()
			if err != nil {
				return nil
			}

			seenThisScan[localPath] = true
			modTime := info.ModTime()
			w.mu.Lock()
			prev, seen := w.lastSeen[localPath]
			if !seen || modTime.After(prev) {
				w.lastSeen[localPath] = modTime
				w.pending[localPath] = now
			}
			w.mu.Unlock()

			return nil
		})

		w.mu.Lock()
		// Emit stable pending files.
		for path, since := range w.pending {
			if now.Sub(since) >= w.debounce {
				remotePath := w.remotePathFor(root, path)
				securelog.LogSecurityEvent(w.ctx, securelog.SecurityEvent{Operation: "watch_change", Path: path})
				if w.taskFn != nil {
					w.taskFn(path, remotePath)
				}
				delete(w.pending, path)
			}
		}

		// Detect deletions: files in lastSeen that were not found in this scan.
		for path := range w.lastSeen {
			if !strings.HasPrefix(path, root.LocalPath+string(filepath.Separator)) {
				continue
			}
			if seenThisScan[path] {
				continue
			}
			remotePath := w.remotePathFor(root, path)
			securelog.LogSecurityEvent(w.ctx, securelog.SecurityEvent{Operation: "watch_delete", Path: path})
			if w.delFn != nil {
				w.delFn(path, remotePath)
			}
			delete(w.lastSeen, path)
			delete(w.pending, path)
		}
		w.mu.Unlock()
	}
}

func (w *Watcher) remotePathFor(root syncRoot, localPath string) string {
	rel, err := filepath.Rel(root.LocalPath, localPath)
	if err != nil {
		return filepath.ToSlash(localPath)
	}
	return filepath.ToSlash(filepath.Join(root.RemotePath, rel))
}

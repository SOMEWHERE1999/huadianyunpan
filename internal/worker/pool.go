package worker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ncepupan/hdd/internal/cloud"
	"ncepupan/hdd/internal/cloud/huadian"
	"ncepupan/hdd/internal/filter"
	securelog "ncepupan/hdd/internal/logging"
	"ncepupan/hdd/internal/store/sqlite"
)

const (
	DefaultPollInterval = 2 * time.Second
	MaxBackoff          = 10 * time.Minute
	MaxRetries          = 8
)

// taskTypes lists the SQLite operations polled by the worker pool.
var taskTypes = []string{"upload", "download", "remove"}

type Pool struct {
	store    *sqlite.Store
	provider cloud.Provider
	filter   *filter.Filter

	workers int
	poll    time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	inFlight   map[string]bool
	inFlightMu sync.Mutex

	pathLocks    map[string]*sync.Mutex
	pathLocksMu  sync.Mutex
	authMu       sync.RWMutex
	authBlocked  bool
	completeTask func(int64) error
}

func NewPool(store *sqlite.Store, provider cloud.Provider, workers int, f *filter.Filter) *Pool {
	if workers < 1 {
		workers = 1
	}
	if f == nil {
		f = filter.New(nil)
	}
	return &Pool{
		store:     store,
		provider:  provider,
		filter:    f,
		workers:   workers,
		poll:      DefaultPollInterval,
		inFlight:  make(map[string]bool),
		pathLocks: make(map[string]*sync.Mutex), completeTask: store.CompleteTask,
	}
}

func (p *Pool) SetPollInterval(d time.Duration) { p.poll = d }

func (p *Pool) Start(parent context.Context) {
	p.ctx, p.cancel = context.WithCancel(parent)
	if _, err := p.store.ResumeBlockedAuth(); err != nil {
		securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_resume_auth", ErrorClass: "store"})
	}

	// Stale running tasks are recovered by Store.Open during startup.
	// Log a single summary line for observability.
	n, err := p.store.ListPendingTasks("upload", 0)
	if err == nil {
		// Count pending tasks across types for a single log.
		total := len(n)
		for _, tt := range taskTypes[1:] {
			t, _ := p.store.ListPendingTasks(tt, 0)
			total += len(t)
		}
		if total > 0 {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_start", Path: fmt.Sprintf("pending_tasks=%d", total)})
		}
	}

	for i := 0; i < p.workers; i++ {
		for _, taskType := range taskTypes {
			p.wg.Add(1)
			go p.pollLoop(taskType)
		}
	}
}

func (p *Pool) Shutdown() {
	if p.cancel != nil {
		p.cancel()
	}
	p.wg.Wait()
}

func (p *Pool) pollLoop(taskType string) {
	defer p.wg.Done()
	ticker := time.NewTicker(p.poll)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}
		p.authMu.RLock()
		blocked := p.authBlocked
		p.authMu.RUnlock()
		if blocked {
			continue
		}

		tasks, err := p.store.ListPendingTasks(taskType, 10)
		if err != nil {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_poll", ErrorClass: "store"})
			continue
		}
		for i := range tasks {
			select {
			case <-p.ctx.Done():
				return
			default:
			}
			p.authMu.RLock()
			blocked = p.authBlocked
			p.authMu.RUnlock()
			if blocked {
				break
			}
			p.processTask(taskType, &tasks[i])
		}
	}
}

func (p *Pool) processTask(taskType string, task *sqlite.TaskRow) {
	key := taskKey(taskType, task.LocalPath)
	if !p.markInFlight(key) {
		return
	}
	defer p.unmarkInFlight(key)

	claimed, err := p.store.ClaimTask(task.ID)
	if err != nil || !claimed {
		return
	}

	lockPath := ""
	if task.LocalPath != nil {
		lockPath = *task.LocalPath
	} else if task.RemotePath != nil {
		lockPath = *task.RemotePath
	}
	if lockPath != "" {
		mu := p.getPathLock(lockPath)
		mu.Lock()
		defer mu.Unlock()
	}

	if task.LocalPath != nil && p.filter.ExcludePath(*task.LocalPath) {
		securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_filtered", Path: *task.LocalPath})
		if err := p.store.MarkCancelled(task.ID, "filtered", "filtered by synchronization rules"); err != nil {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_cancel", ErrorClass: "store"})
		}
		return
	}

	var fn func(context.Context, *sqlite.TaskRow) error
	switch taskType {
	case "upload":
		fn = p.processUpload
	case "download":
		fn = p.processDownload
	case "remove":
		fn = p.processDelete
	default:
		return
	}

	startTime := time.Now()
	err = fn(p.ctx, task)
	duration := time.Since(startTime)

	if err == nil {
		if delErr := p.completeTask(task.ID); delErr != nil {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_complete_task", ErrorClass: "store"})
			_ = p.store.MarkNeedsReconcile(task.ID, "remote operation succeeded but task commit failed")
		}
		return
	}

	errorClass := classifyError(err)
	retryCount := task.RetryCount + 1
	lastErr := safeErrMsg(err)
	if errorClass == "session_expired" {
		if markErr := p.store.MarkBlockedAuth(task.ID, lastErr); markErr != nil {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_block_auth", ErrorClass: "store"})
		}
		p.authMu.Lock()
		first := !p.authBlocked
		p.authBlocked = true
		p.authMu.Unlock()
		if first {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "daemon_state", ErrorClass: "session_expired", Path: "state=blocked_auth reason=session_expired"})
		}
		return
	}
	if errorClass == "local_file_disappeared" {
		if markErr := p.store.MarkCancelled(task.ID, errorClass, lastErr); markErr != nil {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_cancel", ErrorClass: "store"})
		}
		return
	}

	if permanentError(err) {
		securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{
			Operation:  "worker_task",
			ErrorClass: errorClass,
			Path: fmt.Sprintf("task_id=%d type=%s permanent=%v duration=%s",
				task.ID, taskType, true, duration.Round(time.Millisecond)),
		})
		if deadErr := p.store.MarkFailed(task.ID, errorClass, lastErr); deadErr != nil {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_mark_dead", ErrorClass: "store"})
		}
		return
	}

	if retryCount > MaxRetries {
		securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{
			Operation:  "worker_task",
			ErrorClass: "retries_exhausted",
			Path: fmt.Sprintf("task_id=%d type=%s attempt=%d/%d err=%s duration=%s",
				task.ID, taskType, retryCount, MaxRetries, errorClass, duration.Round(time.Millisecond)),
		})
		if deadErr := p.store.MarkFailed(task.ID, "retries_exhausted", lastErr); deadErr != nil {
			securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_mark_dead", ErrorClass: "store"})
		}
		return
	}

	nextRetry := time.Now().Unix() + int64(backoffDuration(retryCount).Seconds())
	if err := p.store.MarkRetry(task.ID, retryCount, nextRetry, errorClass, lastErr); err != nil {
		securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{Operation: "worker_update_state", ErrorClass: "store"})
	}

	securelog.LogSecurityEvent(p.ctx, securelog.SecurityEvent{
		Operation:  "worker_task",
		ErrorClass: errorClass,
		Path: fmt.Sprintf("task_id=%d type=%s attempt=%d next=%s duration=%s",
			task.ID, taskType, retryCount, time.Unix(nextRetry, 0).Format(time.RFC3339),
			duration.Round(time.Millisecond)),
	})
}

func (p *Pool) processUpload(ctx context.Context, task *sqlite.TaskRow) error {
	if task.LocalPath == nil || task.RemotePath == nil {
		return fmt.Errorf("upload task missing path")
	}
	localInfo, localErr := os.Stat(*task.LocalPath)
	if localErr != nil {
		return fmt.Errorf("open local file: %w", localErr)
	}
	remoteInfo, statErr := p.provider.Stat(ctx, *task.RemotePath)
	if statErr == nil {
		if remoteInfo.IsDir || remoteInfo.Size != localInfo.Size() {
			return fmt.Errorf("unsupported_overwrite: remote target exists with different metadata")
		}
		return nil
	}
	if classifyError(statErr) != "not_found" {
		return fmt.Errorf("upload preflight: %w", statErr)
	}
	// Ensure parent remote directory exists before uploading.
	if err := p.ensureRemoteDir(ctx, *task.RemotePath); err != nil {
		return fmt.Errorf("upload: ensure remote dir: %w", err)
	}

	// Record local file metadata before upload for post-upload verification.
	f, err := os.Open(*task.LocalPath)
	if err != nil {
		return fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()

	uploadErr := p.provider.Upload(ctx, *task.RemotePath, f)

	if uploadErr == nil {
		return nil // success path unchanged
	}
	if classifyError(uploadErr) == "session_expired" {
		return fmt.Errorf("upload: %w", uploadErr)
	}

	// Post-upload verification: if finalize returned an ambiguous result
	// (e.g. osendupload 404), check whether the file actually landed.
	errorClass := classifyError(uploadErr)
	if localErr == nil && errorClass != "invalid_task" && errorClass != "local_file_missing" {
		if info, statErr := p.provider.Stat(ctx, *task.RemotePath); statErr == nil && !info.IsDir {
			localSize := localInfo.Size()
			if info.Size == localSize {
				fmt.Fprintf(os.Stderr, "[worker] upload task_id=%d remote_verified: size=%d matches, treating as succeeded\n",
					task.ID, localSize)
				return nil
			}
			return fmt.Errorf("upload_finalize_ambiguous remote_size=%d local_size=%d: %w", info.Size, localSize, uploadErr)
		}
		// File not found on remote after failed upload — genuine failure.
		return fmt.Errorf("upload_finalize_failed remote_not_found: %w", uploadErr)
	}

	return fmt.Errorf("upload: %w", uploadErr)
}

// ensureRemoteDir ensures the parent directory of remotePath exists,
// creating intermediate directories as needed.
func (p *Pool) ensureRemoteDir(ctx context.Context, remotePath string) error {
	parent := remoteDir(remotePath)
	if parent == "/" || parent == "" {
		return nil
	}
	// Stat the parent — if it exists and is a directory, we're done.
	info, err := p.provider.Stat(ctx, parent)
	if err == nil && info.IsDir {
		return nil
	}
	if err == nil && !info.IsDir {
		return fmt.Errorf("path %q is a file, not a directory", parent)
	}
	// Try to create it. If it already exists (race), Mkdir may return
	// an error; Stat-check and accept "already exists" silently.
	if mkdirErr := p.provider.Mkdir(ctx, parent); mkdirErr != nil {
		// Recursively ensure the grandparent first, then retry.
		if gp := remoteDir(parent); gp != "/" && gp != "" {
			if ensureErr := p.ensureRemoteDir(ctx, parent); ensureErr != nil {
				return ensureErr
			}
		}
		// Retry mkdir after parent is created.
		if mkdirErr2 := p.provider.Mkdir(ctx, parent); mkdirErr2 != nil {
			// Check if it now exists (race with another worker).
			info2, statErr := p.provider.Stat(ctx, parent)
			if statErr == nil && info2.IsDir {
				return nil
			}
			return mkdirErr2
		}
	}
	return nil
}

func remoteDir(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "/"
	}
	idx := strings.LastIndexByte(p, '/')
	if idx <= 0 {
		return "/"
	}
	return p[:idx]
}

func (p *Pool) processDownload(ctx context.Context, task *sqlite.TaskRow) error {
	if task.LocalPath == nil || task.RemotePath == nil {
		return fmt.Errorf("download task missing path")
	}
	dir := filepath.Dir(*task.LocalPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create download parent dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".hdddl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	closed := false
	defer func() {
		if !closed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if err := p.provider.Download(ctx, *task.RemotePath, tmp); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp: %w", err)
	}
	closed = true
	if err := os.Rename(tmpName, *task.LocalPath); err != nil {
		return fmt.Errorf("rename temp: %w", err)
	}
	return nil
}

func (p *Pool) processDelete(ctx context.Context, task *sqlite.TaskRow) error {
	if task.RemotePath == nil {
		return fmt.Errorf("delete task missing remote path")
	}
	if err := p.provider.Remove(ctx, *task.RemotePath); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

func taskKey(taskType string, localPath *string) string {
	lp := ""
	if localPath != nil {
		lp = *localPath
	}
	return taskType + ":" + lp
}

func (p *Pool) markInFlight(key string) bool {
	p.inFlightMu.Lock()
	defer p.inFlightMu.Unlock()
	if p.inFlight[key] {
		return false
	}
	p.inFlight[key] = true
	return true
}

func (p *Pool) unmarkInFlight(key string) {
	p.inFlightMu.Lock()
	defer p.inFlightMu.Unlock()
	delete(p.inFlight, key)
}

func (p *Pool) getPathLock(localPath string) *sync.Mutex {
	p.pathLocksMu.Lock()
	defer p.pathLocksMu.Unlock()
	if mu, ok := p.pathLocks[localPath]; ok {
		return mu
	}
	mu := new(sync.Mutex)
	p.pathLocks[localPath] = mu
	return mu
}

func classifyError(err error) string {
	if err == nil {
		return "success"
	}
	msg := err.Error()
	if strings.Contains(msg, "upload_finalize_ambiguous") {
		return "upload_finalize_ambiguous"
	}
	if strings.Contains(msg, "upload_finalize_failed") {
		return "upload_finalize_failed"
	}
	if strings.Contains(msg, "unsupported_overwrite") {
		return "unsupported_overwrite"
	}
	if strings.Contains(msg, "missing path") || strings.Contains(msg, "missing local_path") || strings.Contains(msg, "missing remote_path") {
		return "invalid_task"
	}
	if errors.Is(err, huadian.ErrUnauthorized) {
		return "session_expired"
	}
	if errors.Is(err, huadian.ErrNotFound) {
		return "not_found"
	}
	if strings.Contains(strings.ToLower(msg), "not found") {
		return "not_found"
	}
	if os.IsNotExist(err) {
		return "local_file_disappeared"
	}
	if strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline") {
		return "network_timeout"
	}
	if strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") {
		return "network_unavailable"
	}
	if strings.Contains(msg, "permission") || strings.Contains(msg, "access is denied") {
		return "permission_denied"
	}
	return "task_failed"
}

// permanentError is true when the error should not be retried.
func permanentError(err error) bool {
	if err == nil {
		return false
	}
	e := classifyError(err)
	switch e {
	case "invalid_task", "unsupported_overwrite":
		return true
	}
	return false
}

func safeErrMsg(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 256 {
		msg = msg[:253] + "..."
	}
	for _, secret := range []string{"Authorization:", "ory_at_", "Signature=", "authrequest"} {
		if idx := strings.Index(msg, secret); idx >= 0 {
			msg = msg[:idx] + secret + "***"
			break
		}
	}
	return msg
}

func backoffDuration(n int64) time.Duration {
	secs := math.Pow(2, float64(n-1))
	d := time.Duration(secs) * time.Second
	if d > MaxBackoff {
		d = MaxBackoff
	}
	return d
}

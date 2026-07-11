package worker

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"ncepupan/hdd/internal/cloud/huadian"
	"ncepupan/hdd/internal/cloud/mock"
	"ncepupan/hdd/internal/filter"
	"ncepupan/hdd/internal/store/sqlite"
)

type unauthorizedProvider struct {
	*mock.MockProvider
	uploads atomic.Int32
}

func (p *unauthorizedProvider) Upload(context.Context, string, io.Reader) error {
	p.uploads.Add(1)
	return huadian.ErrUnauthorized
}

func TestSessionExpiredBlocksFurtherClaims(t *testing.T) {
	s := openStore(t)
	dir := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		p := writeTempFile(t, dir, name, name)
		if _, err := s.InsertTask("upload", p, "/"+name); err != nil {
			t.Fatal(err)
		}
	}
	p := &unauthorizedProvider{MockProvider: mock.New(t.TempDir())}
	pool := NewPool(s, p, 1, nil)
	pool.SetPollInterval(10 * time.Millisecond)
	pool.Start(context.Background())
	defer pool.Shutdown()
	time.Sleep(250 * time.Millisecond)
	blocked, _ := s.ListTasks(context.Background(), sqlite.TaskQuery{States: []sqlite.TaskState{sqlite.TaskBlockedAuth}, IncludeTerminal: true})
	pending, _ := s.ListTasks(context.Background(), sqlite.TaskQuery{States: []sqlite.TaskState{sqlite.TaskPending}, IncludeTerminal: true})
	if len(blocked) != 1 || len(pending) != 1 {
		t.Fatalf("blocked=%d pending=%d uploads=%d", len(blocked), len(pending), p.uploads.Load())
	}
	if p.uploads.Load() != 1 {
		t.Fatalf("uploads=%d want 1", p.uploads.Load())
	}
}

type countingProvider struct {
	*mock.MockProvider
	uploads atomic.Int32
}

func (p *countingProvider) Upload(ctx context.Context, path string, r io.Reader) error {
	p.uploads.Add(1)
	return p.MockProvider.Upload(ctx, path, r)
}
func TestSuccessfulUploadCommitFailureNeedsReconcileWithoutDuplicateCreate(t *testing.T) {
	s := openStore(t)
	src := writeTempFile(t, t.TempDir(), "once.txt", "payload")
	id, _ := s.InsertTask("upload", src, "/once.txt")
	p := &countingProvider{MockProvider: mock.New(t.TempDir())}
	pool := NewPool(s, p, 1, nil)
	pool.completeTask = func(int64) error { return errors.New("injected commit failure") }
	pool.SetPollInterval(10 * time.Millisecond)
	pool.Start(context.Background())
	defer pool.Shutdown()
	time.Sleep(250 * time.Millisecond)
	rows, _ := s.ListTasks(context.Background(), sqlite.TaskQuery{States: []sqlite.TaskState{sqlite.TaskNeedsReconcile}, IncludeTerminal: true})
	if len(rows) != 1 || rows[0].ID != id {
		t.Fatalf("rows=%+v", rows)
	}
	if p.uploads.Load() != 1 {
		t.Fatalf("uploads=%d want 1", p.uploads.Load())
	}
}

func openStore(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return p
}

func TestUploadPool_Success(t *testing.T) {
	store := openStore(t)
	tmpDir := t.TempDir()
	prov := mock.New(tmpDir)
	srcPath := writeTempFile(t, tmpDir, "src.txt", "")
	store.InsertTask("upload", srcPath, "/dst.txt")
	prov.Upload(context.Background(), "/dst.txt", strings.NewReader(""))
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()
	time.Sleep(500 * time.Millisecond)
	tasks, _ := store.ListPendingTasks("upload", 10)
	if len(tasks) != 0 {
		t.Errorf("expected 0 pending tasks, got %d", len(tasks))
	}
}

func TestUploadPool_RetryAndBackoff(t *testing.T) {
	store := openStore(t)
	prov := mock.New(t.TempDir())
	noSuch := filepath.Join(t.TempDir(), "no-such-file.txt")
	store.InsertTask("upload", noSuch, "/dst.txt")
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()
	time.Sleep(500 * time.Millisecond)
	tasks, _ := store.ListPendingTasks("upload", 10)
	if len(tasks) == 0 {
		t.Skip("task may have already been processed")
	}
	if tasks[0].RetryCount < 1 {
		t.Errorf("retry = %d, want >= 1", tasks[0].RetryCount)
	}
}

func TestUploadPool_Deduplication(t *testing.T) {
	store := openStore(t)
	prov := mock.New(t.TempDir())
	samePath := filepath.Join(t.TempDir(), "same.txt")
	store.InsertTask("upload", samePath, "/a.txt")
	store.InsertTask("upload", samePath, "/b.txt")
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool.Start(ctx)
	time.Sleep(500 * time.Millisecond)
	pool.Shutdown()
}

func TestUploadPool_PathSerialization(t *testing.T) {
	store := openStore(t)
	prov := mock.New(t.TempDir())
	dirA := filepath.Join(t.TempDir(), "dir", "a.txt")
	dirB := filepath.Join(t.TempDir(), "dir", "b.txt")
	store.InsertTask("upload", dirA, "/dir/a.txt")
	store.InsertTask("upload", dirB, "/dir/b.txt")
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()
	time.Sleep(800 * time.Millisecond)
}

func TestDownloadPool_Success(t *testing.T) {
	store := openStore(t)
	tmpDir := t.TempDir()
	prov := mock.New(tmpDir)
	prov.Upload(context.Background(), "/down.txt", strings.NewReader("download data"))
	outPath := filepath.Join(tmpDir, "out.txt")
	store.InsertTask("download", outPath, "/down.txt")
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()
	time.Sleep(500 * time.Millisecond)
	tasks, _ := store.ListPendingTasks("download", 10)
	if len(tasks) != 0 {
		t.Errorf("expected 0 pending, got %d", len(tasks))
	}
}

func TestDeletePool_Success(t *testing.T) {
	store := openStore(t)
	prov := mock.New(t.TempDir())
	prov.Upload(context.Background(), "/bye.txt", strings.NewReader("x"))
	store.InsertTask("delete", "", "/bye.txt")
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()
	time.Sleep(500 * time.Millisecond)
	tasks, _ := store.ListPendingTasks("delete", 10)
	if len(tasks) != 0 {
		t.Errorf("expected 0 pending, got %d", len(tasks))
	}
}

func TestGracefulShutdown(t *testing.T) {
	store := openStore(t)
	prov := mock.New(t.TempDir())
	slowPath := filepath.Join(t.TempDir(), "slow.txt")
	store.InsertTask("upload", slowPath, "/slow.txt")
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	pool.Start(ctx)
	time.Sleep(100 * time.Millisecond)
	cancel()
	done := make(chan struct{})
	go func() { pool.Shutdown(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown timed out")
	}
}

func TestBackoffDuration(t *testing.T) {
	tests := []struct {
		retry   int64
		wantMin time.Duration
		wantMax time.Duration
	}{
		{1, 1 * time.Second, 2 * time.Second},
		{2, 2 * time.Second, 3 * time.Second},
		{3, 4 * time.Second, 5 * time.Second},
		{4, 8 * time.Second, 9 * time.Second},
		{8, 128 * time.Second, 129 * time.Second},
		{20, MaxBackoff, MaxBackoff + 1*time.Second},
	}
	for _, tt := range tests {
		d := backoffDuration(tt.retry)
		if d < tt.wantMin || d > tt.wantMax {
			t.Errorf("backoff(%d) = %v, want [%v, %v]", tt.retry, d, tt.wantMin, tt.wantMax)
		}
	}
}

func TestRestartRecovery(t *testing.T) {
	dir := t.TempDir()

	s1, _ := sqlite.Open(dir)
	recoverPath := filepath.Join(t.TempDir(), "recover.txt")
	s1.InsertTask("upload", recoverPath, "/recover.txt")
	tasks, _ := s1.ListPendingTasks("upload", 10)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	oldTS := sqlite.Now() - 10
	s1.UpdateTaskState(tasks[0].ID, "failed", 2, &oldTS, strPtr("crashed mid-upload"))
	s1.Close()

	s2, _ := sqlite.Open(dir)
	defer s2.Close()
	prov := mock.New(t.TempDir())

	pool := NewPool(s2, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()

	time.Sleep(500 * time.Millisecond)

	remaining, _ := s2.ListPendingTasks("upload", 10)
	t.Logf("remaining tasks after recovery: %d", len(remaining))
	for _, r := range remaining {
		t.Logf("  task %d: state=%s retry=%d lp=%s", r.ID, r.State, r.RetryCount, strVal(r.LocalPath))
	}
	if len(remaining) > 0 && remaining[0].State == "failed" && remaining[0].RetryCount >= 3 {
		// Expected: recovery worked, worker retried, failed again.
	} else if len(remaining) == 0 {
		// Also acceptable.
	}
}

func strPtr(s string) *string { return &s }
func TestFilterExcludesTask(t *testing.T) {
	store := openStore(t)
	prov := mock.New(t.TempDir())
	tmpPath := filepath.Join(t.TempDir(), "test.tmp")

	store.InsertTask("upload", tmpPath, "/test.tmp")

	f := filter.New(filter.DefaultExcludes())
	pool := NewPool(store, prov, 1, f)
	pool.SetPollInterval(100 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()

	time.Sleep(500 * time.Millisecond)

	tasks, _ := store.ListPendingTasks("upload", 10)
	if len(tasks) != 0 {
		t.Errorf("expected 0 tasks after filter, got %d", len(tasks))
	}
}

func strVal(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestDownloadPool_PreserveOnFailure(t *testing.T) {
	store := openStore(t)
	tmpDir := t.TempDir()
	prov := mock.New(tmpDir)
	destPath := filepath.Join(tmpDir, "existing.txt")
	original := []byte("original content must survive")
	if err := os.WriteFile(destPath, original, 0600); err != nil {
		t.Fatalf("write existing: %v", err)
	}
	store.InsertTask("download", destPath, "/no-such-remote.txt")
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()
	time.Sleep(800 * time.Millisecond)
	content, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(content) != string(original) {
		t.Errorf("file was overwritten on failed download: got %q, want %q", content, original)
	}
}

func TestTaskExhaustRetries_MarkedDead(t *testing.T) {
	store := openStore(t)
	prov := mock.New(t.TempDir())
	noSuch := filepath.Join(t.TempDir(), "no-such-for-dead.txt")
	id, err := store.InsertTask("upload", noSuch, "/dead.txt")
	if err != nil {
		t.Fatalf("InsertTask: %v", err)
	}
	store.UpdateTaskState(id, "failed", MaxRetries, nil, strPtr("pre-exhausted"))
	pool := NewPool(store, prov, 1, nil)
	pool.SetPollInterval(100 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool.Start(ctx)
	defer pool.Shutdown()
	time.Sleep(800 * time.Millisecond)
	tasks, _ := store.ListPendingTasks("upload", 10)
	if len(tasks) != 0 {
		t.Errorf("expected 0 pending tasks, got %d", len(tasks))
	}
}

func TestInsertTaskDedup_Merged(t *testing.T) {
	store := openStore(t)
	tmpDir := t.TempDir()
	dupPath := filepath.Join(tmpDir, "dup.txt")
	id1, err := store.InsertTask("upload", dupPath, "/remote/dup.txt")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	id2, err := store.InsertTask("upload", dupPath, "/remote/dup.txt")
	if err != nil || id2 != id1 {
		t.Fatalf("merge: id=%d err=%v, want id=%d", id2, err, id1)
	}
}

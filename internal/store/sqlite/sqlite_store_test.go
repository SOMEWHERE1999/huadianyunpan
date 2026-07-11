package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestTaskLifecycleAndUnifiedQuery(t *testing.T) {
	s := openTest(t)
	id, err := s.EnqueueOrMerge(context.Background(), 7, "upload", `D:\同步 目录\报告.txt`, `//课程/./资料/../报告.txt`)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimTask(id); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	running, err := s.ListTasks(context.Background(), TaskQuery{States: []TaskState{TaskRunning}, IncludeTerminal: true})
	if err != nil || len(running) != 1 {
		t.Fatalf("running=%v err=%v", running, err)
	}
	if err := s.CompleteTask(id); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListTasks(context.Background(), TaskQuery{IncludeTerminal: true})
	if err != nil || len(all) != 1 || all[0].Status != "succeeded" || all[0].CompletedAt == nil {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	active, err := s.ListTasks(context.Background(), TaskQuery{})
	if err != nil || len(active) != 0 {
		t.Fatalf("active=%v err=%v", active, err)
	}
}

func TestConcurrentEnqueueOrMergeOneActiveTask(t *testing.T) {
	s := openTest(t)
	const n = 24
	ids := make(chan int64, n)
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, e := s.EnqueueOrMerge(context.Background(), 1, "upload", `D:\根\second.txt`, `/目录//second.txt`)
			ids <- id
			errs <- e
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	var first int64
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatalf("ids differ: %d %d", first, id)
		}
	}
	rows, err := s.ListTasks(context.Background(), TaskQuery{States: []TaskState{TaskPending}, IncludeTerminal: true})
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
}

func TestCancelDeletedLocalTask(t *testing.T) {
	s := openTest(t)
	p := `D:\根\新建 文本文档.txt`
	id, _ := s.InsertTask("upload", p, "/新建 文本文档.txt")
	n, err := s.CancelActiveByLocalPath(context.Background(), p, "local_file_disappeared")
	if err != nil || n != 1 {
		t.Fatalf("cancel=%d err=%v", n, err)
	}
	rows, _ := s.ListTasks(context.Background(), TaskQuery{States: []TaskState{TaskCancelled}, IncludeTerminal: true})
	if len(rows) != 1 || rows[0].ID != id || rows[0].ErrorClass == nil {
		t.Fatalf("rows=%+v", rows)
	}
}

func TestMigrationInitializesAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 2; i++ {
		s, err := Open(dir)
		if err != nil {
			t.Fatalf("Open pass %d: %v", i, err)
		}
		var versions int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&versions); err != nil {
			t.Fatal(err)
		}
		if versions != 2 {
			t.Fatalf("migration count = %d, want 2", versions)
		}
		for _, table := range []string{"files", "tasks", "sync_roots", "conflicts", "settings"} {
			var count int
			if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&count); err != nil || count != 1 {
				t.Fatalf("table %s missing: count=%d err=%v", table, count, err)
			}
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentClaimOnlyOneWinner(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertTask("upload", filepath.Join(t.TempDir(), "same.txt"), "/same.txt")
	if err != nil {
		t.Fatal(err)
	}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := s.ClaimTask(id)
			if err != nil {
				t.Errorf("ClaimTask: %v", err)
				return
			}
			if ok {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatalf("claim winners = %d, want 1", winners.Load())
	}
}

func TestSameCanonicalPathSerialized(t *testing.T) {
	s := openTest(t)
	path := filepath.Join(t.TempDir(), "Case Name 中文.txt")
	first, err := s.InsertTask("upload", path, "/first.txt")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.InsertTask("rename", path, "/second.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimTask(first); err != nil || !ok {
		t.Fatalf("first claim: ok=%v err=%v", ok, err)
	}
	if ok, err := s.ClaimTask(second); err != nil || ok {
		t.Fatalf("second claim while path busy: ok=%v err=%v", ok, err)
	}
	if err := s.CompleteTask(first); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimTask(second); err != nil || !ok {
		t.Fatalf("second claim after completion: ok=%v err=%v", ok, err)
	}
}

func TestCompleteTaskWithFileRollsBackOnFailure(t *testing.T) {
	s := openTest(t)
	id, _ := s.InsertTask("upload", filepath.Join(t.TempDir(), "rollback.txt"), "/rollback.txt")
	if ok, err := s.ClaimTask(id); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	err := s.CompleteTaskWithFile(id, &FileRow{SyncRootID: 999999, LocalPath: "rollback.txt", RemotePath: "/rollback.txt", SyncStatus: "synced"})
	if err == nil {
		t.Fatal("expected foreign-key failure")
	}
	var status string
	if err := s.db.QueryRow("SELECT status FROM tasks WHERE id=?", id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("status after rollback = %q, want running", status)
	}
	var files int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM files").Scan(&files); err != nil || files != 0 {
		t.Fatalf("files after rollback = %d, err=%v", files, err)
	}
}

func TestRetryWaitBecomesPendingWhenDue(t *testing.T) {
	s := openTest(t)
	id, _ := s.InsertTask("download", filepath.Join(t.TempDir(), "retry.txt"), "/retry.txt")
	past := Now() - 1
	message := "network unavailable"
	if err := s.UpdateTaskState(id, "failed", 2, &past, &message); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListPendingTasks("download", 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("pending retry: len=%d err=%v", len(tasks), err)
	}
	if tasks[0].Status != "retry_wait" || tasks[0].Attempts != 2 {
		t.Fatalf("unexpected recovered task: %+v", tasks[0])
	}
}

func TestClosedDatabaseErrorsPropagate(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.InsertTask("upload", "closed.txt", "/closed.txt"); err == nil {
		t.Fatal("InsertTask after Close returned nil error")
	}
	if _, err := s.ListSyncRoots(); err == nil {
		t.Fatal("ListSyncRoots after Close returned nil error")
	}
}

func TestUnicodeSpaceAndRestartRecovery(t *testing.T) {
	dir := t.TempDir()
	local := filepath.Join(t.TempDir(), "课程 资料", "报告 终稿.txt")
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := s1.InsertTask("upload", local, "/课程 资料/报告 终稿.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	tasks, err := s2.ListPendingTasks("upload", 10)
	if err != nil || len(tasks) != 1 || tasks[0].ID != id {
		t.Fatalf("restart task recovery: tasks=%v err=%v", tasks, err)
	}
	if tasks[0].LocalPath == nil || *tasks[0].LocalPath != local {
		t.Fatalf("local path changed: %+v", tasks[0].LocalPath)
	}
}

func TestAllTaskOperationsAccepted(t *testing.T) {
	s := openTest(t)
	for i, op := range []string{"upload", "download", "mkdir", "remove", "rename"} {
		if _, err := s.InsertTask(op, fmt.Sprintf("path-%d", i), fmt.Sprintf("/destination-%d", i)); err != nil {
			t.Fatalf("operation %s: %v", op, err)
		}
	}
}

func TestOpenRecoversStaleRunning(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := s1.InsertTask("upload", "stale.txt", "/stale.txt")
	if ok, err := s1.ClaimTask(id); err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := s1.db.Exec("UPDATE tasks SET claimed_at=? WHERE id=?", time.Now().Add(-3*time.Minute).Unix(), id); err != nil {
		t.Fatal(err)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	tasks, err := s2.ListPendingTasks("upload", 10)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("stale recovery: len=%d err=%v", len(tasks), err)
	}
}

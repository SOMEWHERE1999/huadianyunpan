package sqlite

import (
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSyncRootsCRUD(t *testing.T) {
	s := openTest(t)
	id, err := s.InsertSyncRoot("C:\\data", "/data")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if id != 1 {
		t.Errorf("id = %d, want 1", id)
	}
	roots, err := s.ListSyncRoots()
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(roots) != 1 {
		t.Fatalf("len = %d, want 1", len(roots))
	}
	if !roots[0].Enabled {
		t.Error("expected enabled")
	}
	s.UpdateSyncRootEnabled(id, false)
	r, _ := s.GetSyncRoot(id)
	if r.Enabled {
		t.Error("expected disabled")
	}
	s.DeleteSyncRoot(id)
	roots, _ = s.ListSyncRoots()
	if len(roots) != 0 {
		t.Errorf("expected empty, got %d", len(roots))
	}
}

func TestSettings(t *testing.T) {
	s := openTest(t)
	s.SetSetting("theme", "dark")
	v, _ := s.GetSetting("theme")
	if v != "dark" {
		t.Errorf("value = %q, want dark", v)
	}
	s.SetSetting("theme", "light")
	v, _ = s.GetSetting("theme")
	if v != "light" {
		t.Errorf("value = %q, want light", v)
	}
	s.DeleteSetting("theme")
	_, err := s.GetSetting("theme")
	if err == nil {
		t.Error("expected error")
	}
}

func TestTasks(t *testing.T) {
	s := openTest(t)
	id, _ := s.InsertTask("upload", "C:\\a.txt", "/a.txt")
	tasks, _ := s.ListPendingTasks("upload", 10)
	if len(tasks) != 1 || tasks[0].State != "pending" {
		t.Fatalf("unexpected tasks: %+v", tasks)
	}
	s.UpdateTaskState(id, "running", 1, nil, nil)
	tasks, _ = s.ListPendingTasks("upload", 10)
	if len(tasks) != 0 {
		t.Errorf("expected 0 pending, got %d", len(tasks))
	}
}

func TestConflicts(t *testing.T) {
	s := openTest(t)
	id, _ := s.InsertConflict("C:\\a.txt", "/a.txt", "etag1", "etag2")
	conflicts, _ := s.ListConflicts()
	if len(conflicts) != 1 || conflicts[0].Resolution != "unresolved" {
		t.Fatalf("unexpected conflicts: %+v", conflicts)
	}
	s.ResolveConflict(id, "keep_local")
	conflicts, _ = s.ListConflicts()
	if len(conflicts) != 0 {
		t.Errorf("expected 0, got %d", len(conflicts))
	}
}

func TestFiles(t *testing.T) {
	s := openTest(t)
	rootID, _ := s.InsertSyncRoot("C:\\sync", "/sync")
	f := &FileRow{LocalPath: "C:\\sync\\doc.txt", RemotePath: "/sync/doc.txt", Size: 100, SyncStatus: "synced", SyncRootID: rootID}
	id, _ := s.UpsertFile(f)
	if id != 1 {
		t.Errorf("id = %d", id)
	}
	files, _ := s.ListFilesByRoot(rootID)
	if len(files) != 1 {
		t.Fatalf("len = %d", len(files))
	}
	s.DeleteFile(id)
	files, _ = s.ListFilesByRoot(rootID)
	if len(files) != 0 {
		t.Errorf("expected empty")
	}
}

func TestDataPersistence(t *testing.T) {
	dir := t.TempDir()
	s1, _ := Open(dir)
	s1.InsertSyncRoot("C:\\d", "/d")
	s1.Close()
	s2, _ := Open(dir)
	defer s2.Close()
	roots, _ := s2.ListSyncRoots()
	if len(roots) != 1 {
		t.Errorf("persistence failed: got %d roots", len(roots))
	}
}

func TestInsertTask_Dedup(t *testing.T) {
	s := openTest(t)
	id1, err := s.InsertTask("upload", "C:\\dup.txt", "/dup.txt")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if id1 == 0 {
		t.Fatal("expected non-zero id")
	}
	id2, err := s.InsertTask("upload", "C:\\dup.txt", "/dup.txt")
	if err != nil || id2 != id1 {
		t.Fatalf("merge: id=%d err=%v, want id=%d", id2, err, id1)
	}
}

func TestClaimTask(t *testing.T) {
	s := openTest(t)
	id, _ := s.InsertTask("upload", "C:\\x.txt", "/x.txt")
	claimed, err := s.ClaimTask(id)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if !claimed {
		t.Fatal("expected claim to succeed")
	}
	claimed, _ = s.ClaimTask(id)
	if claimed {
		t.Error("second claim should fail (already running)")
	}
}

func TestClaimTask_Nonexistent(t *testing.T) {
	s := openTest(t)
	claimed, err := s.ClaimTask(9999)
	if err == nil && claimed {
		t.Error("expected claim to fail for nonexistent task")
	}
}

func TestMarkTaskDead(t *testing.T) {
	s := openTest(t)
	id, _ := s.InsertTask("upload", "C:\\dead.txt", "/dead.txt")
	s.MarkTaskDead(id)
	tasks, _ := s.ListPendingTasks("upload", 10)
	if len(tasks) != 0 {
		t.Errorf("expected 0 pending tasks after mark dead, got %d", len(tasks))
	}
}

func TestListPendingTasks_RecoversStaleRunning(t *testing.T) {
	s := openTest(t)
	id, _ := s.InsertTask("upload", "C:\\stale.txt", "/stale.txt")
	s.ClaimTask(id)
	if _, err := s.db.Exec("UPDATE tasks SET claimed_at=? WHERE id=?", Now()-300, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecoverStaleTasks(time.Now().Add(-2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.ListPendingTasks("upload", 10)
	if len(tasks) != 1 {
		t.Errorf("expected 1 recovered task, got %d", len(tasks))
	}
	if tasks[0].State != "pending" {
		t.Errorf("expected pending after stale recovery, got %s", tasks[0].State)
	}
}

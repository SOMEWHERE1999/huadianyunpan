package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ncepupan/hdd/internal/store/sqlite"
)

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	callErr := fn()
	w.Close()
	os.Stdout = old
	b, readErr := io.ReadAll(r)
	r.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(b), callErr
}

func TestSyncTasksQueriesAllStatesAndVerbosePaths(t *testing.T) {
	base := t.TempDir()
	t.Setenv("LOCALAPPDATA", base)
	s, err := sqlite.Open(filepath.Join(base, "HuadianDrive"))
	if err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(`D:\同步 根`, `完整 文件名.txt`)
	success, _ := s.InsertTask("upload", local, "/远端/完整 文件名.txt")
	s.ClaimTask(success)
	s.CompleteTask(success)
	running, _ := s.InsertTask("upload", filepath.Join(base, "running.txt"), "/running.txt")
	s.ClaimTask(running)
	_, _ = s.InsertTask("upload", filepath.Join(base, "cancelled.txt"), "/cancelled.txt")
	s.CancelActiveByLocalPath(context.Background(), filepath.Join(base, "cancelled.txt"), "local_file_disappeared")
	s.Close()
	out, err := captureStdout(t, func() error { return cmdSyncTasks([]string{"--verbose", "--limit", "20"}) })
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"succeeded", "running", "cancelled", local, "/远端/完整 文件名.txt", "Error class"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	for _, state := range []string{"pending", "running", "retry_wait", "blocked_auth", "succeeded", "failed", "cancelled", "needs_reconcile"} {
		out, err = captureStdout(t, func() error { return cmdSyncTasks([]string{"--state", state}) })
		if err != nil {
			t.Fatalf("state %s: %v", state, err)
		}
		if state == "running" && !strings.Contains(out, "running") {
			t.Fatalf("running output: %s", out)
		}
		if state == "pending" && out != "No pending tasks.\r\n" && out != "No pending tasks.\n" {
			t.Fatalf("pending output: %q", out)
		}
	}
}

func TestSyncTasksRejectsInvalidStateAndLimit(t *testing.T) {
	if err := cmdSyncTasks([]string{"--state", "bogus"}); err == nil {
		t.Fatal("expected invalid state error")
	}
	if err := cmdSyncTasks([]string{"--limit", "0"}); err == nil {
		t.Fatal("expected invalid limit error")
	}
}

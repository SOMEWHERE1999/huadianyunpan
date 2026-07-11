package sync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ncepupan/hdd/internal/cloud/mock"
	"ncepupan/hdd/internal/domain"
)

func TestConflictName(t *testing.T) {
	tm := time.Date(2026, 6, 27, 15, 30, 0, 0, time.UTC)
	name := ConflictName("C:\\docs\\report.docx", "local", tm)
	if !strings.Contains(name, "report.conflict-local-20260627-153000.docx") {
		t.Errorf("conflict name = %q", name)
	}
}

func TestDiff_UploadNew(t *testing.T) {
	s := New(nil)
	local := map[string]time.Time{"C:\\a.txt": time.Now()}
	remote := map[string]domain.FileInfo{}

	actions := s.Diff(local, remote)
	if len(actions) != 1 || actions[0].Type != ActionUpload {
		t.Fatalf("expected upload, got %v", actions)
	}
}

func TestDiff_DownloadNew(t *testing.T) {
	s := New(nil)
	local := map[string]time.Time{}
	remote := map[string]domain.FileInfo{"/b.txt": {Path: "/b.txt", ModTime: time.Now()}}

	actions := s.Diff(local, remote)
	if len(actions) != 1 || actions[0].Type != ActionDownload {
		t.Fatalf("expected download, got %v", actions)
	}
}

func TestDiff_Conflict(t *testing.T) {
	now := time.Now()
	later := now.Add(time.Hour)

	s := New(nil)
	st := State{LocalModTime: now, RemoteModTime: now}
	s.LoadState("C:\\c.txt", st)

	local := map[string]time.Time{"C:\\c.txt": later}
	remote := map[string]domain.FileInfo{"/c.txt": {Path: "/c.txt", ModTime: later}}

	actions := s.Diff(local, remote)
	if len(actions) != 1 || actions[0].Type != ActionConflict {
		t.Fatalf("expected conflict, got %v", actions)
	}
}

func TestDiff_RemoteDelete(t *testing.T) {
	now := time.Now()
	s := New(nil)
	s.LoadState("C:\\d.txt", State{LocalModTime: now, RemoteModTime: now})

	local := map[string]time.Time{"C:\\d.txt": now}
	remote := map[string]domain.FileInfo{}

	actions := s.Diff(local, remote)
	if len(actions) != 1 || actions[0].Type != ActionUpload {
		t.Fatalf("expected upload (re-upload when remote missing), got %v", actions)
	}
}

func TestDiff_NoChange(t *testing.T) {
	now := time.Now()
	s := New(nil)
	s.LoadState("C:\\e.txt", State{LocalModTime: now, RemoteModTime: now})

	local := map[string]time.Time{"C:\\e.txt": now}
	remote := map[string]domain.FileInfo{"/e.txt": {Path: "/e.txt", ModTime: now}}

	actions := s.Diff(local, remote)
	if len(actions) != 0 {
		t.Fatalf("expected no actions, got %v", actions)
	}
}

func TestCreateConflictCopies(t *testing.T) {
	dir := t.TempDir()
	localPath := filepath.Join(dir, "doc.txt")
	os.WriteFile(localPath, []byte("local version"), 0644)

	gotRemote := false
	downloadFn := func(dst string) error {
		gotRemote = true
		return os.WriteFile(dst, []byte("remote version"), 0644)
	}

	remoteCopy, err := CreateConflictCopies(localPath, downloadFn)
	if err != nil {
		t.Fatalf("conflict copies: %v", err)
	}
	if !gotRemote {
		t.Error("download not called")
	}

	// Verify local copy exists.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "conflict-local") {
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if string(data) != "local version" {
				t.Errorf("local copy = %q", data)
			}
		}
		if strings.Contains(e.Name(), "conflict-remote") {
			data, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if string(data) != "remote version" {
				t.Errorf("remote copy = %q", data)
			}
		}
	}
	_ = remoteCopy
}

func TestConflictCopy_Provider(t *testing.T) {
	prov := mock.New(t.TempDir())
	dir := t.TempDir()

	// Upload local version as existing remote content.
	localPath := filepath.Join(dir, "shared.docx")
	os.WriteFile(localPath, []byte("local edit"), 0644)
	prov.Upload(t.Context(), "/shared.docx", strings.NewReader("remote edit"))

	downloadFn := func(dst string) error {
		f, err := os.Create(dst)
		if err != nil {
			return err
		}
		defer f.Close()
		return prov.Download(t.Context(), "/shared.docx", f)
	}

	_, err := CreateConflictCopies(localPath, downloadFn)
	if err != nil {
		t.Fatalf("conflict copies with provider: %v", err)
	}

	// Verify both copies exist.
	entries, _ := os.ReadDir(dir)
	foundLocal, foundRemote := false, false
	for _, e := range entries {
		if strings.Contains(e.Name(), "conflict-local") {
			foundLocal = true
		}
		if strings.Contains(e.Name(), "conflict-remote") {
			foundRemote = true
		}
	}
	if !foundLocal || !foundRemote {
		t.Errorf("local=%v remote=%v", foundLocal, foundRemote)
	}
}

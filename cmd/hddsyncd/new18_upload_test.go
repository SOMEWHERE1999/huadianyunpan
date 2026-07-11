package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ncepupan/hdd/internal/cloud/mock"
	"ncepupan/hdd/internal/ipc"
)

func TestDispatchNew18UploadStagedAllowed(t *testing.T) {
	prov := mock.New(t.TempDir())
	resp := dispatchFS(context.Background(),
		ipc.Request{Type: "fs.uploadStaged", ID: "1", Data: json.RawMessage(`{"remotePath":"/f","stagingPath":"x","size":4}`)},
		prov, "", nil,
		false, false, false, false, false, false, false, true)
	if resp.Error == "read_only_filesystem" {
		t.Error("fs.uploadStaged should be allowed in new18 mode")
	}
}

func TestDispatchNew17UploadStagedRejected(t *testing.T) {
	prov := mock.New(t.TempDir())
	resp := dispatchFS(context.Background(),
		ipc.Request{Type: "fs.uploadStaged", ID: "1", Data: json.RawMessage(`{"remotePath":"/f","stagingPath":"x","size":4}`)},
		prov, "", nil,
		false, false, false, false, false, false, true, false)
	if resp.Error != "read_only_filesystem" {
		t.Errorf("new17: error=%q, want read_only_filesystem", resp.Error)
	}
}

func TestUploadStagedNewFile(t *testing.T) {
	prov := mock.New(t.TempDir())
	cacheDir := filepath.Join(t.TempDir(), "cache")
	stagingRoot := filepath.Join(cacheDir, "staging")
	os.MkdirAll(stagingRoot, 0700)
	stagingFile := filepath.Join(stagingRoot, "test.staging")
	os.WriteFile(stagingFile, []byte("hello"), 0644)

	req := ipc.Request{Type: "fs.uploadStaged", ID: "1",
		Data: json.RawMessage(`{"remotePath":"/new.txt","stagingPath":"test.staging","size":5,"conflictPolicy":"fail"}`)}
	resp := dispatchFS(context.Background(), req, prov, cacheDir, nil,
		false, false, false, false, false, false, false, true)
	if resp.Error != "" {
		t.Errorf("upload new file: error=%q", resp.Error)
	}
	info, err := prov.Stat(nil, "/new.txt")
	if err != nil {
		t.Errorf("post-upload stat: %v", err)
	}
	if info.Size != 5 {
		t.Errorf("size=%d, want 5", info.Size)
	}
	if _, err := os.Stat(stagingFile); err == nil {
		t.Error("staging file should be cleaned after upload")
	}
}

func TestUploadStagedConflictFail(t *testing.T) {
	prov := mock.New(t.TempDir())
	prov.Upload(nil, "/exists.txt", strings.NewReader("old"))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	stagingRoot := filepath.Join(cacheDir, "staging")
	os.MkdirAll(stagingRoot, 0700)
	os.WriteFile(filepath.Join(stagingRoot, "test.staging"), []byte("new"), 0644)

	req := ipc.Request{Type: "fs.uploadStaged", ID: "1",
		Data: json.RawMessage(`{"remotePath":"/exists.txt","stagingPath":"test.staging","size":3,"conflictPolicy":"fail"}`)}
	resp := dispatchFS(context.Background(), req, prov, cacheDir, nil,
		false, false, false, false, false, false, false, true)
	if resp.Error != "target already exists" {
		t.Errorf("conflict fail: error=%q", resp.Error)
	}
}

func TestUploadStagedTraversalRejected(t *testing.T) {
	prov := mock.New(t.TempDir())
	cacheDir := filepath.Join(t.TempDir(), "cache")
	req := ipc.Request{Type: "fs.uploadStaged", ID: "1",
		Data: json.RawMessage(`{"remotePath":"/f","stagingPath":"../outside","size":4,"conflictPolicy":"fail"}`)}
	resp := dispatchFS(context.Background(), req, prov, cacheDir, nil,
		false, false, false, false, false, false, false, true)
	if resp.Error == "" || !strings.Contains(resp.Error, "escapes") {
		t.Errorf("traversal: error=%q", resp.Error)
	}
}

func TestUploadStagedOverwrite(t *testing.T) {
	prov := mock.New(t.TempDir())
	prov.Upload(nil, "/overwrite.txt", strings.NewReader("old-content"))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	stagingRoot := filepath.Join(cacheDir, "staging")
	os.MkdirAll(stagingRoot, 0700)
	stagingFile := filepath.Join(stagingRoot, "test.staging")
	os.WriteFile(stagingFile, []byte("new-content-overwrite"), 0644)

	req := ipc.Request{Type: "fs.uploadStaged", ID: "1",
		Data: json.RawMessage(`{"remotePath":"/overwrite.txt","stagingPath":"test.staging","size":21,"conflictPolicy":"overwrite"}`)}
	resp := dispatchFS(context.Background(), req, prov, cacheDir, nil,
		false, false, false, false, false, false, false, true)
	if resp.Error != "" {
		t.Errorf("overwrite upload: error=%q", resp.Error)
	}
	info, err := prov.Stat(nil, "/overwrite.txt")
	if err != nil {
		t.Errorf("post-upload stat: %v", err)
	}
	if info.Size != 21 {
		t.Errorf("size=%d, want 21", info.Size)
	}
}

func TestUploadStagedOverwriteFailure(t *testing.T) {
	prov := mock.New(t.TempDir())
	prov.Upload(nil, "/f.txt", strings.NewReader("original"))
	cacheDir := filepath.Join(t.TempDir(), "cache")
	stagingRoot := filepath.Join(cacheDir, "staging")
	os.MkdirAll(stagingRoot, 0700)
	os.WriteFile(filepath.Join(stagingRoot, "test.staging"), []byte("new"), 0644)

	// Use fail policy on existing file: should return EEXIST, not overwrite.
	req := ipc.Request{Type: "fs.uploadStaged", ID: "1",
		Data: json.RawMessage(`{"remotePath":"/f.txt","stagingPath":"test.staging","size":3,"conflictPolicy":"fail"}`)}
	resp := dispatchFS(context.Background(), req, prov, cacheDir, nil,
		false, false, false, false, false, false, false, true)
	if resp.Error != "target already exists" {
		t.Errorf("fail on existing: error=%q", resp.Error)
	}
	// Original file unchanged.
	info, _ := prov.Stat(nil, "/f.txt")
	if info.Size != 8 {
		t.Errorf("original size=%d, want 8 (unchanged)", info.Size)
	}
}

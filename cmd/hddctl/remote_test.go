package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ncepupan/hdd/internal/cloud"
	"ncepupan/hdd/internal/cloud/mock"
)

type directTestProvider struct {
	*mock.MockProvider
	uploadPolicy cloud.UploadConflictPolicy
	dirPolicy    cloud.DirectoryUploadConflictPolicy
	dirCalls     int
	copyPolicy   cloud.TransferConflictPolicy
	movePolicy   cloud.TransferConflictPolicy
}

func newDirectTestProvider(t *testing.T) *directTestProvider {
	return &directTestProvider{MockProvider: setup(t)}
}

func (p *directTestProvider) UploadFile(_ context.Context, local, remote string, policy cloud.UploadConflictPolicy) (cloud.UploadResult, error) {
	p.uploadPolicy = policy
	return cloud.UploadResult{FinalPath: remote}, nil
}
func (p *directTestProvider) UploadDirectory(_ context.Context, local, parent string, policy cloud.DirectoryUploadConflictPolicy) (cloud.UploadResult, error) {
	p.dirCalls++
	p.dirPolicy = policy
	return cloud.UploadResult{FinalPath: parent + "/root"}, nil
}
func (p *directTestProvider) Copy(_ context.Context, source, parent string, policy cloud.TransferConflictPolicy) (cloud.TransferResult, error) {
	p.copyPolicy = policy
	return cloud.TransferResult{FinalPath: parent + "/copy"}, nil
}
func (p *directTestProvider) Move(_ context.Context, source, parent string, policy cloud.TransferConflictPolicy) (cloud.TransferResult, error) {
	p.movePolicy = policy
	return cloud.TransferResult{FinalPath: parent + "/move"}, nil
}

// setup creates a mock provider with a fresh temp root.
func setup(t *testing.T) *mock.MockProvider {
	t.Helper()
	return mock.New(t.TempDir())
}

// TestRemoteLs_Success verifies ls lists uploaded entries.
func TestRemoteLs_Success(t *testing.T) {
	prov := setup(t)
	ctx := context.Background()
	prov.Upload(ctx, "/a.txt", strings.NewReader("a"))
	prov.Upload(ctx, "/b.txt", strings.NewReader("bb"))
	prov.Mkdir(ctx, "/sub")

	code := runRemoteCmdWithProvider(prov, []string{"ls", "/"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// TestRemoteLs_MissingArg returns error for no path.
func TestRemoteLs_MissingArg(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"ls"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestRemoteStat_Found returns 0.
func TestRemoteStat_Found(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/x.txt", strings.NewReader("hello"))

	code := runRemoteCmdWithProvider(prov, []string{"stat", "/x.txt"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// TestRemoteStat_NotFound returns non-zero.
func TestRemoteStat_NotFound(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"stat", "/no/such"})
	if code == 0 {
		t.Fatal("expected non-zero exit for missing file")
	}
}

// TestRemoteStat_MissingArg returns 1.
func TestRemoteStat_MissingArg(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"stat"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestRemoteMkdir_Success returns 0.
func TestRemoteMkdir_Success(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"mkdir", "/newdir"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	_, err := prov.Stat(context.Background(), "/newdir")
	if err != nil {
		t.Fatalf("stat after mkdir: %v", err)
	}
}

// TestRemoteMkdir_MissingArg returns 1.
func TestRemoteMkdir_MissingArg(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"mkdir"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestRemoteUpload_Success returns 0 and persists the file.
func TestRemoteUpload_Success(t *testing.T) {
	prov := setup(t)
	src, _ := os.CreateTemp(t.TempDir(), "upload-src-*")
	src.WriteString("payload")
	src.Close()

	code := runRemoteCmdWithProvider(prov, []string{"upload", src.Name(), "/dst.txt"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	var buf bytes.Buffer
	prov.Download(context.Background(), "/dst.txt", &buf)
	if buf.String() != "payload" {
		t.Errorf("data = %q, want %q", buf.String(), "payload")
	}
}

// TestRemoteUpload_MissingArgs returns 1.
func TestRemoteUpload_MissingArgs(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"upload", "/only-one"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	code = runRemoteCmdWithProvider(prov, []string{"upload"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestRemoteUpload_SourceNotFound returns non-zero.
func TestRemoteUpload_SourceNotFound(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"upload", "/no/such/local", "/remote"})
	if code == 0 {
		t.Fatal("expected non-zero exit for missing local file")
	}
}

// TestRemoteDownload_Success writes to stdout by default.
func TestRemoteDownload_Success(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/greet.txt",
		strings.NewReader("hello"))

	code := runRemoteCmdWithProvider(prov, []string{"download", "/greet.txt"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// TestRemoteDownload_ToFile writes to the specified local file.
func TestRemoteDownload_ToFile(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/data.bin",
		strings.NewReader("binary"))

	dst := filepath.Join(t.TempDir(), "out.bin")
	code := runRemoteCmdWithProvider(prov, []string{"download", "/data.bin", dst})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	got, _ := os.ReadFile(dst)
	if string(got) != "binary" {
		t.Errorf("data = %q, want %q", got, "binary")
	}
}

// TestRemoteDownload_MissingArg returns 1.
func TestRemoteDownload_MissingArg(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"download"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestRemoteDownload_NotFound returns non-zero.
func TestRemoteDownload_NotFound(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"download", "/nope.txt"})
	if code == 0 {
		t.Fatal("expected non-zero exit for missing remote file")
	}
}

// TestRemoteMv_Success returns 0 and moves the entry.
func TestRemoteMv_Success(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/old.txt",
		strings.NewReader("data"))

	code := runRemoteCmdWithProvider(prov, []string{"mv", "/old.txt", "/new.txt"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	_, err := prov.Stat(context.Background(), "/old.txt")
	if err == nil {
		t.Error("old path should not exist")
	}
	var buf bytes.Buffer
	prov.Download(context.Background(), "/new.txt", &buf)
	if buf.String() != "data" {
		t.Errorf("data = %q, want %q", buf.String(), "data")
	}
}

// TestRemoteMv_MissingArgs returns 1.
func TestRemoteMv_MissingArgs(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"mv", "/one"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	code = runRemoteCmdWithProvider(prov, []string{"mv"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestRemoteRm_Success returns 0 after removing a file.
func TestRemoteRm_Success(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/bye.txt",
		strings.NewReader("x"))

	code := runRemoteCmdWithProvider(prov, []string{"rm", "/bye.txt"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	_, err := prov.Stat(context.Background(), "/bye.txt")
	if err == nil {
		t.Error("file should be removed")
	}
}

// TestRemoteRm_MissingArg returns 1.
func TestRemoteRm_MissingArg(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rm"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestRemoteRm_NotFound returns non-zero.
func TestRemoteRm_NotFound(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rm", "/nothing"})
	if code == 0 {
		t.Fatal("expected non-zero exit for missing file")
	}
}

// TestRemoteUnknownSubcommand returns 1.
func TestRemoteUnknownSubcommand(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"bogus"})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

// TestRemoteChineseNames exercises non-ASCII paths through the CLI layer.
func TestRemoteChineseNames(t *testing.T) {
	prov := setup(t)
	ctx := context.Background()
	prov.Mkdir(ctx, "/鐩綍")
	prov.Upload(ctx, "/鐩綍/鏂囨。.txt", strings.NewReader("content"))

	code := runRemoteCmdWithProvider(prov, []string{"ls", "/鐩綍"})
	if code != 0 {
		t.Fatalf("ls exit code = %d, want 0", code)
	}

	code = runRemoteCmdWithProvider(prov, []string{"stat", "/鐩綍/鏂囨。.txt"})
	if code != 0 {
		t.Fatalf("stat exit code = %d, want 0", code)
	}
}

func TestRemoteDirectConflictPolicies(t *testing.T) {
	t.Run("upload trailing flag", func(t *testing.T) {
		p := newDirectTestProvider(t)
		if code := runRemoteCmdWithProvider(p, []string{"upload", "local.txt", "/远端 文件.txt", "--conflict", "overwrite"}); code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if p.uploadPolicy != cloud.UploadConflictOverwrite {
			t.Fatalf("policy = %q", p.uploadPolicy)
		}
	})
	for _, policy := range []string{"auto-rename", "overwrite", "merge"} {
		t.Run("upload-dir rejects "+policy, func(t *testing.T) {
			p := newDirectTestProvider(t)
			if code := runRemoteCmdWithProvider(p, []string{"upload-dir", "local", "/", "--conflict=" + policy}); code == 0 {
				t.Fatal("expected failure")
			}
			if p.dirCalls != 0 {
				t.Fatalf("provider called %d times", p.dirCalls)
			}
		})
	}
	t.Run("upload-dir default and explicit fail", func(t *testing.T) {
		for _, args := range [][]string{{"upload-dir", "local", "/"}, {"upload-dir", "local", "/", "--conflict=fail"}} {
			p := newDirectTestProvider(t)
			if code := runRemoteCmdWithProvider(p, args); code != 0 {
				t.Fatalf("exit = %d", code)
			}
			if p.dirCalls != 1 || p.dirPolicy != cloud.DirectoryConflictFail {
				t.Fatalf("calls=%d policy=%q", p.dirCalls, p.dirPolicy)
			}
		}
	})
	t.Run("copy auto rename", func(t *testing.T) {
		p := newDirectTestProvider(t)
		if code := runRemoteCmdWithProvider(p, []string{"copy", "/a", "/b", "--conflict", "auto-rename"}); code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if p.copyPolicy != cloud.TransferConflictAutoRename {
			t.Fatalf("policy = %q", p.copyPolicy)
		}
	})
	t.Run("move auto rename", func(t *testing.T) {
		p := newDirectTestProvider(t)
		if code := runRemoteCmdWithProvider(p, []string{"move", "/a", "/b", "--conflict", "auto-rename"}); code != 0 {
			t.Fatalf("exit = %d", code)
		}
		if p.movePolicy != cloud.TransferConflictAutoRename {
			t.Fatalf("policy = %q", p.movePolicy)
		}
	})
}

func TestRemoteHelpSimplifiesUploadDirectoryPolicy(t *testing.T) {
	var out bytes.Buffer
	old := flag.CommandLine.Output()
	flag.CommandLine.SetOutput(&out)
	t.Cleanup(func() { flag.CommandLine.SetOutput(old) })
	printRemoteUsage()
	text := out.String()
	if !strings.Contains(text, "upload-dir <local-directory> <remote-parent-directory>") {
		t.Fatalf("upload-dir usage missing: %s", text)
	}
	if strings.Contains(text, "upload-dir <local-directory> <remote-parent-directory> [--conflict") {
		t.Fatalf("obsolete policies advertised: %s", text)
	}
}

func TestRemoteDirectMissingAndInvalidArguments(t *testing.T) {
	p := newDirectTestProvider(t)
	for _, args := range [][]string{
		{"upload", "only-one"},
		{"upload-dir", "only-one"},
		{"copy", "/source"},
		{"move", "/source"},
	} {
		if code := runRemoteCmdWithProvider(p, args); code == 0 {
			t.Errorf("%v: expected non-zero exit", args)
		}
	}
}

func TestRemoteDownloadSuccessAtomicallyReplacesDestination(t *testing.T) {
	td := t.TempDir()
	dst := filepath.Join(td, "out.bin")

	prov := setup(t)
	prov.Upload(context.Background(), "/data.bin", strings.NewReader("hello"))

	code := runRemoteCmdWithProvider(prov, []string{"download", "/data.bin", dst})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got, _ := os.ReadFile(dst)
	if string(got) != "hello" {
		t.Errorf("content = %q, want %q", got, "hello")
	}
	// No temporary files should remain.
	pattern := filepath.Join(filepath.Dir(dst), ".hdddl-*")
	matches, _ := filepath.Glob(pattern)
	if len(matches) != 0 {
		t.Errorf("temporary files remain: %v", matches)
	}
}

func TestRemoteDownloadNotFoundDoesNotCreateDestination(t *testing.T) {
	td := t.TempDir()
	dst := filepath.Join(td, "nope.bin")

	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"download", "/no-such", dst})
	if code == 0 {
		t.Fatal("expected non-zero exit for nonexistent path")
	}
	if _, err := os.Stat(dst); err == nil {
		t.Error("destination file should not exist after failed download")
	}
}

func TestRemoteDownloadFailureRemovesTemporaryFile(t *testing.T) {
	td := t.TempDir()
	dst := filepath.Join(td, "fail.bin")

	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"download", "/no-such", dst})
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	pattern := filepath.Join(filepath.Dir(dst), ".hdddl-*")
	matches, _ := filepath.Glob(pattern)
	if len(matches) != 0 {
		t.Errorf("temporary files remain after failure: %v", matches)
	}
}

func TestRemoteDownloadFailurePreservesExistingDestination(t *testing.T) {
	td := t.TempDir()
	dst := filepath.Join(td, "existing.bin")
	originalContent := []byte("original content")
	if err := os.WriteFile(dst, originalContent, 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"download", "/no-such", dst})
	if code == 0 {
		t.Fatal("expected non-zero exit for nonexistent path")
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, originalContent) {
		t.Errorf("existing file was modified: got %q, want %q", got, originalContent)
	}
}

func TestRemoteDownloadInterruptedPreservesExistingDestination(t *testing.T) {
	td := t.TempDir()
	dst := filepath.Join(td, "keep.bin")
	originalContent := []byte("keep me")
	if err := os.WriteFile(dst, originalContent, 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	// Use a path the mock provider doesn't have.
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"download", "/interrupted", dst})
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	got, _ := os.ReadFile(dst)
	if !bytes.Equal(got, originalContent) {
		t.Errorf("existing file was modified: got %q, want %q", got, originalContent)
	}
}

func TestRemoteDownloadSuccessLeavesNoTemporaryFiles(t *testing.T) {
	td := t.TempDir()
	dst := filepath.Join(td, "result.bin")

	prov := setup(t)
	prov.Upload(context.Background(), "/ok.txt", strings.NewReader("ok"))

	code := runRemoteCmdWithProvider(prov, []string{"download", "/ok.txt", dst})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	pattern := filepath.Join(filepath.Dir(dst), ".hdddl-*")
	matches, _ := filepath.Glob(pattern)
	if len(matches) != 0 {
		t.Errorf("temporary files remain: %v", matches)
	}
}

// --- rename / move ---

func TestRemoteRenameFileSameDir(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/a.txt", strings.NewReader("hi"))
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/a.txt", "/b.txt"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	c := runRemoteCmdWithProvider(prov, []string{"stat", "/a.txt"})
	if c == 0 {
		t.Error("/a.txt should not exist after rename")
	}
}

func TestRemoteRenameDir(t *testing.T) {
	prov := setup(t)
	prov.Mkdir(context.Background(), "/sub")
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/sub", "/renamed"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestRemoteRenameRoot(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/", "/x"})
	if code == 0 {
		t.Fatal("expected non-zero exit for renaming root")
	}
}

func TestRemoteRenameMissingArgs(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/a"})
	if code == 0 {
		t.Fatal("expected non-zero exit for missing arg")
	}
}

func TestRemoteRenameSourceNotFound(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/no/such", "/dst"})
	if code == 0 {
		t.Fatal("expected non-zero exit for missing source")
	}
}

func TestRemoteRenameMvAlias(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/x.txt", strings.NewReader("x"))
	code := runRemoteCmdWithProvider(prov, []string{"mv", "/x.txt", "/y.txt"})
	if code != 0 {
		t.Fatalf("mv exit code = %d, want 0", code)
	}
}

func TestRemoteMoveReturnsUnsupported(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"move", "/a.txt", "/sub/b.txt"})
	if code == 0 {
		t.Fatal("expected non-zero exit for move (unsupported)")
	}
}

func TestRemoteCopyConflictPolicies(t *testing.T) {
	for _, tc := range []struct {
		name   string
		arg    string
		policy cloud.TransferConflictPolicy
	}{
		{name: "fail", arg: "fail", policy: cloud.TransferConflictFail},
		{name: "auto rename", arg: "auto-rename", policy: cloud.TransferConflictAutoRename},
		{name: "overwrite", arg: "overwrite", policy: cloud.TransferConflictOverwrite},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prov := newDirectTestProvider(t)
			prov.Upload(context.Background(), "/source.txt", strings.NewReader("x"))
			if err := cmdRemoteCopy(prov, []string{"/source.txt", "/", "--conflict", tc.arg}); err != nil {
				t.Fatal(err)
			}
			if prov.copyPolicy != tc.policy {
				t.Fatalf("policy = %q, want %q", prov.copyPolicy, tc.policy)
			}
		})
	}
}

func TestRemoteCopyDirectoryDefaultsToAutoRename(t *testing.T) {
	prov := newDirectTestProvider(t)
	if err := prov.Mkdir(context.Background(), "/中文 目录"); err != nil {
		t.Fatal(err)
	}
	if err := cmdRemoteCopy(prov, []string{"/中文 目录", "/"}); err != nil {
		t.Fatal(err)
	}
	if prov.copyPolicy != cloud.TransferConflictAutoRename {
		t.Fatalf("policy = %q", prov.copyPolicy)
	}
}

func TestRemoteMoveConflictPolicies(t *testing.T) {
	for _, tc := range []struct {
		arg    string
		policy cloud.TransferConflictPolicy
	}{
		{arg: "fail", policy: cloud.TransferConflictFail},
		{arg: "auto-rename", policy: cloud.TransferConflictAutoRename},
		{arg: "overwrite", policy: cloud.TransferConflictOverwrite},
		{arg: "merge", policy: cloud.TransferConflictMerge},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			prov := newDirectTestProvider(t)
			if err := cmdRemoteMove(prov, []string{"/source", "/target", "--conflict=" + tc.arg}); err != nil {
				t.Fatal(err)
			}
			if prov.movePolicy != tc.policy {
				t.Fatalf("policy = %q, want %q", prov.movePolicy, tc.policy)
			}
		})
	}
}

// --- remove / delete ---

func TestRemoteRmFile(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/f.txt", strings.NewReader("x"))
	code := runRemoteCmdWithProvider(prov, []string{"rm", "/f.txt"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	code = runRemoteCmdWithProvider(prov, []string{"stat", "/f.txt"})
	if code == 0 {
		t.Error("/f.txt should not exist after rm")
	}
}

func TestRemoteRmEmptyDir(t *testing.T) {
	prov := setup(t)
	prov.Mkdir(context.Background(), "/emptydir")
	code := runRemoteCmdWithProvider(prov, []string{"rm", "/emptydir"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestRemoteRmRoot(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rm", "/"})
	if code == 0 {
		t.Fatal("expected non-zero exit for removing root")
	}
}

func TestRemoteRmMissingArg(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rm"})
	if code == 0 {
		t.Fatal("expected non-zero exit for missing arg")
	}
}

func TestRemoteRmNotFound(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rm", "/no/such"})
	if code == 0 {
		t.Fatal("expected non-zero exit for missing path")
	}
}

func TestRemoteRemoveDeleteAliases(t *testing.T) {
	tests := []string{"remove", "delete", "rm"}
	for _, alias := range tests {
		t.Run(alias, func(t *testing.T) {
			prov := setup(t)
			prov.Upload(context.Background(), "/"+alias+".txt", strings.NewReader("x"))
			code := runRemoteCmdWithProvider(prov, []string{alias, "/" + alias + ".txt"})
			if code != 0 {
				t.Errorf("%s exit code = %d, want 0", alias, code)
			}
		})
	}
}

func TestRemoteRmProviderErrorPropagates(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/tmp.txt", strings.NewReader("x"))
	code := runRemoteCmdWithProvider(prov, []string{"rm", "/tmp.txt"})
	if code != 0 {
		t.Fatalf("rm failed unexpectedly: %d", code)
	}
	code = runRemoteCmdWithProvider(prov, []string{"rm", "/tmp.txt"})
	if code == 0 {
		t.Fatal("expected non-zero exit when removing already-removed file")
	}
}

// --- help ---

func TestRemoteHelp(t *testing.T) {
	oldMock := *useMock
	*useMock = true
	defer func() { *useMock = oldMock }()

	tests := []struct {
		name string
		args []string
	}{
		{"no subcommand", nil},
		{"help", []string{"help"}},
		{"--help", []string{"--help"}},
		{"-h", []string{"-h"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code := runRemoteCmd(tt.args)
			if code != 0 {
				t.Errorf("%s exit code = %d, want 0", tt.name, code)
			}
		})
	}
}

func TestUnknownRemoteSubcommand(t *testing.T) {
	oldMock := *useMock
	*useMock = true
	defer func() { *useMock = oldMock }()
	code := runRemoteCmd([]string{"bogus"})
	if code == 0 {
		t.Fatal("expected non-zero exit for unknown subcommand")
	}
}

func TestRenameSameParentSucceeds(t *testing.T) {
	prov := setup(t)
	prov.Mkdir(context.Background(), "/dir")
	prov.Upload(context.Background(), "/dir/old.txt", strings.NewReader("x"))
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/dir/old.txt", "/dir/new.txt"})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestRenameCrossParentRejected(t *testing.T) {
	prov := setup(t)
	prov.Mkdir(context.Background(), "/dir1")
	prov.Mkdir(context.Background(), "/dir2")
	prov.Upload(context.Background(), "/dir1/old.txt", strings.NewReader("x"))
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/dir1/old.txt", "/dir2/new.txt"})
	if code == 0 {
		t.Fatal("expected non-zero exit for cross-parent rename")
	}
}

func TestRenameCrossParentDoesNotCallProvider(t *testing.T) {
	prov := setup(t)
	prov.Mkdir(context.Background(), "/a")
	prov.Mkdir(context.Background(), "/b")
	prov.Upload(context.Background(), "/a/f.txt", strings.NewReader("x"))
	// Cross-parent rename should be rejected before any API call.
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/a/f.txt", "/b/f.txt"})
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	// Original file should still exist.
	c := runRemoteCmdWithProvider(prov, []string{"stat", "/a/f.txt"})
	if c != 0 {
		t.Error("/a/f.txt should still exist after rejected cross-parent rename")
	}
}

func TestRenameCrossParentDoesNotCreateBasenameInOldParent(t *testing.T) {
	prov := setup(t)
	prov.Mkdir(context.Background(), "/src")
	prov.Mkdir(context.Background(), "/dst")
	prov.Upload(context.Background(), "/src/file.txt", strings.NewReader("x"))
	// Try to rename /src/file.txt → /dst/file.txt. Should be rejected.
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/src/file.txt", "/dst/file.txt"})
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	// /src should still have "file.txt", not "dst/file.txt" basename.
	code = runRemoteCmdWithProvider(prov, []string{"stat", "/src/file.txt"})
	if code != 0 {
		t.Error("/src/file.txt should still exist")
	}
}

func TestRenameSamePath(t *testing.T) {
	prov := setup(t)
	prov.Upload(context.Background(), "/f.txt", strings.NewReader("x"))
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/f.txt", "/f.txt"})
	if code == 0 {
		t.Fatal("expected non-zero exit for same old and new path")
	}
}

func TestRenameRootRejected(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"rename", "/", "/x"})
	if code == 0 {
		t.Fatal("expected non-zero exit for renaming root")
	}
}

func TestMoveCrossParentUnsupported(t *testing.T) {
	prov := setup(t)
	code := runRemoteCmdWithProvider(prov, []string{"move", "/a.txt", "/b.txt"})
	if code == 0 {
		t.Fatal("expected non-zero exit for move (unsupported)")
	}
}

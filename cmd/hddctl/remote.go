package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"ncepupan/hdd/internal/cloud"
)

// cmdRemoteLs lists entries under a remote path.
func cmdRemoteLs(prov cloud.Provider, args []string) error {
	if len(args) != 1 {
		return usageError("ls <path>")
	}
	entries, err := prov.List(context.Background(), args[0])
	if err != nil {
		return err
	}
	for _, e := range entries {
		dir := ""
		if e.IsDir {
			dir = "/"
		}
		fmt.Printf("%s%s  %10d  %s\n", e.Path, dir, e.Size,
			e.ModTime.Format(time.RFC3339))
	}
	return nil
}

// cmdRemoteStat shows metadata for a single remote path.
func cmdRemoteStat(prov cloud.Provider, args []string) error {
	if len(args) != 1 {
		return usageError("stat <path>")
	}
	info, err := prov.Stat(context.Background(), args[0])
	if err != nil {
		return err
	}
	kind := "file"
	if info.IsDir {
		kind = "directory"
	}
	fmt.Printf("  path: %s\n", info.Path)
	fmt.Printf("  type: %s\n", kind)
	fmt.Printf("  size: %d\n", info.Size)
	fmt.Printf("  time: %s\n", info.ModTime.Format(time.RFC3339))
	return nil
}

// cmdRemoteMkdir creates a directory.
func cmdRemoteMkdir(prov cloud.Provider, args []string) error {
	if len(args) != 1 {
		return usageError("mkdir <path>")
	}
	return prov.Mkdir(context.Background(), args[0])
}

// cmdRemoteUpload uploads a local file to a remote path.
func cmdRemoteUpload(prov cloud.Provider, args []string) error {
	pos, conflict, err := parseConflictArgs(args, string(cloud.UploadConflictFail))
	if err != nil || len(pos) != 2 {
		return usageError("upload <local-file> <remote-file> [--conflict fail|auto-rename|overwrite]")
	}
	policy := cloud.UploadConflictPolicy(conflict)
	if policy != cloud.UploadConflictFail && policy != cloud.UploadConflictAutoRename && policy != cloud.UploadConflictOverwrite {
		return fmt.Errorf("unsupported_conflict_policy: %q", conflict)
	}
	src, dst := pos[0], pos[1]
	if direct, ok := prov.(cloud.DirectRemoteProvider); ok {
		result, err := direct.UploadFile(context.Background(), src, dst, policy)
		if err == nil {
			fmt.Println(result.FinalPath)
		}
		return err
	}
	if policy != cloud.UploadConflictFail {
		return errors.New("unsupported_conflict_policy")
	}

	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open local file: %w", err)
	}
	defer f.Close()

	return prov.Upload(context.Background(), dst, f)
}

func cmdRemoteUploadDirectory(prov cloud.Provider, args []string) error {
	pos, conflict, err := parseConflictArgs(args, string(cloud.DirectoryConflictFail))
	if err != nil || len(pos) != 2 {
		return usageError("upload-dir <local-directory> <remote-parent-directory>")
	}
	policy := cloud.DirectoryUploadConflictPolicy(conflict)
	if policy != cloud.DirectoryConflictFail {
		return errors.New("unsupported_conflict_policy_for_directory_upload")
	}
	direct, ok := prov.(cloud.DirectRemoteProvider)
	if !ok {
		return errors.New("unsupported_operation")
	}
	result, err := direct.UploadDirectory(context.Background(), pos[0], pos[1], policy)
	if err == nil {
		fmt.Println(result.FinalPath)
	}
	return err
}

func cmdRemoteCopy(prov cloud.Provider, args []string) error {
	pos, conflict, err := parseConflictArgs(args, string(cloud.TransferConflictFail))
	if err != nil || len(pos) != 2 {
		return usageError("copy <source-path> <destination-directory> [--conflict fail|auto-rename|overwrite]")
	}
	if !hasConflictArg(args) {
		info, statErr := prov.Stat(context.Background(), pos[0])
		if statErr != nil {
			return statErr
		}
		if info.IsDir {
			conflict = string(cloud.TransferConflictAutoRename)
		}
	}
	policy := cloud.TransferConflictPolicy(conflict)
	if policy != cloud.TransferConflictFail && policy != cloud.TransferConflictAutoRename && policy != cloud.TransferConflictOverwrite {
		return fmt.Errorf("unsupported_conflict_policy: %q", conflict)
	}
	direct, ok := prov.(cloud.DirectRemoteProvider)
	if !ok {
		return errors.New("unsupported_operation")
	}
	result, err := direct.Copy(context.Background(), pos[0], pos[1], policy)
	if err == nil {
		fmt.Println(result.FinalPath)
	}
	return err
}

// cmdRemoteDownload downloads a remote file.
// If dst is "-" or empty and there are exactly 2 positional args,
// writes to stdout.  Otherwise dst is a local file path.
func cmdRemoteDownload(prov cloud.Provider, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usageError("download <remote-path> [local-path]")
	}
	src := args[0]
	dst := ""
	if len(args) == 2 {
		dst = args[1]
	}

	var w io.Writer = os.Stdout
	var closer *downloadCloser
	if dst != "" && dst != "-" {
		dir := filepath.Dir(dst)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create parent directory: %w", err)
		}
		f, err := os.CreateTemp(dir, ".hdddl-*")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		tmpName := f.Name()
		w = f
		closer = &downloadCloser{f: f, tmp: tmpName, dst: dst}
	} else if dst == "" {
		dlDir := filepath.Join(os.TempDir(), "hddctl-downloads")
		if err := os.MkdirAll(dlDir, 0o755); err != nil {
			return fmt.Errorf("create download directory %q: %w", dlDir, err)
		}
		base := filepath.Base(strings.ReplaceAll(src, "/", string(os.PathSeparator)))
		dstPath := filepath.Join(dlDir, base)
		f, err := os.CreateTemp(dlDir, ".hdddl-*")
		if err != nil {
			return fmt.Errorf("create temp file: %w", err)
		}
		tmpName := f.Name()
		w = f
		closer = &downloadCloser{f: f, tmp: tmpName, dst: dstPath}
	}
	if closer != nil {
		defer closer.Close()
	}

	dlErr := prov.Download(context.Background(), src, w)
	if closer != nil && dlErr != nil {
		closer.failed = true
	}
	return dlErr
}

// cmdRemoteMv renames or moves a remote entry (alias for rename).
func cmdRemoteMv(prov cloud.Provider, args []string) error {
	return cmdRemoteRename(prov, args)
}

// cmdRemoteRename renames a file or directory within the same parent.
func cmdRemoteRename(prov cloud.Provider, args []string) error {
	if len(args) != 2 {
		return usageError("rename <old-path> <new-path>")
	}
	oldPath := cleanRemotePath(args[0])
	newPath := cleanRemotePath(args[1])

	if oldPath == "/" {
		return errors.New("cannot rename root directory")
	}
	if oldPath == newPath {
		return errors.New("rename: old and new path are the same")
	}

	oldParent := remoteParent(oldPath)
	newParent := remoteParent(newPath)
	if oldParent != newParent {
		return errors.New("rename: cross-parent rename is not supported. The AnyShare rename API only changes the entry name within the same parent directory. Use \"hddctl remote move\" for an explanation.")
	}

	return prov.Rename(context.Background(), oldPath, newPath)
}

// cleanRemotePath normalizes a remote path (slash-separated).
func cleanRemotePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return "/"
	}
	return path.Join("/", strings.Trim(p, "/"))
}

// remoteParent returns the parent directory of a remote path.
func remoteParent(p string) string {
	p = cleanRemotePath(p)
	if p == "/" {
		return "/"
	}
	return path.Dir(p)
}

// cmdRemoteMove moves a remote file or directory to another parent.
func cmdRemoteMove(prov cloud.Provider, args []string) error {
	pos, conflict, err := parseConflictArgs(args, string(cloud.TransferConflictFail))
	if err != nil || len(pos) != 2 {
		return usageError("move <source-path> <destination-directory> [--conflict fail|auto-rename|overwrite|merge]")
	}
	policy := cloud.TransferConflictPolicy(conflict)
	if policy != cloud.TransferConflictFail && policy != cloud.TransferConflictAutoRename && policy != cloud.TransferConflictOverwrite && policy != cloud.TransferConflictMerge {
		return fmt.Errorf("unsupported_conflict_policy: %q", conflict)
	}
	direct, ok := prov.(cloud.DirectRemoteProvider)
	if !ok {
		return errors.New("unsupported_operation")
	}
	result, err := direct.Move(context.Background(), pos[0], pos[1], policy)
	if err == nil {
		fmt.Println(result.FinalPath)
	}
	return err
}

func parseConflictArgs(args []string, defaultPolicy string) ([]string, string, error) {
	policy := defaultPolicy
	positional := make([]string, 0, 2)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--conflict":
			i++
			if i >= len(args) {
				return nil, "", errUsage
			}
			policy = args[i]
		case strings.HasPrefix(arg, "--conflict="):
			policy = strings.TrimPrefix(arg, "--conflict=")
		case strings.HasPrefix(arg, "-"):
			return nil, "", errUsage
		default:
			positional = append(positional, arg)
		}
	}
	return positional, policy, nil
}

func hasConflictArg(args []string) bool {
	for _, arg := range args {
		if arg == "--conflict" || strings.HasPrefix(arg, "--conflict=") {
			return true
		}
	}
	return false
}

// cmdRemoteRm removes a remote file or empty directory.
func cmdRemoteRm(prov cloud.Provider, args []string) error {
	if len(args) != 1 {
		return usageError("rm <path>")
	}
	path := args[0]
	if path == "/" || strings.TrimSpace(path) == "/" {
		return errors.New("cannot remove root directory")
	}
	return prov.Remove(context.Background(), path)
}

// cmdRemoteMv — renamed alias kept for backward compat.
var _ = cmdRemoteMv

// errUsage is a sentinel for usage errors.
var errUsage = errors.New("usage")

func usageError(format string, a ...any) error {
	return fmt.Errorf("usage: "+format, a...)
}

type downloadCloser struct {
	f      *os.File
	tmp    string
	dst    string
	done   bool
	failed bool
}

func (c *downloadCloser) Close() error {
	if c.done {
		return nil
	}
	c.done = true

	closeErr := c.f.Close()
	if closeErr != nil {
		c.failed = true
	}
	if c.failed {
		os.Remove(c.tmp)
		if closeErr != nil {
			return closeErr
		}
		return fmt.Errorf("download failed; temporary file removed")
	}
	if err := os.Rename(c.tmp, c.dst); err != nil {
		os.Remove(c.tmp)
		return fmt.Errorf("rename temporary file: %w", err)
	}
	return nil
}

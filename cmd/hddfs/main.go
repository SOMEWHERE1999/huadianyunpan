//go:build windows && cgo

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"ncepupan/hdd/internal/cloud/mock"
	"ncepupan/hdd/internal/ipc"
	"ncepupan/hdd/internal/mount/winfsp"
	"ncepupan/hdd/internal/platform/windows/npipe"

	"github.com/winfsp/cgofuse/fuse"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "memfs":
		runMemFS(args)
	case "mount":
		runMount(args)
	case "help", "-h", "--help":
		printUsage()
	case "version":
		fmt.Println("hddfs version 0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "hddfs: unknown command %q\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	out := os.Stderr
	out.Write([]byte("Usage: hddfs [command]\n\n"))
	out.Write([]byte("Commands:\n"))
	out.Write([]byte("  memfs --mount H:              Mount read-only in-memory demo\n"))
	out.Write([]byte("  mount --provider mock --root <dir> --mount H:\n"))
	out.Write([]byte("                                Mount cloud-backed read-only filesystem\n"))
	out.Write([]byte("  mount --daemon --mount H:     Mount via hddsyncd IPC\n"))
	out.Write([]byte("  help                          Print this help message\n"))
	out.Write([]byte("  version                       Print version information\n"))
	out.Write([]byte("\nOptions:\n"))
	out.Write([]byte("  --mount string                Drive letter (e.g. H:)\n"))
	out.Write([]byte("  --provider string             Cloud provider (mock)\n"))
	out.Write([]byte("  --root string                 Local root for mock provider\n"))
	out.Write([]byte("  --daemon                      Use hddsyncd via IPC instead of direct provider\n"))
	out.Write([]byte("  --pipe string                 Named pipe path (default: \\\\.\\pipe\\huadian-drive)\n"))
	out.Write([]byte("  --read-only                   Mount as read-only; reject all writes\n"))
	out.Write([]byte("  --mkdir-only                  Allow directory creation only; reject all other writes\n"))
	out.Write([]byte("  --mkdir-rename-only           Allow directory creation and same-parent dir rename\n"))
	out.Write([]byte("  --debug-log <path>            Write FUSE callback trace to file\n"))
}

// ---------------------------------------------------------------------------
// memfs command
// ---------------------------------------------------------------------------

func runMemFS(args []string) {
	mountPoint := parseMountFlag(args)
	if mountPoint == "" {
		fmt.Fprintln(os.Stderr, "hddfs: --mount is required")
		os.Exit(1)
	}
	validateAndMount(mountPoint, func() fuse.FileSystemInterface {
		return winfsp.NewMemFS()
	}, true)
}

// ---------------------------------------------------------------------------
// mount command
// ---------------------------------------------------------------------------

func runMount(args []string) {
	var (
		provider                          string
		root                              string
		mountPoint                        string
		daemon                            bool
		pipePath                          string
		readOnly                          bool
		mkdirOnly                         bool
		mkdirRenameOnly                   bool
		mkdirRenameMoveOnly               bool
		mkdirRenameMoveFileRenameOnly     bool
		mkdirRenameMoveFileRenameMoveOnly       bool
		mkdirRenameMoveFileRenameMoveRemoveOnly             bool
		mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly   bool
		debugLog                                             string
	)

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--provider":
			if i+1 < len(args) {
				provider = args[i+1]
				i++
			}
		case "--root":
			if i+1 < len(args) {
				root = args[i+1]
				i++
			}
		case "--mount":
			if i+1 < len(args) {
				mountPoint = args[i+1]
				i++
			}
		case "--daemon":
			daemon = true
		case "--pipe":
			if i+1 < len(args) {
				pipePath = args[i+1]
				i++
			}
		case "--read-only":
			readOnly = true
		case "--mkdir-only":
			mkdirOnly = true
		case "--mkdir-rename-only":
			mkdirRenameOnly = true
		case "--mkdir-rename-move-only":
			mkdirRenameMoveOnly = true
		case "--mkdir-rename-move-file-rename-only":
			mkdirRenameMoveFileRenameOnly = true
		case "--mkdir-rename-move-file-rename-move-only":
			mkdirRenameMoveFileRenameMoveOnly = true
		case "--mkdir-rename-move-file-rename-move-remove-only":
			mkdirRenameMoveFileRenameMoveRemoveOnly = true
		case "--mkdir-rename-move-file-rename-move-remove-copy-upload-only":
			mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly = true
		case "--debug-log":
			if i+1 < len(args) {
				debugLog = args[i+1]
				i++
			}
		case "-h", "--help":
			fmt.Println("Usage: hddfs mount [--daemon] [options]")
			fmt.Println()
			fmt.Println("  Direct mode:")
			fmt.Println("    hddfs mount --provider mock --root <dir> --mount H:")
			fmt.Println()
			fmt.Println("  Daemon mode:")
			fmt.Println("    hddfs mount --daemon --mount H:")
			fmt.Println("    hddfs mount --daemon --pipe \\\\.\\pipe\\huadian-drive --mount H:")
			return
		}
	}

	if mountPoint == "" {
		fmt.Fprintln(os.Stderr, "hddfs: --mount is required")
		os.Exit(1)
	}

	if readOnly && mkdirOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --read-only and --mkdir-only are mutually exclusive")
		os.Exit(1)
	}
	if readOnly && mkdirRenameOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --read-only and --mkdir-rename-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirOnly && mkdirRenameOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-only and --mkdir-rename-only are mutually exclusive")
		os.Exit(1)
	}
	if readOnly && mkdirRenameMoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --read-only and --mkdir-rename-move-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirOnly && mkdirRenameMoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-only and --mkdir-rename-move-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameOnly && mkdirRenameMoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-only and --mkdir-rename-move-only are mutually exclusive")
		os.Exit(1)
	}
	if readOnly && mkdirRenameMoveFileRenameOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --read-only and --mkdir-rename-move-file-rename-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirOnly && mkdirRenameMoveFileRenameOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-only and --mkdir-rename-move-file-rename-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameOnly && mkdirRenameMoveFileRenameOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-only and --mkdir-rename-move-file-rename-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveOnly && mkdirRenameMoveFileRenameOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-only and --mkdir-rename-move-file-rename-only are mutually exclusive")
		os.Exit(1)
	}
	if readOnly && mkdirRenameMoveFileRenameMoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --read-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirOnly && mkdirRenameMoveFileRenameMoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameOnly && mkdirRenameMoveFileRenameMoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveOnly && mkdirRenameMoveFileRenameMoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveFileRenameOnly && mkdirRenameMoveFileRenameMoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-file-rename-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
		os.Exit(1)
	}
	if readOnly && mkdirRenameMoveFileRenameMoveRemoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --read-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirOnly && mkdirRenameMoveFileRenameMoveRemoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameOnly && mkdirRenameMoveFileRenameMoveRemoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveOnly && mkdirRenameMoveFileRenameMoveRemoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveFileRenameOnly && mkdirRenameMoveFileRenameMoveRemoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-file-rename-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveFileRenameMoveOnly && mkdirRenameMoveFileRenameMoveRemoveOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-file-rename-move-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
		os.Exit(1)
	}
	if readOnly && mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --read-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirOnly && mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameOnly && mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveOnly && mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveFileRenameOnly && mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-file-rename-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveFileRenameMoveOnly && mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-file-rename-move-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
		os.Exit(1)
	}
	if mkdirRenameMoveFileRenameMoveRemoveOnly && mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		fmt.Fprintln(os.Stderr, "hddfs: --mkdir-rename-move-file-rename-move-remove-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
		os.Exit(1)
	}

	if daemon {
		if pipePath == "" {
			pipePath = `\\.\pipe\huadian-drive`
		}

		// Pre-mount handshake: verify the daemon is healthy before
		// creating a WinFsp mount. Retry for up to 5 seconds.
		if err := daemonHandshake(pipePath, 5*time.Second); err != nil {
			fmt.Fprintf(os.Stderr, "hddfs: daemon probe failed: %v\n", err)
			os.Exit(1)
		}

		validateAndMount(mountPoint, func() fuse.FileSystemInterface {
			fs := winfsp.NewIPCFileSystem(winfsp.IPCFileSystemConfig{
				PipePath:                                         pipePath,
				ReadOnly:                                         readOnly,
				MkdirOnly:                                        mkdirOnly,
				MkdirRenameOnly:                                  mkdirRenameOnly,
				MkdirRenameMoveOnly:                              mkdirRenameMoveOnly,
				MkdirRenameMoveFileRenameOnly:                    mkdirRenameMoveFileRenameOnly,
				MkdirRenameMoveFileRenameMoveOnly:                mkdirRenameMoveFileRenameMoveOnly,
				MkdirRenameMoveFileRenameMoveRemoveOnly:          mkdirRenameMoveFileRenameMoveRemoveOnly,
				MkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly: mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly,
				LogPath:                           debugLog,
			})
			return fs
		}, readOnly, func(fsi fuse.FileSystemInterface) {
			if ipc, ok := fsi.(*winfsp.IPCFileSystem); ok {
				ipc.Close()
			}
		})
		return
	}

	// Direct provider mode.
	if provider == "" {
		fmt.Fprintln(os.Stderr, "hddfs: --provider is required (e.g. mock)")
		os.Exit(1)
	}
	if root == "" {
		fmt.Fprintln(os.Stderr, "hddfs: --root is required for mock provider")
		os.Exit(1)
	}
	if provider != "mock" {
		fmt.Fprintf(os.Stderr, "hddfs: unknown provider %q (only 'mock' is supported)\n", provider)
		os.Exit(1)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hddfs: invalid root %q: %v\n", root, err)
		os.Exit(1)
	}
	if _, err := os.Stat(absRoot); err != nil {
		fmt.Fprintf(os.Stderr, "hddfs: root directory does not exist: %q\n", absRoot)
		os.Exit(1)
	}

	prov := mock.New(absRoot)
	cacheDir := filepath.Join(os.TempDir(), "hddfs-cache")
	os.MkdirAll(cacheDir, 0700)

	validateAndMount(mountPoint, func() fuse.FileSystemInterface {
		cfs, err := winfsp.NewCloudFS(prov, cacheDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "hddfs: create cloudfs: %v\n", err)
			os.Exit(1)
		}
		return cfs
	}, true)
}

// ---------------------------------------------------------------------------
// Common
// ---------------------------------------------------------------------------

func parseMountFlag(args []string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == "--mount" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func validateAndMount(mountPoint string, createFS func() fuse.FileSystemInterface, readOnly bool, cleanup ...func(fuse.FileSystemInterface)) {
	mp := strings.TrimRight(mountPoint, "\\")
	if !(len(mp) == 2 && mp[1] == ':') {
		fmt.Fprintf(os.Stderr, "hddfs: invalid mount point %q (expected drive letter e.g. H:)\n", mountPoint)
		os.Exit(1)
	}

	fsi := createFS()
	host := fuse.NewFileSystemHost(fsi)
	if _, ok := fsi.(interface{ RequiresDeleteAccessCheck() }); ok {
		host.SetCapDeleteAccess(true)
	}

	opts := mountOptions(readOnly)
	if logger, ok := fsi.(interface {
		LogMountConfiguration(string, []string)
	}); ok {
		logger.LogMountConfiguration(mp, opts)
	}

	fmt.Printf("Mounting Huadian Drive at %s...\n", mp)

	// Install signal handler before Mount (which blocks).
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	defer signal.Stop(ch)

	// Mount in background goroutine because host.Mount blocks until unmount.
	mounted := make(chan bool, 1)
	go func() {
		mounted <- host.Mount(mp, opts)
	}()

	// Wait for either mount completion or Ctrl+C.
	select {
	case ok := <-mounted:
		if !ok {
			fmt.Fprintf(os.Stderr, "hddfs: mount failed\n")
			os.Exit(2)
		}
		fmt.Println("Unmounted.")
	case <-ch:
		fmt.Println("\nUnmounting...")
		if !host.Unmount() {
			fmt.Fprintln(os.Stderr, "hddfs: unmount warning — may already be unmounted")
		}
		<-mounted
		fmt.Println("Unmounted.")
	}

	for _, c := range cleanup {
		c(fsi)
	}
}

func mountOptions(readOnly bool) []string {
	if readOnly {
		return []string{"-o", "ro"}
	}
	return nil
}

func daemonHandshake(pipePath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		conn, err := npipe.Dial(pipePath, 3*time.Second)
		if err != nil {
			lastErr = fmt.Errorf("dial: %w", err)
			time.Sleep(250 * time.Millisecond)
			continue
		}
		client := &npipeClient{conn: conn}
		// status
		if _, err := client.Call(ipc.Request{Type: "status", ID: "probe-status"}); err != nil {
			lastErr = fmt.Errorf("status: %w", err)
			conn.Close()
			time.Sleep(250 * time.Millisecond)
			continue
		}
		// stat("/")
		data := ipcRawJSON(map[string]string{"path": "/"})
		if _, err := client.Call(ipc.Request{Type: "fs.stat", ID: "probe-stat", Data: data}); err != nil {
			lastErr = fmt.Errorf("stat(/): %w", err)
			conn.Close()
			time.Sleep(250 * time.Millisecond)
			continue
		}
		// list("/")
		if _, err := client.Call(ipc.Request{Type: "fs.list", ID: "probe-list", Data: data}); err != nil {
			lastErr = fmt.Errorf("list(/): %w", err)
			conn.Close()
			time.Sleep(250 * time.Millisecond)
			continue
		}
		conn.Close()
		return nil
	}
	return fmt.Errorf("handshake timeout after %v: %w", timeout, lastErr)
}

type npipeClient struct {
	conn *npipe.ClientConn
}

func (c *npipeClient) Call(req ipc.Request) (ipc.Response, error) {
	return c.conn.Call(req)
}

func ipcRawJSON(v any) []byte {
	d, _ := json.Marshal(v)
	return d
}

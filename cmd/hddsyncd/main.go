// hddsyncd is the background synchronization daemon for the Huadian Drive
// cloud storage service. It watches configured directories and keeps them
// synchronized with the cloud provider.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ncepupan/hdd/internal/app"
	"ncepupan/hdd/internal/cloud"
	"ncepupan/hdd/internal/cloud/anyshare/auth"
	"ncepupan/hdd/internal/cloud/huadian"
	"ncepupan/hdd/internal/cloud/mock"
	"ncepupan/hdd/internal/domain"
	"ncepupan/hdd/internal/ipc"
	securelog "ncepupan/hdd/internal/logging"
	"ncepupan/hdd/internal/platform/windows/npipe"
	"ncepupan/hdd/internal/platform/windows/service"
	"ncepupan/hdd/internal/store/sqlite"
	"ncepupan/hdd/internal/watch"
	"ncepupan/hdd/internal/worker"
)

type writePolicy struct {
	name              string
	canMkdir          bool
	canRenameFile     bool
	canRenameDir      bool
	canMoveFile       bool
	canMoveDir        bool
	canCreateFile     bool
	canWriteFile      bool
	canDeleteFile     bool
	canDeleteDir      bool
	canCopyUploadFile bool
}

func writePolicyFromArgs(readOnly, mkdirOnly, mkdirRenameOnly, mkdirRenameMoveOnly, mkdirRenameMoveFileRenameOnly, mkdirRenameMoveFileRenameMoveOnly, mkdirRenameMoveFileRenameMoveRemoveOnly, mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly bool) writePolicy {
	switch {
	case readOnly:
		return writePolicy{name: "read_only"}
	case mkdirOnly:
		return writePolicy{name: "mkdir_only", canMkdir: true}
	case mkdirRenameOnly:
		return writePolicy{name: "mkdir_rename_only", canMkdir: true, canRenameDir: true}
	case mkdirRenameMoveOnly:
		return writePolicy{name: "mkdir_rename_move_only", canMkdir: true, canRenameDir: true, canMoveDir: true}
	case mkdirRenameMoveFileRenameOnly:
		return writePolicy{name: "mkdir_rename_move_file_rename_only", canMkdir: true, canRenameFile: true, canRenameDir: true, canMoveDir: true}
	case mkdirRenameMoveFileRenameMoveOnly:
		return writePolicy{name: "mkdir_rename_move_file_rename_move_only", canMkdir: true, canRenameFile: true, canRenameDir: true, canMoveFile: true, canMoveDir: true}
	case mkdirRenameMoveFileRenameMoveRemoveOnly:
		return writePolicy{name: "mkdir_rename_move_file_rename_move_remove_only", canMkdir: true, canRenameFile: true, canRenameDir: true, canMoveFile: true, canMoveDir: true, canDeleteFile: true, canDeleteDir: true}
	case mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly:
		return writePolicy{name: "mkdir_rename_move_file_rename_move_remove_copy_upload_only", canMkdir: true, canRenameFile: true, canRenameDir: true, canMoveFile: true, canMoveDir: true, canDeleteFile: true, canDeleteDir: true, canCopyUploadFile: true}
	default:
		return writePolicy{name: "fully_writable"}
	}
}

type runArgs struct {
	provider                              string
	mockRoot                              string
	dataDir                               string
	pipePath                              string
	noBackground                          bool
	readOnly                              bool
	mkdirOnly                             bool
	mkdirRenameOnly                       bool
	mkdirRenameMoveOnly                   bool
	mkdirRenameMoveFileRenameOnly         bool
	mkdirRenameMoveFileRenameMoveOnly     bool
	mkdirRenameMoveFileRenameMoveRemoveOnly       bool
	mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly bool
}

func parseRunArgs(args []string) (*runArgs, error) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.Usage = func() {} // suppress default; handled by caller
	provider := fs.String("provider", "", "Cloud provider (huadian or mock)")
	mockRoot := fs.String("root", "", "Local root directory for mock provider")
	dataDir := fs.String("data-dir", "", "Override data directory")
	pipePath := fs.String("pipe", "", "Override named pipe path")
	noBackground := fs.Bool("no-background", false, "Disable worker pool, watcher, and initial scan")
	readOnly := fs.Bool("read-only", false, "Reject all write operations via IPC (read-only filesystem)")
	mkdirOnly := fs.Bool("mkdir-only", false, "Allow directory creation only; reject all other writes")
	mkdirRenameOnly := fs.Bool("mkdir-rename-only", false, "Allow directory creation and same-parent rename only")
	mkdirRenameMoveOnly := fs.Bool("mkdir-rename-move-only", false, "Allow dir create, rename and same-library cross-parent move")
	mkdirRenameMoveFileRenameOnly := fs.Bool("mkdir-rename-move-file-rename-only", false, "Allow dir create/rename/move and file rename (same parent only)")
	mkdirRenameMoveFileRenameMoveOnly := fs.Bool("mkdir-rename-move-file-rename-move-only", false, "Allow dir create/rename/move and file rename/cross-parent move (same name only)")
	mkdirRenameMoveFileRenameMoveRemoveOnly := fs.Bool("mkdir-rename-move-file-rename-move-remove-only", false, "Allow dir create/rename/move, file rename/move and file/dir removal")
	mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly := fs.Bool("mkdir-rename-move-file-rename-move-remove-copy-upload-only", false, "Copy-to-mount upload with staged write-commit")

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	if *provider != "" && *provider != "huadian" && *provider != "mock" {
		return nil, fmt.Errorf("unknown provider %q (expected huadian or mock)", *provider)
	}
	if *provider == "" {
		*provider = "huadian"
	}

	if *provider == "mock" {
		if *mockRoot == "" {
			return nil, fmt.Errorf("--root is required when --provider=mock")
		}
		abs, err := filepath.Abs(*mockRoot)
		if err != nil {
			return nil, fmt.Errorf("--root: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return nil, fmt.Errorf("--root does not exist: %w", err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("--root must be a directory")
		}
		*mockRoot = abs
	}
	if *provider == "huadian" && *mockRoot != "" {
		return nil, fmt.Errorf("--root is only valid with --provider=mock")
	}

	if *dataDir != "" {
		abs, err := filepath.Abs(*dataDir)
		if err != nil {
			return nil, fmt.Errorf("--data-dir: %w", err)
		}
		if err := os.MkdirAll(abs, 0700); err != nil {
			return nil, fmt.Errorf("--data-dir create: %w", err)
		}
		*dataDir = abs
	}

	if *readOnly && *mkdirOnly {
		return nil, fmt.Errorf("--read-only and --mkdir-only are mutually exclusive")
	}
	if *readOnly && *mkdirRenameOnly {
		return nil, fmt.Errorf("--read-only and --mkdir-rename-only are mutually exclusive")
	}
	if *mkdirOnly && *mkdirRenameOnly {
		return nil, fmt.Errorf("--mkdir-only and --mkdir-rename-only are mutually exclusive")
	}
	if *mkdirOnly && *mkdirRenameMoveOnly {
		return nil, fmt.Errorf("--mkdir-only and --mkdir-rename-move-only are mutually exclusive")
	}
	if *mkdirRenameOnly && *mkdirRenameMoveOnly {
		return nil, fmt.Errorf("--mkdir-rename-only and --mkdir-rename-move-only are mutually exclusive")
	}
	if *readOnly && *mkdirRenameMoveFileRenameOnly {
		return nil, fmt.Errorf("--read-only and --mkdir-rename-move-file-rename-only are mutually exclusive")
	}
	if *mkdirOnly && *mkdirRenameMoveFileRenameOnly {
		return nil, fmt.Errorf("--mkdir-only and --mkdir-rename-move-file-rename-only are mutually exclusive")
	}
	if *mkdirRenameOnly && *mkdirRenameMoveFileRenameOnly {
		return nil, fmt.Errorf("--mkdir-rename-only and --mkdir-rename-move-file-rename-only are mutually exclusive")
	}
	if *mkdirRenameMoveOnly && *mkdirRenameMoveFileRenameOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-only and --mkdir-rename-move-file-rename-only are mutually exclusive")
	}
	if *readOnly && *mkdirRenameMoveFileRenameMoveOnly {
		return nil, fmt.Errorf("--read-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
	}
	if *mkdirOnly && *mkdirRenameMoveFileRenameMoveOnly {
		return nil, fmt.Errorf("--mkdir-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
	}
	if *mkdirRenameOnly && *mkdirRenameMoveFileRenameMoveOnly {
		return nil, fmt.Errorf("--mkdir-rename-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
	}
	if *mkdirRenameMoveOnly && *mkdirRenameMoveFileRenameMoveOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
	}
	if *mkdirRenameMoveFileRenameOnly && *mkdirRenameMoveFileRenameMoveOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-file-rename-only and --mkdir-rename-move-file-rename-move-only are mutually exclusive")
	}
	if *readOnly && *mkdirRenameMoveFileRenameMoveRemoveOnly {
		return nil, fmt.Errorf("--read-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
	}
	if *mkdirOnly && *mkdirRenameMoveFileRenameMoveRemoveOnly {
		return nil, fmt.Errorf("--mkdir-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
	}
	if *mkdirRenameOnly && *mkdirRenameMoveFileRenameMoveRemoveOnly {
		return nil, fmt.Errorf("--mkdir-rename-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
	}
	if *mkdirRenameMoveOnly && *mkdirRenameMoveFileRenameMoveRemoveOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
	}
	if *mkdirRenameMoveFileRenameOnly && *mkdirRenameMoveFileRenameMoveRemoveOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-file-rename-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
	}
	if *mkdirRenameMoveFileRenameMoveOnly && *mkdirRenameMoveFileRenameMoveRemoveOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-file-rename-move-only and --mkdir-rename-move-file-rename-move-remove-only are mutually exclusive")
	}
	if *readOnly && *mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return nil, fmt.Errorf("--read-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
	}
	if *mkdirOnly && *mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return nil, fmt.Errorf("--mkdir-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
	}
	if *mkdirRenameOnly && *mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return nil, fmt.Errorf("--mkdir-rename-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
	}
	if *mkdirRenameMoveOnly && *mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
	}
	if *mkdirRenameMoveFileRenameOnly && *mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-file-rename-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
	}
	if *mkdirRenameMoveFileRenameMoveOnly && *mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-file-rename-move-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
	}
	if *mkdirRenameMoveFileRenameMoveRemoveOnly && *mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly {
		return nil, fmt.Errorf("--mkdir-rename-move-file-rename-move-remove-only and --mkdir-rename-move-file-rename-move-remove-copy-upload-only are mutually exclusive")
	}

	return &runArgs{
		provider:                                    *provider,
		mockRoot:                                    *mockRoot,
		dataDir:                                     *dataDir,
		pipePath:                                    *pipePath,
		noBackground:                                *noBackground,
		readOnly:                                    *readOnly,
		mkdirOnly:                                   *mkdirOnly,
		mkdirRenameOnly:                             *mkdirRenameOnly,
		mkdirRenameMoveOnly:                         *mkdirRenameMoveOnly,
		mkdirRenameMoveFileRenameOnly:               *mkdirRenameMoveFileRenameOnly,
		mkdirRenameMoveFileRenameMoveOnly:           *mkdirRenameMoveFileRenameMoveOnly,
		mkdirRenameMoveFileRenameMoveRemoveOnly:     *mkdirRenameMoveFileRenameMoveRemoveOnly,
		mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly: *mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly,
	}, nil
}

func main() {
	flag.Usage = printDaemonUsage
	flag.Parse()

	args := flag.Args()
	cmd := "help"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "version":
		app.PrintVersion("hddsyncd")
	case "run":
		runCfg, err := parseRunArgs(args[1:])
		if err != nil {
			if errors.Is(err, flag.ErrHelp) {
				printRunUsage()
				return
			}
			fmt.Fprintf(os.Stderr, "hddsyncd run: %v\nRun 'hddsyncd run --help' for usage.\n", err)
			os.Exit(1)
		}
		runDaemon(runCfg)
	case "service":
		runServiceCmd(args[1:])
	case "help", "-h", "--help":
		printDaemonUsage()
	default:
		fmt.Fprintf(os.Stderr, "hddsyncd: unknown command %q\nRun 'hddsyncd help' for usage.\n", cmd)
		os.Exit(1)
	}
}

func printDaemonUsage() {
	out := flag.CommandLine.Output()
	out.Write([]byte("Usage: hddsyncd <command>\n\n"))
	out.Write([]byte("Commands:\n"))
	out.Write([]byte("  run                  Start the daemon (console mode)\n"))
	out.Write([]byte("  version              Print version\n"))
	out.Write([]byte("  service install      Install as Windows service\n"))
	out.Write([]byte("  service uninstall    Remove the Windows service\n"))
	out.Write([]byte("  service start        Start the Windows service\n"))
	out.Write([]byte("  service stop         Stop the Windows service\n"))
	out.Write([]byte("  help                 Print this help\n"))
	out.Write([]byte("\nRun 'hddsyncd run' to start the daemon.\n"))
	out.Write([]byte("Run 'hddsyncd run --help' for run options.\n"))
	out.Write([]byte("Data is stored in %LOCALAPPDATA%\\HuadianDrive.\n"))
	out.Write([]byte("Authentication is loaded from the credential store (hddctl login).\n"))
}

func printRunUsage() {
	out := flag.CommandLine.Output()
	out.Write([]byte("Usage: hddsyncd run [options]\n\n"))
	out.Write([]byte("Start the daemon in console mode (foreground).\n\n"))
	out.Write([]byte("Options:\n"))
	out.Write([]byte("  --provider <name>    Cloud provider: huadian (default) or mock\n"))
	out.Write([]byte("  --root <dir>         Local root directory for mock provider\n"))
	out.Write([]byte("  --data-dir <dir>     Override data directory (metadata.db + cache)\n"))
	out.Write([]byte("                       Default: %LOCALAPPDATA%\\HuadianDrive\n"))
	out.Write([]byte("  --pipe <path>        Override named pipe path\n"))
	out.Write([]byte("                       Default: \\\\.\\pipe\\huadian-drive\n"))
	out.Write([]byte("  --no-background      Disable worker pool, watcher, and initial scan\n"))
	out.Write([]byte("  --read-only                         Reject all write operations via IPC\n"))
	out.Write([]byte("  --mkdir-only                        Allow directory creation only\n"))
	out.Write([]byte("  --mkdir-rename-only                 Allow dir create and same-parent rename\n"))
	out.Write([]byte("  --mkdir-rename-move-only            Allow dir create, rename and cross-parent move\n"))
	out.Write([]byte("  --mkdir-rename-move-file-rename-only      Allow dir create/rename/move and file rename (same parent)\n"))
	out.Write([]byte("  --mkdir-rename-move-file-rename-move-only       Allow dir create/rename/move and file rename/cross-parent move\n"))
	out.Write([]byte("  --mkdir-rename-move-file-rename-move-remove-only Allow dir create/rename/move, file rename/move and removal\n"))
	out.Write([]byte("\nMock mode example:\n"))
	out.Write([]byte("  hddsyncd run --provider mock --root <dir> --data-dir <dir> --no-background\n"))
	out.Write([]byte("\nBehavior:\n"))
	out.Write([]byte("  - Opens SQLite metadata database in the data directory\n"))
	out.Write([]byte("  - huadian: loads credentials and verifies the server session\n"))
	out.Write([]byte("  - mock: no credentials, no network; requires --root\n"))
	out.Write([]byte("  - Starts the named pipe IPC server\n"))
	out.Write([]byte("  - Unless --no-background: starts worker pool, watcher, and scan\n"))
	out.Write([]byte("\nPress Ctrl+C to stop gracefully.\n"))
}

func runServiceCmd(args []string) {
	subcmd := "help"
	if len(args) > 0 {
		subcmd = args[0]
	}
	svcName := "hddsyncd"
	svcDisplay := "Huadian Drive Sync Daemon"

	var err error
	switch subcmd {
	case "install":
		err = service.Install(svcName, svcDisplay, "")
	case "uninstall":
		err = service.Uninstall(svcName)
	case "start":
		err = service.Start(svcName)
	case "stop":
		err = service.Stop(svcName)
	default:
		fmt.Fprintf(os.Stderr, "hddsyncd service: unknown subcommand %q\n", subcmd)
		os.Exit(1)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "hddsyncd service %s: %v\n", subcmd, err)
		os.Exit(2)
	}
	fmt.Printf("service %s: ok\n", subcmd)
}

type daemonState struct {
	prov     cloud.Provider
	store    *sqlite.Store
	pool     *worker.Pool
	watcher  *watch.Watcher
	cacheDir string
	sess     *auth.Session

	mu         sync.Mutex
	running    bool
	paused     bool
	startedAt  time.Time
	activeTask int
	pendingCnt int
	retryCnt   int
	failedCnt  int
	rootsCnt   int
}

func (s *daemonState) refreshCounts() {
	s.mu.Lock()
	defer s.mu.Unlock()
}

func runDaemon(cfg *runArgs) {
	state := &daemonState{startedAt: time.Now(), running: true}

	// Resolve data directory.
	var dataDir string
	if cfg.dataDir != "" {
		dataDir = cfg.dataDir
	} else {
		appData := os.Getenv("LOCALAPPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		dataDir = filepath.Join(appData, "HuadianDrive")
	}
	cacheDir := filepath.Join(dataDir, "cache")
	os.MkdirAll(cacheDir, 0700)
	state.cacheDir = cacheDir

	fmt.Fprintf(os.Stderr, "hddsyncd: data dir: %s\n", dataDir)

	// 1. Open SQLite store.
	store, err := sqlite.Open(dataDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hddsyncd: open store: %v\n", err)
		os.Exit(1)
	}
	defer store.Close()
	state.store = store

	// 1b. Clean up malformed legacy tasks.
	malformed := cleanupMalformedTasks(store)
	if malformed > 0 {
		fmt.Fprintf(os.Stderr, "hddsyncd: invalid_tasks_marked_failed=%d\n", malformed)
	}

	// 2-3. Create provider.
	var prov cloud.Provider
	if cfg.provider == "mock" {
		prov = mock.New(cfg.mockRoot)
		// Health check via local Stat — no network.
		if _, err := prov.Stat(context.Background(), "/"); err != nil {
			fmt.Fprintf(os.Stderr, "hddsyncd: mock stat root: %v\n", err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "hddsyncd: mock provider root=%s\n", cfg.mockRoot)
	} else {
		credStore, err := auth.NewFileCredentialStore("")
		if err != nil {
			fmt.Fprintf(os.Stderr, "hddsyncd: open credential store: %v\n", err)
			os.Exit(1)
		}
		mgr := auth.NewSessionManager(credStore)
		sess, err := mgr.LoadSession()
		if err != nil {
			fmt.Fprintf(os.Stderr, "hddsyncd: load session: %v — run 'hddctl login' first\n", err)
			os.Exit(1)
		}
		if sess.RootDocID == "" {
			fmt.Fprintln(os.Stderr, "hddsyncd: root docid not set — run 'hddctl login' first")
			os.Exit(1)
		}
		state.sess = sess

		huadianProv := huadian.New(sess.AccessToken)
		huadianProv.SetUserID(sess.UserID)
		huadianProv.SetRootDocID(sess.RootDocID)
		if len(sess.Cookies) > 0 {
			cookies := make([]*http.Cookie, len(sess.Cookies))
			for i, sc := range sess.Cookies {
				cookies[i] = sc.ToHTTPCookie()
			}
			huadianProv.SetCookies(cookies)
			for _, sc := range sess.Cookies {
				if sc.Name == "_csrf" && sc.Value != "" {
					huadianProv.SetCSRFToken(sc.Value)
					break
				}
			}
		}
		if err := huadianProv.Connect(context.Background()); err != nil {
			fmt.Fprintf(os.Stderr, "hddsyncd: provider connect: %v\n", err)
			os.Exit(1)
		}

		// Verify the session.
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, verifyErr := huadianProv.Stat(ctx, "/")
		cancel()
		if verifyErr != nil {
			if errors.Is(verifyErr, huadian.ErrUnauthorized) {
				fmt.Fprintf(os.Stderr, "hddsyncd: session expired — run 'hddctl login'\n")
			} else {
				fmt.Fprintf(os.Stderr, "hddsyncd: session verify failed: %v — continuing in degraded mode\n", verifyErr)
			}
		}
		prov = huadianProv

		// Reconcile upload tasks.
		rec := reconcileUploadTasks(prov, store)
		if rec > 0 {
			fmt.Fprintf(os.Stderr, "hddsyncd: reconciled_upload_tasks=%d\n", rec)
		}
	}
	state.prov = prov

	// 4-6. Start background systems unless disabled.
	if !cfg.noBackground {
		pool := worker.NewPool(store, prov, 1, nil)
		pool.Start(context.Background())
		state.pool = pool

		roots, listErr := store.ListSyncRoots()
		rootIDForPath := func(localPath string) int64 {
			for _, r := range roots {
				if rel, e := filepath.Rel(r.LocalPath, localPath); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
					return r.ID
				}
			}
			return 0
		}
		watcher := watch.New(store, func(localPath, remotePath string) {
			if _, err := store.EnqueueOrMerge(context.Background(), rootIDForPath(localPath), "upload", localPath, remotePath); err != nil {
				securelog.LogSecurityEvent(context.Background(), securelog.SecurityEvent{Operation: "watch_enqueue", ErrorClass: "store"})
			}
		})
		watcher.SetDeleteFunc(func(localPath, remotePath string) {
			if _, err := store.CancelActiveByLocalPath(context.Background(), localPath, "local_file_disappeared"); err != nil {
				securelog.LogSecurityEvent(context.Background(), securelog.SecurityEvent{Operation: "watch_cancel", ErrorClass: "store"})
			}
		})
		if listErr == nil {
			for _, r := range roots {
				if r.Enabled {
					watcher.AddRoot(r.LocalPath, r.RemotePath)
				}
			}
			state.rootsCnt = len(roots)
		}
		watcher.Start(context.Background())
		state.watcher = watcher

		for _, r := range roots {
			if !r.Enabled {
				continue
			}
			scanRoot(r, store)
		}
	}

	// 7. Context + signal handling.
	daemonCtx, daemonCancel := context.WithCancel(context.Background())
	defer daemonCancel()

	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	defer signal.Stop(ch)

	securelog.LogSecurityEvent(daemonCtx, securelog.SecurityEvent{Operation: "daemon_start"})

	// 8. Named pipe IPC.
	pipePath := npipe.PipePath
	if cfg.pipePath != "" {
		pipePath = cfg.pipePath
	}
	handler := func(req ipc.Request) ipc.Response {
		return dispatchFS(daemonCtx, req, prov, cacheDir, state, cfg.readOnly, cfg.mkdirOnly, cfg.mkdirRenameOnly, cfg.mkdirRenameMoveOnly, cfg.mkdirRenameMoveFileRenameOnly, cfg.mkdirRenameMoveFileRenameMoveOnly, cfg.mkdirRenameMoveFileRenameMoveRemoveOnly, cfg.mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly)
	}
	server := npipe.NewServer(pipePath, handler)
	go func() {
		if err := server.Serve(); err != nil {
			securelog.LogSecurityEvent(daemonCtx, securelog.SecurityEvent{Operation: "ipc_serve", ErrorClass: "ipc"})
		}
	}()

	securelog.LogSecurityEvent(daemonCtx, securelog.SecurityEvent{Operation: "daemon_ready"})
	fmt.Println("hddsyncd: ready")
	fmt.Println("Press Ctrl+C to stop.")

	select {
	case <-ch:
		securelog.LogSecurityEvent(daemonCtx, securelog.SecurityEvent{Operation: "daemon_shutdown"})
	case <-daemonCtx.Done():
	}

	// Graceful shutdown.
	if state.watcher != nil {
		state.watcher.Stop()
	}
	if state.pool != nil {
		state.pool.Shutdown()
	}
	server.Shutdown()
	state.running = false
	fmt.Println("hddsyncd: stopped")
}

func cleanupMalformedTasks(store *sqlite.Store) int {
	count := 0
	for _, taskType := range []string{"upload", "download", "remove"} {
		tasks, _ := store.ListPendingTasks(taskType, 500)
		for _, t := range tasks {
			if taskType == "upload" && (t.LocalPath == nil || t.RemotePath == nil) {
				msg := "upload task missing local_path or remote_path"
				store.UpdateTaskState(t.ID, "failed", t.RetryCount, nil, &msg)
				count++
			}
		}
	}
	return count
}

func reconcileUploadTasks(prov cloud.Provider, store *sqlite.Store) int {
	count := 0
	for _, taskType := range []string{"upload"} {
		tasks, _ := store.ListPendingTasks(taskType, 500)
		for _, t := range tasks {
			if t.RetryCount == 0 || t.RemotePath == nil || t.LocalPath == nil {
				continue
			}
			if t.LastError == nil {
				continue
			}
			le := *t.LastError
			if !strings.Contains(le, "osendupload") && !strings.Contains(le, "upload_finalize") {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			info, statErr := prov.Stat(ctx, *t.RemotePath)
			cancel()
			if statErr != nil || info.IsDir {
				continue
			}
			lInfo, localErr := os.Stat(*t.LocalPath)
			if localErr != nil {
				continue
			}
			if info.Size != lInfo.Size() {
				continue
			}
			store.CompleteTask(t.ID)
			count++
		}
	}
	return count
}

func scanRoot(r sqlite.SyncRootRow, store *sqlite.Store) {
	filepath.Walk(r.LocalPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		rel, err := filepath.Rel(r.LocalPath, path)
		if err != nil || rel == "." {
			return nil
		}
		remotePath := filepath.ToSlash(filepath.Join(r.RemotePath, rel))
		base := filepath.Base(path)
		if base == "metadata.db" || base == "metadata.db-wal" || base == "metadata.db-shm" {
			return nil
		}
		if strings.HasPrefix(base, ".") && base != "." && base != ".." {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(base, ".hdddl-") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".part") {
			return nil
		}
		if info.IsDir() {
			store.EnqueueOrMerge(context.Background(), r.ID, "mkdir", path, remotePath)
			return nil
		}
		store.EnqueueOrMerge(context.Background(), r.ID, "upload", path, remotePath)
		return nil
	})
}

func policyAllowsRequest(pol writePolicy, reqType string) bool {
	switch reqType {
	case "fs.list", "fs.stat", "fs.open", "fs.cacheDir", "status":
		return true
	case "fs.mkdir":
		return pol.canMkdir
	case "fs.rename":
		return pol.canRenameFile || pol.canMoveFile || pol.canRenameDir || pol.canMoveDir
	case "fs.create":
		return pol.canCreateFile
	case "fs.markDirty":
		return pol.canWriteFile
	case "fs.remove":
		return pol.canDeleteFile || pol.canDeleteDir
	case "fs.setattr":
		return pol.canWriteFile
	case "fs.uploadStaged":
		return pol.canCopyUploadFile
	case "fs.close":
		return true
	default:
		return true
	}
}

func dispatchFS(ctx context.Context, req ipc.Request, prov cloud.Provider, cacheDir string, state *daemonState, readOnly bool, mkdirOnly bool, mkdirRenameOnly bool, mkdirRenameMoveOnly bool, mkdirRenameMoveFileRenameOnly bool, mkdirRenameMoveFileRenameMoveOnly bool, mkdirRenameMoveFileRenameMoveRemoveOnly bool, mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly bool) ipc.Response {
	pol := writePolicyFromArgs(readOnly, mkdirOnly, mkdirRenameOnly, mkdirRenameMoveOnly, mkdirRenameMoveFileRenameOnly, mkdirRenameMoveFileRenameMoveOnly, mkdirRenameMoveFileRenameMoveRemoveOnly, mkdirRenameMoveFileRenameMoveRemoveCopyUploadOnly)
	isRestricted := pol.name != "fully_writable"

	if isRestricted && !policyAllowsRequest(pol, req.Type) {
		fmt.Fprintf(os.Stderr,
			"hddsyncd: operation=%s writePolicy=%s canRenameFile=%v canMoveFile=%v canRenameDir=%v canMoveDir=%v canMkdir=%v allowed=false result=read_only_filesystem\n",
			req.Type, pol.name, pol.canRenameFile, pol.canMoveFile, pol.canRenameDir, pol.canMoveDir, pol.canMkdir)
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "read_only_filesystem"}
	}

	if isRestricted {
		fmt.Fprintf(os.Stderr,
			"hddsyncd: operation=%s writePolicy=%s canRenameFile=%v canMoveFile=%v canRenameDir=%v canMoveDir=%v canMkdir=%v allowed=true\n",
			req.Type, pol.name, pol.canRenameFile, pol.canMoveFile, pol.canRenameDir, pol.canMoveDir, pol.canMkdir)
	}

	switch req.Type {
	case "fs.list":
		return handleFSList(ctx, req, prov)
	case "fs.stat":
		return handleFSStat(ctx, req, prov)
	case "fs.open":
		return handleFSOpen(ctx, req, prov, cacheDir)
	case "fs.create":
		return handleFSCreate(ctx, req, prov, cacheDir)
	case "fs.close":
		return handleFSClose(req, prov, isRestricted && !pol.canWriteFile)
	case "fs.markDirty":
		return handleFSMarkDirty(ctx, req, prov)
	case "fs.mkdir":
		return handleFSMkdir(ctx, req, prov)
	case "fs.rename":
		return handleFSRename(ctx, req, prov, pol)
	case "fs.remove":
		return handleFSRemove(ctx, req, prov, pol)
	case "fs.uploadStaged":
		return handleFSUploadStaged(ctx, req, prov, pol, cacheDir)
	case "fs.cacheDir":
		data, _ := json.Marshal(map[string]string{"path": cacheDir})
		return ipc.Response{Type: req.Type, ID: req.ID, Data: data}
	case "fs.setattr":
		return handleFSSetattr(ctx, req, prov)
	case "status":
		data, _ := json.Marshal(ipc.StatusData{Provider: prov.Name()})
		return ipc.Response{Type: req.Type, ID: req.ID, Data: data}
	default:
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "unknown command"}
	}
}

type fsPathReq struct {
	Path string `json:"path"`
}

func handleFSList(ctx context.Context, req ipc.Request, prov cloud.Provider) ipc.Response {
	var p fsPathReq
	if err := json.Unmarshal(req.Data, &p); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "bad request"}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	entries, err := prov.List(ctx, p.Path)
	if err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: err.Error()}
	}
	result := ipc.FSListData{Entries: make([]ipc.FSEntry, 0, len(entries))}
	for _, e := range entries {
		result.Entries = append(result.Entries, ipc.FSEntry{
			Path: e.Path, Size: e.Size, IsDir: e.IsDir,
			ModTime: e.ModTime.Format(time.RFC3339),
		})
	}
	data, _ := json.Marshal(result)
	return ipc.Response{Type: req.Type, ID: req.ID, Data: data}
}

func handleFSStat(ctx context.Context, req ipc.Request, prov cloud.Provider) ipc.Response {
	var p fsPathReq
	if err := json.Unmarshal(req.Data, &p); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "bad request"}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	info, err := prov.Stat(ctx, p.Path)
	if err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: err.Error()}
	}
	data, _ := json.Marshal(ipc.FSStatData{Entry: ipc.FSEntry{
		Path: info.Path, Size: info.Size, IsDir: info.IsDir,
		ModTime: info.ModTime.Format(time.RFC3339),
	}})
	return ipc.Response{Type: req.Type, ID: req.ID, Data: data}
}

func handleFSOpen(ctx context.Context, req ipc.Request, prov cloud.Provider, cacheDir string) ipc.Response {
	var p fsPathReq
	if err := json.Unmarshal(req.Data, &p); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "bad request"}
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	cacheName, err := safeCachePath(cacheDir, p.Path)
	if err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: err.Error()}
	}
	os.MkdirAll(filepath.Dir(cacheName), 0700)
	f, ferr := os.Create(cacheName)
	if ferr != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: ferr.Error()}
	}
	defer f.Close()
	if derr := prov.Download(ctx, p.Path, f); derr != nil {
		os.Remove(cacheName)
		return ipc.Response{Type: req.Type, ID: req.ID, Error: derr.Error()}
	}
	info, _ := os.Stat(cacheName)
	size := int64(0)
	if info != nil {
		size = info.Size()
	}
	data, _ := json.Marshal(ipc.FSOpenData{CachePath: cacheName, Size: size})
	return ipc.Response{Type: req.Type, ID: req.ID, Data: data}
}

func handleFSCreate(ctx context.Context, req ipc.Request, prov cloud.Provider, cacheDir string) ipc.Response {
	var p fsPathReq
	if err := json.Unmarshal(req.Data, &p); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "bad request"}
	}
	cacheName, err := safeCachePath(cacheDir, p.Path)
	if err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: err.Error()}
	}
	os.MkdirAll(filepath.Dir(cacheName), 0700)
	f, ferr := os.Create(cacheName)
	if ferr != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: ferr.Error()}
	}
	f.Close()
	data, _ := json.Marshal(ipc.FSCreateData{CachePath: cacheName})
	return ipc.Response{Type: req.Type, ID: req.ID, Data: data}
}

func handleFSClose(req ipc.Request, prov cloud.Provider, readOnly bool) ipc.Response {
	var p struct {
		Path  string `json:"path"`
		Dirty bool   `json:"dirty"`
	}
	json.Unmarshal(req.Data, &p)
	if p.Dirty {
		if readOnly {
			return ipc.Response{Type: req.Type, ID: req.ID, Error: "read_only_filesystem"}
		}
		if p.Path != "" {
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				f, err := os.Open(p.Path)
				if err != nil {
					return
				}
				defer f.Close()
				prov.Upload(ctx, p.Path, f)
			}()
		}
	}
	return ipc.Response{Type: req.Type, ID: req.ID}
}

func handleFSMarkDirty(ctx context.Context, req ipc.Request, prov cloud.Provider) ipc.Response {
	var p fsPathReq
	json.Unmarshal(req.Data, &p)
	if p.Path != "" {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			f, err := os.Open(p.Path)
			if err != nil {
				return
			}
			defer f.Close()
			prov.Upload(ctx, p.Path, f)
		}()
	}
	return ipc.Response{Type: req.Type, ID: req.ID}
}

func handleFSMkdir(ctx context.Context, req ipc.Request, prov cloud.Provider) ipc.Response {
	var p fsPathReq
	if err := json.Unmarshal(req.Data, &p); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "bad request"}
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := prov.Mkdir(ctx, p.Path); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: err.Error()}
	}
	return ipc.Response{Type: req.Type, ID: req.ID}
}

func handleFSRename(ctx context.Context, req ipc.Request, prov cloud.Provider, pol writePolicy) ipc.Response {
	var p ipc.FSRenameRequest
	if err := json.Unmarshal(req.Data, &p); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "bad request"}
	}
	started := time.Now()
	oldParent := remoteParent(p.OldPath)
	newParent := remoteParent(p.NewPath)
	sameParent := oldParent == newParent

	if pol.name == "fully_writable" {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		err := prov.Rename(ctx, p.OldPath, p.NewPath)
		elapsed := time.Since(started)
		if err != nil {
			fmt.Fprintf(os.Stderr,
				"hddsyncd: operation=%s writePolicy=%s oldPath=%s newPath=%s sameParent=%v providerMethod=Rename renameCalls=1 moveCalls=0 providerErr=%s elapsed=%s\n",
				"fs_rename", pol.name, p.OldPath, p.NewPath, sameParent, err.Error(), elapsed)
			return ipc.Response{Type: req.Type, ID: req.ID, Error: err.Error()}
		}
		fmt.Fprintf(os.Stderr,
			"hddsyncd: operation=%s writePolicy=%s oldPath=%s newPath=%s sameParent=%v providerMethod=Rename renameCalls=1 moveCalls=0 result=success elapsed=%s\n",
			"fs_rename", pol.name, p.OldPath, p.NewPath, sameParent, elapsed)
		return ipc.Response{Type: req.Type, ID: req.ID}
	}

	info, err := prov.Stat(ctx, p.OldPath)
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"hddsyncd: operation=%s writePolicy=%s oldPath=%s newPath=%s sameParent=%v restricted=true sourceExists=false result=enoent elapsed=%s\n",
			"fs_rename", pol.name, p.OldPath, p.NewPath, sameParent, time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID, Error: err.Error()}
	}

	if info.IsDir {
		return handleFSRenameDir(ctx, prov, pol, p, info, oldParent, newParent, sameParent, started)
	}
	return handleFSRenameFile(ctx, prov, pol, p, info, oldParent, newParent, sameParent, started)
}

func handleFSRenameDir(ctx context.Context, prov cloud.Provider, pol writePolicy, p ipc.FSRenameRequest, info domain.FileInfo, oldParent, newParent string, sameParent bool, started time.Time) ipc.Response {
	sourceType := "directory"
	logLine := func(result string, extra ...string) {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=%s sourceType=%s writePolicy=%s oldPath=%s newPath=%s oldParent=%s newParent=%s sameParent=%v %s result=%s elapsed=%s\n",
			"fs_rename", sourceType, pol.name, p.OldPath, p.NewPath, oldParent, newParent, sameParent,
			strings.Join(extra, " "), result, time.Since(started))
	}

	if p.OldPath == "/" {
		logLine("eperm_root")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "cannot rename root directory"}
	}

	if !pol.canRenameDir && !pol.canMoveDir {
		logLine("disabled_by_policy")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: directory operation not allowed in " + pol.name}
	}

	if sameParent {
		if !pol.canRenameDir {
			logLine("same_parent_disabled")
			return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: directory rename not allowed in " + pol.name}
		}
		if _, err := prov.Stat(ctx, p.NewPath); err == nil {
			logLine("eexist", "destinationExists=true")
			return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: target already exists"}
		}
		renameCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := prov.Rename(renameCtx, p.OldPath, p.NewPath); err != nil {
			logLine("provider_error", "providerMethod=Rename renameCalls=1 moveCalls=0 providerErr="+err.Error())
			return ipc.Response{Type: "fs.rename", ID: "", Error: err.Error()}
		}
		logLine("success", "providerMethod=Rename renameCalls=1 moveCalls=0")
		return ipc.Response{Type: "fs.rename", ID: ""}
	}

	if !pol.canMoveDir {
		logLine("cross_parent_disabled")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: cross-parent move not allowed in " + pol.name}
	}

	oldName := path.Base(p.OldPath)
	newName := path.Base(p.NewPath)
	sameName := oldName == newName

	if !sameName {
		logLine("cross_parent_rename_denied", "reason=cross-parent-move-with-different-base-name")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: cross-parent move with different base name is not supported in restricted mode"}
	}

	newParentInfo, statErr := prov.Stat(ctx, newParent)
	if statErr != nil {
		logLine("enoent_parent")
		return ipc.Response{Type: "fs.rename", ID: "", Error: statErr.Error()}
	}
	if !newParentInfo.IsDir {
		logLine("enotdir")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: target parent is not a directory"}
	}
	if strings.HasPrefix(p.NewPath, p.OldPath+"/") {
		logLine("einval_subtree")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: cannot move directory into itself"}
	}
	if _, err := prov.Stat(ctx, p.NewPath); err == nil {
		logLine("eexist", "destinationExists=true")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: target already exists"}
	}

	dirProv, ok := prov.(cloud.DirectRemoteProvider)
	if !ok {
		logLine("move_not_supported")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: cross-parent move is not supported by this provider"}
	}
	moveCtx, moveCancel := context.WithTimeout(ctx, 30*time.Second)
	defer moveCancel()
	if _, moveErr := dirProv.Move(moveCtx, p.OldPath, newParent, cloud.TransferConflictFail); moveErr != nil {
		logLine("move_error", "providerMethod=Move renameCalls=0 moveCalls=1 providerErr="+moveErr.Error())
		return ipc.Response{Type: "fs.rename", ID: "", Error: "move: " + moveErr.Error()}
	}

	verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer verifyCancel()
	_, oldErr := prov.Stat(verifyCtx, p.OldPath)
	newInfo, newErr := prov.Stat(verifyCtx, p.NewPath)
	if oldErr != nil && newErr == nil && newInfo.IsDir {
		logLine("success", "providerMethod=Move renameCalls=0 moveCalls=1 verifiedSuccess=true postcheckOldExists=false postcheckNewExists=true")
		return ipc.Response{Type: "fs.rename", ID: ""}
	}
	logLine("eio_verification_failed", "providerMethod=Move renameCalls=0 moveCalls=1 verifiedSuccess=false")
	return ipc.Response{Type: "fs.rename", ID: "", Error: "move: provider returned success but post-move verification failed"}
}

func handleFSRenameFile(ctx context.Context, prov cloud.Provider, pol writePolicy, p ipc.FSRenameRequest, info domain.FileInfo, oldParent, newParent string, sameParent bool, started time.Time) ipc.Response {
	sourceType := "file"
	oldSize := info.Size

	logLine := func(result string, extra ...string) {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=%s sourceType=%s writePolicy=%s oldPath=%s newPath=%s oldParent=%s newParent=%s sameParent=%v oldSize=%d %s result=%s elapsed=%s\n",
			"fs_rename", sourceType, pol.name, p.OldPath, p.NewPath, oldParent, newParent, sameParent, oldSize,
			strings.Join(extra, " "), result, time.Since(started))
	}

	if !pol.canRenameFile && !pol.canMoveFile {
		logLine("disabled_by_policy", "canRenameFile=false canMoveFile=false")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: file operation not allowed in " + pol.name}
	}

	if !sameParent {
		// Determine if this is a same-name cross-parent move or a cross-parent rename.
		oldName := path.Base(p.OldPath)
		newName := path.Base(p.NewPath)
		sameName := oldName == newName

		if !sameName {
			logLine("file_cross_parent_rename_rejected",
				fmt.Sprintf("allowed=false reason=file-cross-parent-rename-not-supported sameName=false renameCalls=0 moveCalls=0"))
			return ipc.Response{Type: "fs.rename", ID: "",
				Error: "rename: file cross-parent rename with different basename is not supported in restricted mode"}
		}

		// Same-name cross-parent move.
		if !pol.canMoveFile {
			logLine("file_cross_parent_rejected",
				"allowed=false reason=file-cross-parent-move-not-enabled renameCalls=0 moveCalls=0")
			return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: file cross-parent move is not enabled"}
		}

		// Cross-parent file move — use Move API.
		newParentInfo, statErr := prov.Stat(ctx, newParent)
		if statErr != nil {
			logLine("enoent_parent", "targetParentExists=false")
			return ipc.Response{Type: "fs.rename", ID: "", Error: statErr.Error()}
		}
		if !newParentInfo.IsDir {
			logLine("enotdir")
			return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: target parent is not a directory"}
		}
		if _, err := prov.Stat(ctx, p.NewPath); err == nil {
			logLine("eexist", "destinationExists=true")
			return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: target already exists"}
		}

		dirProv, ok := prov.(cloud.DirectRemoteProvider)
		if !ok {
			logLine("move_not_supported", "providerMethod=Move renameCalls=0 moveCalls=0")
			return ipc.Response{Type: "fs.rename", ID: "",
				Error: "rename: cross-parent move is not supported by this provider"}
		}
		moveCtx, moveCancel := context.WithTimeout(ctx, 30*time.Second)
		defer moveCancel()
		moveErr := func() error {
			_, e := dirProv.Move(moveCtx, p.OldPath, newParent, cloud.TransferConflictFail)
			return e
		}()

		if moveErr != nil {
			logLine("move_error",
				fmt.Sprintf("providerMethod=Move renameCalls=0 moveCalls=1 providerErr=%s conflict=fail", moveErr.Error()))
			// Check if move actually succeeded despite error.
			checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
			defer checkCancel()
			_, oldChk := prov.Stat(checkCtx, p.OldPath)
			newChk, newChkErr := prov.Stat(checkCtx, p.NewPath)
			pOldEx := oldChk == nil
			pNewEx := newChkErr == nil
			if !pOldEx && pNewEx && !newChk.IsDir && newChk.Size == oldSize {
				logLine("success_post_error",
					fmt.Sprintf("providerMethod=Move renameCalls=0 moveCalls=1 conflict=fail providerErr=%s postcheckOldExists=false postcheckNewExists=true verifiedSuccess=true", moveErr.Error()))
				return ipc.Response{Type: "fs.rename", ID: ""}
			}
			logLine("provider_error",
				fmt.Sprintf("providerMethod=Move renameCalls=0 moveCalls=1 conflict=fail providerErr=%s postcheckOldExists=%v postcheckNewExists=%v verifiedSuccess=false",
					moveErr.Error(), pOldEx, pNewEx))
			return ipc.Response{Type: "fs.rename", ID: "", Error: "move: " + moveErr.Error()}
		}

		// Post-move verification.
		verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
		defer verifyCancel()
		_, oldErr := prov.Stat(verifyCtx, p.OldPath)
		newInfo, newErr := prov.Stat(verifyCtx, p.NewPath)

		oldExists := oldErr == nil
		newExists := newErr == nil
		newIsFile := newExists && !newInfo.IsDir
		newSize := int64(0)
		if newIsFile {
			newSize = newInfo.Size
		}
		metadataMismatch := newIsFile && newSize != oldSize

		if !oldExists && newExists && newIsFile && newSize == oldSize {
			logLine("success",
				fmt.Sprintf("providerMethod=Move renameCalls=0 moveCalls=1 conflict=fail verifiedSuccess=true postcheckOldExists=false postcheckNewExists=true oldSize=%d newSize=%d destinationDirectory=%s",
					oldSize, newSize, newParent))
			return ipc.Response{Type: "fs.rename", ID: ""}
		}

		var reason string
		switch {
		case oldExists && !newExists:
			reason = "provider-returned-success-but-state-unchanged"
		case oldExists && newExists:
			reason = "move-postcondition-both-exist"
		case !oldExists && !newExists:
			reason = "move-postcondition-neither-exists"
		case !oldExists && newExists && !newIsFile:
			reason = "move-postcondition-type-mismatch"
		case metadataMismatch:
			reason = "move-postcondition-size-mismatch"
		default:
			reason = "unknown"
		}
		logLine("eio_verification_failed",
			fmt.Sprintf("providerMethod=Move renameCalls=0 moveCalls=1 conflict=fail verifiedSuccess=false reason=%s postcheckOldExists=%v postcheckNewExists=%v postcheckNewIsFile=%v oldSize=%d newSize=%d metadataMismatch=%v",
				reason, oldExists, newExists, newIsFile, oldSize, newSize, metadataMismatch))
		return ipc.Response{Type: "fs.rename", ID: "",
			Error: "move: provider returned success but post-move verification failed"}
	}

	if p.OldPath == p.NewPath {
		logLine("success_noop", "renameCalls=0 moveCalls=0")
		return ipc.Response{Type: "fs.rename", ID: ""}
	}

	newParentInfo, statErr := prov.Stat(ctx, newParent)
	if statErr != nil {
		logLine("enoent_parent", "targetParentExists=false")
		return ipc.Response{Type: "fs.rename", ID: "", Error: statErr.Error()}
	}
	if !newParentInfo.IsDir {
		logLine("enotdir")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: target parent is not a directory"}
	}

	if _, err := prov.Stat(ctx, p.NewPath); err == nil {
		logLine("eexist", "destinationExists=true")
		return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: target already exists"}
	}

	renameCtx, renameCancel := context.WithTimeout(ctx, 5*time.Second)
	defer renameCancel()
	renameErr := prov.Rename(renameCtx, p.OldPath, p.NewPath)

	if renameErr != nil {
		checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
		defer checkCancel()
		_, oldChk := prov.Stat(checkCtx, p.OldPath)
		newChk, newChkErr := prov.Stat(checkCtx, p.NewPath)
		pOldExist := oldChk == nil
		pNewExist := newChkErr == nil
		if !pOldExist && pNewExist && !newChk.IsDir && newChk.Size == oldSize {
			logLine("success_post_error",
				"providerMethod=Rename renameCalls=1 moveCalls=0 providerErr="+renameErr.Error()+
					" postcheckOldExists=false postcheckNewExists=true verifiedSuccess=true")
			return ipc.Response{Type: "fs.rename", ID: ""}
		}
		logLine("provider_error",
			fmt.Sprintf("providerMethod=Rename renameCalls=1 moveCalls=0 providerErr=%s postcheckOldExists=%v postcheckNewExists=%v verifiedSuccess=false",
				renameErr.Error(), pOldExist, pNewExist))
		return ipc.Response{Type: "fs.rename", ID: "", Error: renameErr.Error()}
	}

	verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer verifyCancel()
	_, oldErr := prov.Stat(verifyCtx, p.OldPath)
	newInfo, newErr := prov.Stat(verifyCtx, p.NewPath)

	oldExists := oldErr == nil
	newExists := newErr == nil
	newIsFile := newExists && !newInfo.IsDir
	newSize := int64(0)
	if newIsFile {
		newSize = newInfo.Size
	}
	metadataMismatch := newIsFile && newSize != oldSize

	if !oldExists && newExists && newIsFile && newSize == oldSize {
		logLine("success",
			fmt.Sprintf("providerMethod=Rename renameCalls=1 moveCalls=0 verifiedSuccess=true postcheckOldExists=false postcheckNewExists=true newSize=%d", newSize))
		return ipc.Response{Type: "fs.rename", ID: ""}
	}

	var reason string
	switch {
	case oldExists && !newExists:
		reason = "old_exists_new_missing"
	case oldExists && newExists:
		reason = "both_exist"
	case !oldExists && !newExists:
		reason = "neither_exists"
	case !oldExists && newExists && !newIsFile:
		reason = "new_is_not_file"
	case metadataMismatch:
		reason = "size_mismatch"
	default:
		reason = "unknown"
	}
	logLine("eio_verification_failed",
		fmt.Sprintf("providerMethod=Rename renameCalls=1 moveCalls=0 verifiedSuccess=false reason=%s postcheckOldExists=%v postcheckNewExists=%v postcheckNewIsFile=%v newSize=%d metadataMismatch=%v",
			reason, oldExists, newExists, newIsFile, newSize, metadataMismatch))
	return ipc.Response{Type: "fs.rename", ID: "", Error: "rename: provider returned success but post-rename verification failed"}
}

func handleFSRemove(ctx context.Context, req ipc.Request, prov cloud.Provider, pol writePolicy) ipc.Response {
	var p ipc.FSRemoveRequest
	if err := json.Unmarshal(req.Data, &p); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "bad request"}
	}
	started := time.Now()
	path := p.Path

	if path == "" || path == "/" {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_remove writePolicy=%s path=%s result=eperm reason=root_or_empty elapsed=%s\n",
			pol.name, path, time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "remove: cannot remove root or empty path"}
	}

	info, statErr := prov.Stat(ctx, path)
	if statErr != nil {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_remove writePolicy=%s path=%s sourceExists=false result=enoent elapsed=%s\n",
			pol.name, path, time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID, Error: statErr.Error()}
	}

	sourceType := "file"
	if info.IsDir {
		sourceType = "directory"
		if !pol.canDeleteDir {
			fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_remove writePolicy=%s path=%s sourceType=%s canDeleteDir=false canDeleteFile=%v result=eperm reason=directory_deletion_not_allowed elapsed=%s\n",
				pol.name, path, sourceType, pol.canDeleteFile, time.Since(started))
			return ipc.Response{Type: req.Type, ID: req.ID, Error: "remove: directory deletion not allowed in " + pol.name}
		}
	} else if !pol.canDeleteFile {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_remove writePolicy=%s path=%s sourceType=%s canDeleteFile=false canDeleteDir=%v result=eperm reason=file_deletion_not_allowed elapsed=%s\n",
			pol.name, path, sourceType, pol.canDeleteDir, time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "remove: file deletion not allowed in " + pol.name}
	}

	removeCtx, removeCancel := context.WithTimeout(ctx, 10*time.Second)
	defer removeCancel()
	removeErr := prov.Remove(removeCtx, path)

	if removeErr != nil {
		checkCtx, checkCancel := context.WithTimeout(ctx, 10*time.Second)
		defer checkCancel()
		_, chkErr := prov.Stat(checkCtx, path)
		postExists := chkErr == nil
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_remove writePolicy=%s path=%s sourceType=%s providerMethod=Remove removeCalls=1 providerErr=%s postcheckExists=%v verifiedSuccess=false result=provider_error elapsed=%s\n",
			pol.name, path, sourceType, removeErr.Error(), postExists, time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID, Error: removeErr.Error()}
	}

	verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer verifyCancel()
	_, verifyErr := prov.Stat(verifyCtx, path)
	postExists := verifyErr == nil

	if !postExists {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_remove writePolicy=%s path=%s sourceType=%s providerMethod=Remove removeCalls=1 postcheckExists=false verifiedSuccess=true result=success elapsed=%s\n",
			pol.name, path, sourceType, time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID}
	}

	fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_remove writePolicy=%s path=%s sourceType=%s providerMethod=Remove removeCalls=1 postcheckExists=true verifiedSuccess=false result=eio reason=verification_failed elapsed=%s\n",
		pol.name, path, sourceType, time.Since(started))
	return ipc.Response{Type: req.Type, ID: req.ID, Error: "remove: provider returned success but post-removal verification failed"}
}

func stagingRoot(cacheDir string) string { return filepath.Join(cacheDir, "staging") }

func validateStagingPath(stagingRoot, rel string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(rel))
	if filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("invalid staging path: absolute or volume path not allowed: %q", rel)
	}
	abs := filepath.Join(stagingRoot, clean)
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil && resolved != abs {
		return "", fmt.Errorf("invalid staging path: symlink resolved outside staging root: %q", rel)
	}
	relCheck, err := filepath.Rel(stagingRoot, abs)
	if err != nil || strings.HasPrefix(relCheck, "..") || filepath.IsAbs(relCheck) {
		return "", fmt.Errorf("staging path escapes staging root: %q", rel)
	}
	return abs, nil
}

func handleFSUploadStaged(ctx context.Context, req ipc.Request, prov cloud.Provider, pol writePolicy, cacheDir string) ipc.Response {
	var p ipc.FSUploadStagedRequest
	if err := json.Unmarshal(req.Data, &p); err != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "bad request"}
	}
	started := time.Now()

	stagingAbs, err := validateStagingPath(stagingRoot(cacheDir), p.StagingPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_upload_staged writePolicy=%s stagingPath=%s result=invalid_staging_path reason=%s elapsed=%s\n",
			pol.name, p.StagingPath, err.Error(), time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "invalid staging path: " + err.Error()}
	}

	info, infoErr := os.Stat(stagingAbs)
	if infoErr != nil {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_upload_staged writePolicy=%s stagingPath=%s result=staging_not_found elapsed=%s\n",
			pol.name, p.StagingPath, time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "staging file not found: " + stagingAbs}
	}
	if info.IsDir() {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "staging path is a directory"}
	}
	stagingSize := info.Size()

	// Conflict check.
	remoteInfo, statErr := prov.Stat(ctx, p.RemotePath)
	remoteExists := statErr == nil
	if remoteExists && p.ConflictPolicy == "fail" {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_upload_staged writePolicy=%s remotePath=%s stagingPath=%s conflictPolicy=fail destinationExists=true result=already_exists elapsed=%s\n",
			pol.name, p.RemotePath, p.StagingPath, time.Since(started))
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "target already exists"}
	}

	// Upload.
	f, openErr := os.Open(stagingAbs)
	if openErr != nil {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "cannot open staging file: " + openErr.Error()}
	}
	uploadCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	uploadErr := prov.Upload(uploadCtx, p.RemotePath, f)
	f.Close() // Close before cleanup so Remove can succeed on Windows.

	if uploadErr != nil {
		fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_upload_staged writePolicy=%s remotePath=%s stagingPath=%s size=%d conflictPolicy=%s providerMethod=Upload providerErr=%s result=upload_error elapsed=%s\n",
			pol.name, p.RemotePath, p.StagingPath, stagingSize, p.ConflictPolicy, uploadErr.Error(), time.Since(started))
		// Clean up staging on error.
		os.Remove(stagingAbs)
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "upload: " + uploadErr.Error()}
	}

	// Post-check.
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer verifyCancel()
	uploadedInfo, verErr := prov.Stat(verifyCtx, p.RemotePath)
	postExists := verErr == nil
	postSize := int64(0)
	verifiedSuccess := false
	if postExists && !uploadedInfo.IsDir {
		postSize = uploadedInfo.Size
		verifiedSuccess = postSize == stagingSize
	}

	result := "success"
	if !verifiedSuccess {
		result = "verification_failed"
	}

	finalPath := p.RemotePath
	if p.ConflictPolicy == "auto_rename" && remoteExists && remoteInfo.Size > 0 {
		// If auto_rename was requested but daemon receives the renamed path from hddfs,
		// just confirm it.
		finalPath = p.RemotePath
	}

	fmt.Fprintf(os.Stderr, "hddsyncd: operation=fs_upload_staged writePolicy=%s remotePath=%s stagingPath=%s size=%d conflictPolicy=%s providerMethod=Upload postcheckExists=%v postcheckSize=%d finalPath=%s verifiedSuccess=%v result=%s elapsed=%s\n",
		pol.name, p.RemotePath, p.StagingPath, stagingSize, p.ConflictPolicy, postExists, postSize, finalPath, verifiedSuccess, result, time.Since(started))

	// Clean up staging.
	os.Remove(stagingAbs)

	if !verifiedSuccess {
		return ipc.Response{Type: req.Type, ID: req.ID, Error: "upload: verification failed"}
	}

	respData, _ := json.Marshal(ipc.FSUploadStagedResponse{RemotePath: finalPath, Size: postSize})
	return ipc.Response{Type: req.Type, ID: req.ID, Data: respData}
}

func handleFSSetattr(ctx context.Context, req ipc.Request, prov cloud.Provider) ipc.Response {
	return ipc.Response{Type: req.Type, ID: req.ID}
}

func remoteParent(p string) string {
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")
	if p == "" {
		return "/"
	}
	idx := strings.LastIndexByte(p, '/')
	if idx <= 0 {
		return "/"
	}
	return "/" + p[:idx]
}

func safeCachePath(cacheDir, remotePath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(remotePath))
	if filepath.IsAbs(clean) {
		return "", fmt.Errorf("path must be relative: %q", remotePath)
	}
	if filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("path contains volume: %q", remotePath)
	}
	abs := filepath.Join(cacheDir, clean)
	rel, err := filepath.Rel(cacheDir, abs)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", fmt.Errorf("path escapes cache dir: %q", remotePath)
	}
	return abs, nil
}

var _ = bytes.NewReader
var _ = io.Copy

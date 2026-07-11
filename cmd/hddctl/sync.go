package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"ncepupan/hdd/internal/cloud"
	"ncepupan/hdd/internal/cloud/anyshare/auth"
	"ncepupan/hdd/internal/cloud/huadian"
	"ncepupan/hdd/internal/cloud/mock"
	"ncepupan/hdd/internal/store/sqlite"
	"ncepupan/hdd/internal/watch"
	"ncepupan/hdd/internal/worker"
)

// cmdSyncAdd registers a sync root.
func cmdSyncAdd(args []string) error {
	if len(args) != 2 {
		return usageError("sync add <local-dir> <remote-path>")
	}
	localPath, err := filepath.Abs(args[0])
	if err != nil {
		return fmt.Errorf("resolve local path: %w", err)
	}
	remotePath := args[1]

	store, err := openSyncStore()
	if err != nil {
		return err
	}
	defer store.Close()

	id, err := store.InsertSyncRoot(localPath, remotePath)
	if err != nil {
		return fmt.Errorf("add sync root: %w", err)
	}
	fmt.Printf("sync root added: id=%d local=%s remote=%s\n", id, localPath, remotePath)
	return nil
}

// cmdSyncRemove removes a sync root by ID.
func cmdSyncRemove(args []string) error {
	if len(args) != 1 {
		return usageError("sync remove <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}
	store, err := openSyncStore()
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.DeleteSyncRoot(id); err != nil {
		return fmt.Errorf("remove sync root: %w", err)
	}
	fmt.Printf("sync root %d removed\n", id)
	return nil
}

// cmdSyncEnable enables a sync root.
func cmdSyncEnable(args []string) error {
	if len(args) != 1 {
		return usageError("sync enable <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}
	store, err := openSyncStore()
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.UpdateSyncRootEnabled(id, true); err != nil {
		return fmt.Errorf("enable sync root: %w", err)
	}
	fmt.Printf("sync root %d enabled\n", id)
	return nil
}

// cmdSyncDisable disables a sync root.
func cmdSyncDisable(args []string) error {
	if len(args) != 1 {
		return usageError("sync disable <id>")
	}
	id, err := strconv.ParseInt(args[0], 10, 64)
	if err != nil {
		return fmt.Errorf("invalid id: %w", err)
	}
	store, err := openSyncStore()
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.UpdateSyncRootEnabled(id, false); err != nil {
		return fmt.Errorf("disable sync root: %w", err)
	}
	fmt.Printf("sync root %d disabled\n", id)
	return nil
}

// cmdSyncRun starts watching and syncing.
func cmdSyncRun(args []string) error {
	_ = args
	store, err := openSyncStore()
	if err != nil {
		return err
	}
	defer store.Close()

	// Load credentials.
	credStore, credErr := auth.NewFileCredentialStore("")
	var prov cloud.Provider
	if credErr == nil {
		mgr := auth.NewSessionManager(credStore)
		if sess, err := mgr.LoadSession(); err == nil && sess.RootDocID != "" {
			p := huadian.New(sess.AccessToken)
			p.SetUserID(sess.UserID)
			p.SetRootDocID(sess.RootDocID)
			if len(sess.Cookies) > 0 {
				cookies := make([]*http.Cookie, len(sess.Cookies))
				for i, sc := range sess.Cookies {
					cookies[i] = sc.ToHTTPCookie()
				}
				p.SetCookies(cookies)
			}
			p.Connect(context.Background())
			prov = p
		}
	}
	if prov == nil {
		// Fallback to mock if no credentials.
		mockDir, _ := os.MkdirTemp("", "hddctl-sync-*")
		defer os.RemoveAll(mockDir)
		prov = mock.New(mockDir)
		prov.Mkdir(context.Background(), "/")
	}

	// Load sync roots.
	roots, err := store.ListSyncRoots()
	if err != nil {
		return fmt.Errorf("list sync roots: %w", err)
	}
	if len(roots) == 0 {
		return fmt.Errorf("no sync roots configured; use 'hddctl sync add' first")
	}

	// Start worker pool.
	pool := worker.NewPool(store, prov, 1, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Trap Ctrl+C.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	// Start watcher.
	w := watch.New(store, func(localPath, remotePath string) {
		fmt.Printf("[watch] changed: %s -> %s\n", localPath, remotePath)
		var rootID int64
		for _, r := range roots {
			if rel, e := filepath.Rel(r.LocalPath, localPath); e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				rootID = r.ID
				break
			}
		}
		if _, err := store.EnqueueOrMerge(context.Background(), rootID, "upload", localPath, remotePath); err != nil {
			fmt.Fprintf(os.Stderr, "[watch] enqueue: %v\n", err)
		}
	})
	w.SetDeleteFunc(func(localPath, remotePath string) {
		if _, err := store.CancelActiveByLocalPath(context.Background(), localPath, "local_file_disappeared"); err != nil {
			fmt.Fprintf(os.Stderr, "[watch] cancel: %v\n", err)
		}
	})
	for _, r := range roots {
		if r.Enabled {
			w.AddRoot(r.LocalPath, r.RemotePath)
		}
	}

	go pool.Start(ctx)
	go w.Start(ctx)

	fmt.Println("sync running (Ctrl+C to stop)")
	<-sigCh
	fmt.Println("\nshutting down...")

	cancel()
	pool.Shutdown()
	w.Stop()

	return nil
}

// cmdSyncList shows configured sync roots.
func cmdSyncList(args []string) error {
	_ = args
	store, err := openSyncStore()
	if err != nil {
		return err
	}
	defer store.Close()

	roots, _ := store.ListSyncRoots()
	if len(roots) == 0 {
		fmt.Println("No sync roots configured. Use 'hddctl sync add' to add one.")
		return nil
	}
	fmt.Printf("%-4s %-8s %-50s %-30s\n", "ID", "Enabled", "Local Path", "Remote Path")
	for _, r := range roots {
		enabled := "no"
		if r.Enabled {
			enabled = "yes"
		}
		fmt.Printf("%-4d %-8s %-50s %-30s\n", r.ID, enabled, r.LocalPath, r.RemotePath)
	}
	return nil
}

// cmdSyncTasks shows recent tasks with optional state filter.
func cmdSyncTasks(args []string) error {
	fs := flag.NewFlagSet("sync tasks", flag.ContinueOnError)
	stateFilter := fs.String("state", "", "filter by task state")
	verbose := fs.Bool("verbose", false, "show full task details")
	limit := fs.Int("limit", 100, "maximum tasks to display")
	if err := fs.Parse(args); err != nil {
		return err
	}
	valid := map[string]bool{"pending": true, "running": true, "retry_wait": true, "blocked_auth": true, "succeeded": true, "failed": true, "cancelled": true, "needs_reconcile": true}
	if *stateFilter != "" && !valid[*stateFilter] {
		return fmt.Errorf("invalid state %q", *stateFilter)
	}
	if *limit < 1 {
		return fmt.Errorf("limit must be positive")
	}
	if *limit > sqlite.MaxTaskQueryLimit {
		*limit = sqlite.MaxTaskQueryLimit
	}

	store, err := openSyncStore()
	if err != nil {
		return err
	}
	defer store.Close()

	query := sqlite.TaskQuery{Limit: *limit, IncludeTerminal: true}
	if *stateFilter != "" {
		query.States = []sqlite.TaskState{sqlite.TaskState(*stateFilter)}
	}
	tasks, err := store.ListTasks(context.Background(), query)
	if err != nil {
		return err
	}
	for i, t := range tasks {
		if i == 0 {
			if *verbose {
				fmt.Printf("%-4s %-6s %-10s %-15s %-5s %-20s %-20s %-20s %-24s %-24s %-24s %s\n", "ID", "Root", "Type", "State", "Att", "Local", "Remote", "Error class", "Last error", "Next run", "Updated", "Completed")
			} else {
				fmt.Printf("%-4s %-10s %-15s %-5s %-30s %-30s %s\n", "ID", "Type", "State", "Att", "Local", "Remote", "Error")
			}
		}
		nextStr := "-"
		if t.NextRetryAt != nil {
			nextStr = time.Unix(*t.NextRetryAt, 0).Format("15:04:05")
		}
		localStr := "-"
		if t.LocalPath != nil {
			localStr = *t.LocalPath
			if !*verbose {
				localStr = filepath.Base(localStr)
			}
		}
		remoteStr := "-"
		if t.RemotePath != nil {
			remoteStr = *t.RemotePath
		}
		errStr := "-"
		if t.LastError != nil && *t.LastError != "" {
			e := *t.LastError
			if len(e) > 40 {
				e = e[:37] + "..."
			}
			errStr = e
		}
		if *verbose {
			ec := "-"
			if t.ErrorClass != nil {
				ec = *t.ErrorClass
			}
			completed := "-"
			if t.CompletedAt != nil {
				completed = time.Unix(*t.CompletedAt, 0).Format(time.RFC3339)
			}
			fmt.Printf("%-4d %-6d %-10s %-15s %-5d %-20s %-20s %-20s %-24s %-24s %-24s %s\n", t.ID, t.SyncRootID, t.TaskType, t.Status, t.Attempts, localStr, remoteStr, ec, errStr, nextStr, time.Unix(t.UpdatedAt, 0).Format(time.RFC3339), completed)
		} else {
			fmt.Printf("%-4d %-10s %-15s %-5d %-30s %-30s %s\n", t.ID, t.TaskType, t.Status, t.Attempts, localStr, remoteStr, errStr)
		}
	}
	if len(tasks) == 0 {
		if *stateFilter != "" {
			fmt.Printf("No %s tasks.\n", *stateFilter)
		} else {
			fmt.Println("No tasks.")
		}
	}
	return nil
}

// cmdSyncStatus shows sync state.
func cmdSyncStatus(args []string) error {
	_ = args
	store, err := openSyncStore()
	if err != nil {
		return err
	}
	defer store.Close()

	roots, _ := store.ListSyncRoots()
	fmt.Printf("sync roots: %d\n", len(roots))
	for _, r := range roots {
		status := "disabled"
		if r.Enabled {
			status = "enabled"
		}
		fmt.Printf("  [%d] [%s] %s -> %s\n", r.ID, status, r.LocalPath, r.RemotePath)
	}

	for _, taskType := range []string{"upload", "download", "remove"} {
		tasks, _ := store.ListPendingTasks(taskType, 100)
		if len(tasks) > 0 {
			fmt.Printf("pending %s: %d\n", taskType, len(tasks))
		}
	}
	return nil
}

// openSyncStore returns a Store at the local app data location.
func openSyncStore() (*sqlite.Store, error) {
	appData := os.Getenv("LOCALAPPDATA")
	if appData == "" {
		appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
	}
	dir := filepath.Join(appData, "HuadianDrive")
	return sqlite.Open(dir)
}

// hddctl is the command-line client for the Huadian Drive cloud storage service.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"

	"ncepupan/hdd/internal/app"
	"ncepupan/hdd/internal/cloud"
	"ncepupan/hdd/internal/cloud/anyshare/auth"
	"ncepupan/hdd/internal/cloud/huadian"
	"ncepupan/hdd/internal/cloud/mock"
)

var consoleLogin = flag.Bool("console", false, "Use console (paste token) login instead of browser")
var useMock = flag.Bool("mock", false, "Use mock provider instead of real AnyShare cloud")
var useCDP = flag.Bool("cdp", false, "Use browser CDP proxy for API calls (bypasses WAF)")
var helpFlag = flag.Bool("help", false, "Print help")

func main() {
	flag.Usage = printUsage
	flag.Parse()

	if *helpFlag {
		printUsage()
		os.Exit(0)
	}

	args := flag.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(1)
	}

	cmd := args[0]
	switch cmd {
	case "version", "-V", "--version":
		app.PrintVersion("hddctl")
	case "login":
		cmdLogin()
		return
	case "logout":
		cmdLogout()
		return
	case "auth":
		cmdAuth(args[1:])
		return
	case "help":
		printUsage()
	case "sync":
		os.Exit(runSyncCmd(args[1:]))
	case "rule":
		os.Exit(runRuleCmd(args[1:]))
	case "remote":
		os.Exit(runRemoteCmd(args[1:]))
	case "daemon-probe":
		os.Exit(runDaemonProbeCmd(args[1:]))
	default:
		fmt.Fprintf(os.Stderr, "hddctl: unknown command %q\nRun 'hddctl help' for usage.\n", cmd)
		os.Exit(1)
	}
}

func printUsage() {
	out := flag.CommandLine.Output()
	out.Write([]byte("Usage: hddctl <command> [args]\n\n"))
	out.Write([]byte("Commands:\n"))
	out.Write([]byte("  login              Authenticate with AnyShare\n"))
	out.Write([]byte("  logout             Remove saved credentials\n"))
	out.Write([]byte("  auth status        Show authentication status\n"))
	out.Write([]byte("  auth diagnose      Diagnose session state (no secrets)\n"))
	out.Write([]byte("  remote             Direct cloud operations\n"))
	out.Write([]byte("  sync add|run|status Manage sync roots\n"))
	out.Write([]byte("  rule list|test     Filter rules\n"))
	out.Write([]byte("  version            Print version\n"))
	out.Write([]byte("  help               Print this help\n"))
	out.Write([]byte("\nRun \"hddctl remote\" for remote subcommands.\n"))
}

func printRemoteUsage() {
	out := flag.CommandLine.Output()
	out.Write([]byte("Usage: hddctl remote <subcommand> [args]\n\n"))
	out.Write([]byte("Remote subcommands:\n"))
	out.Write([]byte("  ls <path>          List directory contents\n"))
	out.Write([]byte("  stat <path>        Show file or directory metadata\n"))
	out.Write([]byte("  download <remote> [local]  Download a file\n"))
	out.Write([]byte("  upload <local-file> <remote-file> [--conflict fail|auto-rename|overwrite]\n"))
	out.Write([]byte("  upload-dir <local-directory> <remote-parent-directory>\n"))
	out.Write([]byte("    existing destination root is rejected; never merges or overwrites (--conflict fail remains accepted)\n"))
	out.Write([]byte("  copy <source-path> <destination-directory> [--conflict fail|auto-rename|overwrite]\n"))
	out.Write([]byte("    file: fail|auto-rename|overwrite; directory: auto-rename (default)\n"))
	out.Write([]byte("  mkdir <path>       Create a directory\n"))
	out.Write([]byte("  rename <old> <new> Rename a file or directory (same parent)\n"))
	out.Write([]byte("  move <source-path> <destination-directory> [--conflict fail|auto-rename|overwrite|merge]\n"))
	out.Write([]byte("    file: fail|auto-rename|overwrite; directory: fail|merge (default fail)\n"))
	out.Write([]byte("  remove|delete|rm <path>  Delete a file or empty directory\n"))
}

func printRemoteSubcommandUsage(sub string) {
	usage := map[string]string{
		"upload":     "hddctl remote upload <local-file> <remote-file> [--conflict fail|auto-rename|overwrite]",
		"upload-dir": "hddctl remote upload-dir <local-directory> <remote-parent-directory> (existing root rejected; --conflict fail accepted for compatibility)",
		"copy":       "hddctl remote copy <source-path> <destination-directory> [--conflict fail|auto-rename|overwrite] (file: all; directory: auto-rename only, default auto-rename)",
		"move":       "hddctl remote move <source-path> <destination-directory> [--conflict fail|auto-rename|overwrite|merge] (file: fail|auto-rename|overwrite; directory: fail|merge)",
		"rename":     "hddctl remote rename <old-path> <new-path>",
	}
	if line, ok := usage[sub]; ok {
		fmt.Fprintln(flag.CommandLine.Output(), "Usage:", line)
		return
	}
	printRemoteUsage()
}

func printSyncUsage() {
	out := flag.CommandLine.Output()
	out.Write([]byte("Usage: hddctl sync <subcommand> [args]\n\n"))
	out.Write([]byte("Sync subcommands:\n"))
	out.Write([]byte("  add <local> <remote>  Add a sync root\n"))
	out.Write([]byte("  list                  List configured sync roots\n"))
	out.Write([]byte("  remove <id>           Remove a sync root\n"))
	out.Write([]byte("  enable <id>           Enable a sync root\n"))
	out.Write([]byte("  disable <id>          Disable a sync root\n"))
	out.Write([]byte("  status                Show sync status (daemon or local)\n"))
	out.Write([]byte("  tasks                 Show recent tasks\n"))
	out.Write([]byte("  tasks --state <state>  Filter tasks by state\n"))
	out.Write([]byte("  tasks [--verbose] [--limit N]  Control task detail and count\n"))
	out.Write([]byte("  run                   Start foreground sync watcher\n"))
}

// runSyncCmd handles sync subcommands (sync add, sync run, sync status).
func runSyncCmd(args []string) int {
	if len(args) == 0 {
		printSyncUsage()
		return 0
	}
	sub := args[0]
	if sub == "help" || sub == "--help" || sub == "-h" {
		printSyncUsage()
		return 0
	}
	switch args[0] {
	case "add":
		if err := cmdSyncAdd(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl sync add:", err)
			return 1
		}
	case "run":
		if err := cmdSyncRun(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl sync run:", err)
			return 1
		}
	case "status":
		if err := cmdSyncStatus(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl sync status:", err)
			return 1
		}
	case "remove", "rm":
		if err := cmdSyncRemove(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl sync remove:", err)
			return 1
		}
	case "enable":
		if err := cmdSyncEnable(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl sync enable:", err)
			return 1
		}
	case "disable":
		if err := cmdSyncDisable(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl sync disable:", err)
			return 1
		}
	case "list":
		if err := cmdSyncList(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl sync list:", err)
			return 1
		}
	case "tasks":
		if err := cmdSyncTasks(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl sync tasks:", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "hddctl sync: unknown subcommand %q\n", args[0])
		return 1
	}
	return 0
}

// runRuleCmd handles rule subcommands.
func runRuleCmd(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "hddctl rule: missing subcommand (list, test)")
		return 1
	}
	switch args[0] {
	case "list":
		if err := cmdRuleList(); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl rule list:", err)
			return 1
		}
	case "test":
		if err := cmdRuleTest(args[1:]); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl rule test:", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "hddctl rule: unknown subcommand %q\n", args[0])
		return 1
	}
	return 0
}

// runRemoteCmd handles remote subcommands.
// Default: real AnyShare cloud (requires login first).
// Use --mock for local mock filesystem.
func runRemoteCmd(args []string) int {
	if len(args) == 0 {
		printRemoteUsage()
		return 0
	}
	sub := args[0]
	if sub == "help" || sub == "--help" || sub == "-h" {
		printRemoteUsage()
		return 0
	}
	if len(args) > 1 && (args[1] == "--help" || args[1] == "-h") {
		printRemoteSubcommandUsage(sub)
		return 0
	}

	var prov cloud.Provider

	if *useMock {
		dir, err := os.MkdirTemp("", "hddctl-remote-*")
		if err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote:", err)
			return 1
		}
		defer os.RemoveAll(dir)
		prov = mock.New(dir)
	} else {
		store, err := auth.NewFileCredentialStore("")
		if err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote: load credentials:", err)
			return 1
		}
		mgr := auth.NewSessionManager(store)
		sess, err := mgr.LoadSession()
		if err != nil {
			fmt.Fprintf(os.Stderr, "hddctl remote: not authenticated. Run 'hddctl login' first.\n")
			return 1
		}
		prov = huadian.New(sess.AccessToken)
		if sess.UserID != "" {
			prov.(*huadian.Provider).SetUserID(sess.UserID)
		}
		if sess.RootDocID != "" {
			prov.(*huadian.Provider).SetRootDocID(sess.RootDocID)
		}
		if len(sess.Cookies) > 0 {
			cookies := make([]*http.Cookie, len(sess.Cookies))
			for i, sc := range sess.Cookies {
				cookies[i] = sc.ToHTTPCookie()
			}
			prov.(*huadian.Provider).SetCookies(cookies)
			// Extract CSRF token from _csrf cookie
			for _, sc := range sess.Cookies {
				if sc.Name == "_csrf" && sc.Value != "" {
					prov.(*huadian.Provider).SetCSRFToken(sc.Value)
					break
				}
			}
		}
		if err := prov.Connect(context.Background()); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote: connect:", err)
			return 1
		}
	}
	return runRemoteCmdWithProvider(prov, args)
}

func runRemoteCmdWithProvider(prov cloud.Provider, args []string) int {
	sub := args[0]
	rest := args[1:]

	switch sub {
	case "ls":
		if err := cmdRemoteLs(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote ls:", err)
			return 1
		}
	case "stat":
		if err := cmdRemoteStat(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote stat:", err)
			return 1
		}
	case "mkdir":
		if err := cmdRemoteMkdir(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote mkdir:", err)
			return 1
		}
	case "upload":
		if err := cmdRemoteUpload(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote upload:", err)
			return 1
		}
	case "upload-dir":
		if err := cmdRemoteUploadDirectory(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote upload-dir:", err)
			return 1
		}
	case "copy":
		if err := cmdRemoteCopy(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote copy:", err)
			return 1
		}
	case "download":
		if err := cmdRemoteDownload(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote download:", err)
			return 1
		}
	case "mv", "rename":
		if err := cmdRemoteRename(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote rename:", err)
			return 1
		}
	case "move":
		if err := cmdRemoteMove(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote move:", err)
			return 1
		}
	case "rm", "remove", "delete":
		if err := cmdRemoteRm(prov, rest); err != nil {
			fmt.Fprintln(os.Stderr, "hddctl remote rm:", err)
			return 1
		}
	default:
		fmt.Fprintf(os.Stderr, "hddctl remote: unknown subcommand %q\nRun \"hddctl remote\" for usage.\n", sub)
		return 1
	}
	return 0
}

// ---------------------------------------------------------------------------
// login / logout / auth
// ---------------------------------------------------------------------------

func cmdLogin() {
	store, err := auth.NewFileCredentialStore("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hddctl login: create credential store: %v\n", err)
		os.Exit(1)
	}
	mgr := auth.NewSessionManager(store)
	if *consoleLogin {
		mgr.SetLoginUI(auth.NewConsoleLoginUI())
	}
	if err := mgr.LoginInteractive(); err != nil {
		fmt.Fprintf(os.Stderr, "hddctl login: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Login successful. Credential saved.")
}

func cmdLogout() {
	store, err := auth.NewFileCredentialStore("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hddctl logout: %v\n", err)
		os.Exit(1)
	}
	mgr := auth.NewSessionManager(store)
	if !mgr.HasSession() {
		fmt.Println("Not currently authenticated.")
		return
	}
	if err := mgr.InvalidateSession(); err != nil {
		fmt.Fprintf(os.Stderr, "hddctl logout: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Logged out. Credential removed.")
}

func cmdAuth(args []string) {
	sub := "status"
	if len(args) > 0 {
		sub = args[0]
	}

	store, err := auth.NewFileCredentialStore("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "hddctl auth: %v\n", err)
		os.Exit(1)
	}
	mgr := auth.NewSessionManager(store)

	switch sub {
	case "status":
		fmt.Print(mgr.Status().String())
		if !mgr.Status().Authenticated {
			os.Exit(1)
		}
	case "diagnose":
		if err := mgr.Diagnose(); err != nil {
			fmt.Fprintf(os.Stderr, "hddctl auth diagnose: %v\n", err)
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "hddctl auth: unknown subcommand %q\n", sub)
		os.Exit(1)
	}
}

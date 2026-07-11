package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"ncepupan/hdd/internal/ipc"
	"ncepupan/hdd/internal/platform/windows/npipe"
)

func runDaemonProbeCmd(args []string) int {
	pipePath := npipe.PipePath
	var probePath string
	var mkdirPath string
	confirmWrite := false

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--pipe":
			if i+1 < len(args) {
				pipePath = args[i+1]
				i++
			}
		case "--path":
			if i+1 < len(args) {
				probePath = args[i+1]
				i++
			}
		case "--mkdir":
			if i+1 < len(args) {
				mkdirPath = args[i+1]
				i++
			}
		case "--confirm-mock-write":
			confirmWrite = true
		case "--help", "-h":
			fmt.Printf("Usage: hddctl daemon-probe [options]\n\n")
			fmt.Printf("Options:\n")
			fmt.Printf("  --pipe <path>     Named pipe path (default: %s)\n", npipe.PipePath)
			fmt.Printf("  --path <path>     Remote path for stat and list (default: /)\n")
			fmt.Printf("  --mkdir <path>    Create a remote directory (requires --confirm-mock-write)\n")
			fmt.Printf("  --confirm-mock-write  Allow fs.mkdir only if provider is mock\n")
			return 0
		default:
			fmt.Fprintf(os.Stderr, "hddctl daemon-probe: unknown flag %q\n", args[i])
			return 1
		}
	}

	if probePath == "" {
		probePath = "/"
	}

	conn, err := npipe.Dial(pipePath, 3*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon-probe: dial failed: %v\n", err)
		return 1
	}
	defer conn.Close()

	// status
	start := time.Now()
	resp, err := conn.Call(ipc.Request{Type: "status", ID: "probe-status"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon-probe: status failed: %v\n", err)
		return 1
	}
	var statusData ipc.StatusData
	json.Unmarshal(resp.Data, &statusData)
	fmt.Printf("status: ok provider=%q (%v)\n", statusData.Provider, time.Since(start).Round(time.Millisecond))

	// stat
	start = time.Now()
	statPayload, _ := json.Marshal(map[string]string{"path": probePath})
	resp, err = conn.Call(ipc.Request{Type: "fs.stat", ID: "probe-stat", Data: statPayload})
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon-probe: stat(%q) failed: %v\n", probePath, err)
		return 1
	}
	var statData ipc.FSStatData
	json.Unmarshal(resp.Data, &statData)
	fmt.Printf("stat(%q): ok is_dir=%v size=%d (%v)\n", probePath, statData.Entry.IsDir, statData.Entry.Size, time.Since(start).Round(time.Millisecond))

	// list
	start = time.Now()
	listPayload, _ := json.Marshal(map[string]string{"path": probePath})
	resp, err = conn.Call(ipc.Request{Type: "fs.list", ID: "probe-list", Data: listPayload})
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon-probe: list(%q) failed: %v\n", probePath, err)
		return 1
	}
	var listData ipc.FSListData
	json.Unmarshal(resp.Data, &listData)
	fmt.Printf("list(%q): ok entries=%d (%v)\n", probePath, len(listData.Entries), time.Since(start).Round(time.Millisecond))

	// mkdir (only if explicitly requested)
	if mkdirPath != "" {
		if !confirmWrite {
			fmt.Fprintf(os.Stderr, "daemon-probe: --mkdir requires --confirm-mock-write\n")
			return 1
		}
		if statusData.Provider != "mock" {
			fmt.Fprintf(os.Stderr, "daemon-probe: refusing fs.mkdir — provider is %q, not mock\n", statusData.Provider)
			return 1
		}
		start = time.Now()
		mkdirPayload, _ := json.Marshal(map[string]string{"path": mkdirPath})
		resp, err = conn.Call(ipc.Request{Type: "fs.mkdir", ID: "probe-mkdir", Data: mkdirPayload})
		if err != nil {
			fmt.Fprintf(os.Stderr, "daemon-probe: mkdir(%q) failed: %v\n", mkdirPath, err)
			return 1
		}
		if resp.Error != "" {
			fmt.Fprintf(os.Stderr, "daemon-probe: mkdir(%q) error: %s\n", mkdirPath, resp.Error)
			return 1
		}
		fmt.Printf("mkdir(%q): ok (%v)\n", mkdirPath, time.Since(start).Round(time.Millisecond))
	}

	return 0
}

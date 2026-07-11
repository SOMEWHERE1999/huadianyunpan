//go:build windows

package auth

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	cdpPort      = 9224
	cdpTimeout   = 120 * time.Second
	pollInterval = 2 * time.Second
	loginURL     = "https://pan.ncepu.edu.cn"
)

// cdpLoginState holds per-login data captured from CDP network events.
type cdpLoginState struct {
	apiToken  string
	rootDocID string
}

type CDPLoginUI struct {
	browserPath string
}

func NewCDPLoginUI() *CDPLoginUI {
	return &CDPLoginUI{browserPath: findChrome()}
}

func findChrome() string {
	for _, p := range []string{
		`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		`C:\Program Files\Google\Chrome\Application\chrome.exe`,
		`C:\Program Files (x86)\Google\Chrome\Application\chrome.exe`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	exe, _ := os.Executable()
	for base := filepath.Dir(exe); base != filepath.Dir(base); base = filepath.Dir(base) {
		cp := filepath.Join(base, ".tools", "chromium", "chrome-win", "chrome.exe")
		if _, err := os.Stat(cp); err == nil {
			return cp
		}
	}
	if cwd, _ := os.Getwd(); cwd != "" {
		cp := filepath.Join(cwd, ".tools", "chromium", "chrome-win", "chrome.exe")
		if _, err := os.Stat(cp); err == nil {
			return cp
		}
	}
	return "msedge.exe"
}

func (ui *CDPLoginUI) Login(ctx context.Context) (*Session, error) {
	dataDir, err := os.MkdirTemp("", "hddfs-cdp-*")
	if err != nil {
		return nil, fmt.Errorf("cdp tempdir: %w", err)
	}
	defer os.RemoveAll(dataDir)

	killEdgeOnPort(cdpPort)

	cmd := exec.Command(ui.browserPath,
		"--remote-debugging-port="+fmt.Sprint(cdpPort),
		"--user-data-dir="+dataDir,
		"--no-first-run",
		"--no-default-browser-check",
		loginURL,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: false, CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("cdp start browser: %w", err)
	}
	defer func() { cmd.Process.Kill(); cmd.Process.Release() }()

	target, err := waitForCDP(cdpPort, 10*time.Second)
	if err != nil {
		return nil, fmt.Errorf("cdp connect: %w", err)
	}

	fmt.Println("Browser opened. Complete CAS login in the browser window.")
	fmt.Println("Waiting for login completion...")

	ctx, cancel := context.WithTimeout(ctx, cdpTimeout)
	defer cancel()

	state := &cdpLoginState{}
	cdpConn, cookies, err := pollForCookies(ctx, target.WebSocketDebuggerURL, pollInterval, state)
	if err != nil {
		return nil, fmt.Errorf("cdp login: %w", err)
	}
	// After login, wait briefly for the browser to send dir/list
	// so we can capture the root docid from its postData.
	captureRootDocID(ctx, cdpConn)
	cdpConn.Close()

	// Convert to StoredCookie using proper conversion that preserves all fields
	sess := &Session{Cookies: make([]StoredCookie, 0, len(cookies))}
	for _, c := range cookies {
		sc := FromHTTPCookie(c)
		sess.Cookies = append(sess.Cookies, sc)
	}
	if state.apiToken != "" {
		sess.AccessToken = state.apiToken
	}
	if state.rootDocID != "" {
		sess.RootDocID = state.rootDocID
	}
	fmt.Fprintf(os.Stderr, "[auth] cdp api_token_present=%v root_doc_id_present=%v\n",
		state.apiToken != "", state.rootDocID != "")
	return sess, nil
}

// captureRootDocID waits for the browser to issue a dir/list API call
// after login completes and extracts the root docid from the request
// body. It returns when the docid is found or a timeout expires.
func captureRootDocID(ctx context.Context, c *cdpConn) {
	if c == nil {
		return
	}
	// Re-enable Network to ensure we receive events on this connection.
	sendCDP(c, "Network.enable", nil, 20)

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		default:
		}
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		data, err := readWSFrame(c.Conn)
		if err != nil {
			continue
		}
		var evt struct {
			Method string `json:"method"`
			Params struct {
				Request struct {
					URL      string `json:"url"`
					PostData string `json:"postData"`
				} `json:"request"`
			} `json:"params"`
		}
		if json.Unmarshal(data, &evt) != nil {
			continue
		}
		if evt.Method != "Network.requestWillBeSent" {
			continue
		}
		url := evt.Params.Request.URL
		if !strings.Contains(url, "api/efast/v1/dir/list") && !strings.Contains(url, "dir/list") {
			continue
		}
		if evt.Params.Request.PostData == "" {
			continue
		}
		var body struct {
			DocID string `json:"docid"`
		}
		if json.Unmarshal([]byte(evt.Params.Request.PostData), &body) == nil && body.DocID != "" {
			c.state.rootDocID = body.DocID
			fmt.Fprintf(os.Stderr, "[auth] captured root docid from dir/list postData\n")
			return
		}
	}
}

// --------- CDP ---------

type cdpTarget struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

func waitForCDP(port int, timeout time.Duration) (*cdpTarget, error) {
	dl := time.Now().Add(timeout)
	for time.Now().Before(dl) {
		r, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/json", port))
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		var t []cdpTarget
		json.NewDecoder(r.Body).Decode(&t)
		r.Body.Close()
		// Prefer a page target whose URL contains pan.ncepu.edu.cn.
		for _, x := range t {
			if x.WebSocketDebuggerURL != "" &&
				x.Type == "page" &&
				strings.Contains(x.URL, "pan.ncepu.edu.cn") {
				return &x, nil
			}
		}
		// Fallback: any page target.
		for _, x := range t {
			if x.WebSocketDebuggerURL != "" && x.Type == "page" {
				return &x, nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return nil, fmt.Errorf("cdp timeout")
}

func pollForCookies(ctx context.Context, wsURL string, iv time.Duration, state *cdpLoginState) (*cdpConn, []*http.Cookie, error) {
	conn, err := dialWS(wsURL)
	if err != nil {
		return nil, nil, err
	}
	c := &cdpConn{Conn: conn, state: state}
	sendCDP(c, "Network.enable", nil, 1)
	sendCDP(c, "Runtime.enable", nil, 3)

	tk := time.NewTicker(iv)
	defer tk.Stop()

	stableDetections := 0
	const requiredStablePolls = 3 // must stay on pan.ncepu.edu.cn for 3 consecutive polls

	for {
		select {
		case <-ctx.Done():
			return c, nil, ctx.Err()
		case <-tk.C:
			// Check current page URL
			pageURL := getCurrentURLCDP(c)

			cookies, err := getCookiesCDP(c)
			if err != nil {
				stableDetections = 0
				continue
			}

			if isLoginCompleteCDP(pageURL, cookies) {
				stableDetections++
				if stableDetections >= requiredStablePolls {
					// Re-extract fresh cookies after stabilization
					finalCookies, err := getCookiesCDP(c)
					if err != nil {
						return c, cookies, nil // fallback to last known cookies
					}
					if len(finalCookies) == 0 {
						return c, cookies, nil
					}
					return c, finalCookies, nil
				}
			} else {
				stableDetections = 0
			}
		}
	}
}

func isLoginCompleteCDP(pageURL string, cookies []*http.Cookie) bool {
	// Must be on pan.ncepu.edu.cn (NOT CAS login page)
	if pageURL == "" {
		return false
	}
	if !strings.Contains(pageURL, "pan.ncepu.edu.cn") {
		return false
	}
	if strings.Contains(pageURL, "ids.ncepu.edu.cn/authserver/login") {
		return false
	}
	// Must have cookies for ncepu.edu.cn
	return hasAnyShareCookies(cookies)
}

func getCurrentURLCDP(conn *cdpConn) string {
	params := map[string]interface{}{
		"expression":    "document.location.href",
		"returnByValue": true,
	}
	if err := sendCDP(conn, "Runtime.evaluate", params, 10); err != nil {
		return ""
	}
	raw, err := readCDPResponse(conn, 10)
	if err != nil {
		return ""
	}
	var resp struct {
		Result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &resp) != nil {
		return ""
	}
	return resp.Result.Result.Value
}

func getCookiesCDP(conn *cdpConn) ([]*http.Cookie, error) {
	drainWS(conn)
	type ck struct {
		Name, Value, Domain, Path string
		Expires                   float64
		Secure, HttpOnly          bool
	}
	if err := sendCDP(conn, "Network.getCookies", nil, 2); err != nil {
		return nil, err
	}
	raw, err := readCDPResponse(conn, 2)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Result struct {
			Cookies []ck `json:"cookies"`
		} `json:"result"`
	}
	json.Unmarshal(raw, &resp)
	out := make([]*http.Cookie, len(resp.Result.Cookies))
	for i, c := range resp.Result.Cookies {
		out[i] = &http.Cookie{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path, Secure: c.Secure, HttpOnly: c.HttpOnly}
		if c.Expires > 0 {
			out[i].Expires = time.Unix(int64(c.Expires), 0)
		}
	}
	return out, nil
}

func hasAnyShareCookies(cookies []*http.Cookie) bool {
	for _, c := range cookies {
		if !strings.HasSuffix(c.Domain, "ncepu.edu.cn") && c.Domain != "ncepu.edu.cn" {
			continue
		}
		// Key cookie names from AnyShare + CAS.
		switch c.Name {
		case "JSESSIONID", "SESSION", "connect.sid":
			return true
		}
		// Token value pattern.
		if strings.HasPrefix(c.Value, "ory_at_") {
			return true
		}
		// Substring match for AnyShare/CAS-related cookie names.
		lower := strings.ToLower(c.Name)
		if strings.Contains(lower, "session") || strings.Contains(lower, "token") ||
			strings.Contains(lower, "csrf") || strings.Contains(lower, "xsrf") {
			return true
		}
	}
	return false
}

func killEdgeOnPort(port int) {
	out, _ := exec.Command("netstat", "-ano").Output()
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, fmt.Sprintf(":%d", port)) && strings.Contains(line, "LISTENING") {
			f := strings.Fields(line)
			if len(f) >= 5 {
				exec.Command("taskkill", "/PID", f[len(f)-1], "/F").Run()
			}
		}
	}
}

// --------- WS ---------

// cdpConn wraps a WebSocket connection with the per-login state needed
// by captureNetworkEvent to record API tokens and root docids.
type cdpConn struct {
	net.Conn
	state *cdpLoginState
}

// webSocketMaskKey is a fixed mask key used for client-to-server frames.
// RFC 6455 5.3 requires that every frame sent from client to server be masked.
var webSocketMaskKey = [4]byte{0x12, 0x34, 0x56, 0x78}

func dialWS(wsURL string) (net.Conn, error) {
	host := fmt.Sprintf("127.0.0.1:%d", cdpPort)
	path := strings.TrimPrefix(wsURL, "ws://"+host)
	c, err := net.DialTimeout("tcp", host, 10*time.Second)
	if err != nil {
		return nil, err
	}
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, host, key)
	c.Write([]byte(req))
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil || resp.StatusCode != 101 {
		c.Close()
		return nil, fmt.Errorf("ws upgrade: %d", resp.StatusCode)
	}
	return c, nil
}

func sendCDP(conn *cdpConn, method string, params interface{}, id int) error {
	m := map[string]interface{}{"id": id, "method": method}
	if params != nil {
		m["params"] = params
	}
	data, _ := json.Marshal(m)
	return writeWSFrame(conn, data)
}

func readCDPResponse(conn *cdpConn, expectedID int) ([]byte, error) {
	dl := time.Now().Add(10 * time.Second)
	for time.Now().Before(dl) {
		data, err := readWSFrame(conn)
		if err != nil {
			return nil, err
		}
		// Capture API token from Network events (same as drainWS).
		captureNetworkEvent(data, conn.state)

		var check struct {
			ID int `json:"id"`
		}
		if json.Unmarshal(data, &check) == nil && check.ID == expectedID {
			return data, nil
		}
	}
	return nil, fmt.Errorf("cdp timeout id=%d", expectedID)
}

func captureNetworkEvent(data []byte, state *cdpLoginState) {
	var evt struct {
		Method string `json:"method"`
		Params struct {
			Request struct {
				URL      string            `json:"url"`
				Headers  map[string]string `json:"headers"`
				PostData string            `json:"postData"`
			} `json:"request"`
		} `json:"params"`
	}
	if json.Unmarshal(data, &evt) != nil || evt.Method != "Network.requestWillBeSent" {
		return
	}
	url := evt.Params.Request.URL
	if !strings.Contains(url, "api/efast") {
		return
	}
	// Capture root docid from dir/list call
	if strings.Contains(url, "dir/list") && state.rootDocID == "" && evt.Params.Request.PostData != "" {
		var body struct {
			DocID string `json:"docid"`
		}
		if json.Unmarshal([]byte(evt.Params.Request.PostData), &body) == nil && body.DocID != "" {
			state.rootDocID = body.DocID
		}
	}
	// Capture Bearer token
	if state.apiToken == "" {
		for k, v := range evt.Params.Request.Headers {
			if strings.ToLower(k) == "authorization" && strings.HasPrefix(v, "Bearer ") {
				state.apiToken = strings.TrimPrefix(v, "Bearer ")
				return
			}
		}
	}
}

func drainWS(conn *cdpConn) {
	conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	for {
		data, err := readWSFrame(conn)
		if err != nil {
			break
		}
		captureNetworkEvent(data, conn.state)
	}
	conn.SetReadDeadline(time.Time{})
}

func writeWSFrame(conn net.Conn, data []byte) error {
	n := len(data)
	// Header: 2 (base) + mask key (4) + optional extended length (2 or 8)
	hdrLen := 2 + 4 // base + mask key
	var extLen int
	switch {
	case n > 65535:
		extLen = 8
	case n > 125:
		extLen = 2
	}
	hdrLen += extLen

	frame := make([]byte, hdrLen+n)
	frame[0] = 0x81 // FIN | Text opcode

	// Payload length + MASK bit (0x80)
	if n <= 125 {
		frame[1] = byte(n) | 0x80
	} else if n <= 65535 {
		frame[1] = 126 | 0x80
		frame[2] = byte(n >> 8)
		frame[3] = byte(n)
	} else {
		frame[1] = 127 | 0x80
		frame[2] = byte(n >> 56)
		frame[3] = byte(n >> 48)
		frame[4] = byte(n >> 40)
		frame[5] = byte(n >> 32)
		frame[6] = byte(n >> 24)
		frame[7] = byte(n >> 16)
		frame[8] = byte(n >> 8)
		frame[9] = byte(n)
	}

	// Mask key at offset 2 (or 2+extLen depending on layout).
	// Extended length occupies bytes 2..(1+extLen), mask key follows.
	pos := 2 + extLen
	frame[pos+0] = webSocketMaskKey[0]
	frame[pos+1] = webSocketMaskKey[1]
	frame[pos+2] = webSocketMaskKey[2]
	frame[pos+3] = webSocketMaskKey[3]

	// Mask payload: each byte XOR with mask_key[i % 4]
	for i := 0; i < n; i++ {
		frame[hdrLen+i] = data[i] ^ webSocketMaskKey[i%4]
	}

	_, err := conn.Write(frame)
	return err
}

func readWSFrame(conn net.Conn) ([]byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(conn, h); err != nil {
		return nil, err
	}
	ln := int(h[1] & 0x7f)
	if ln == 126 {
		e := make([]byte, 2)
		io.ReadFull(conn, e)
		ln = int(e[0])<<8 | int(e[1])
	} else if ln == 127 {
		e := make([]byte, 8)
		io.ReadFull(conn, e)
		ln = int(e[4])<<24 | int(e[5])<<16 | int(e[6])<<8 | int(e[7])
	}
	data := make([]byte, ln)
	io.ReadFull(conn, data)
	return data, nil
}

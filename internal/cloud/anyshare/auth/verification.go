package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"time"

	securelog "ncepupan/hdd/internal/logging"
)

const (
	probeTimeout    = 15 * time.Second
	probeEndpoint   = "https://pan.ncepu.edu.cn/api/eacp/v1/user/get"
	loginPageMarker = "ids.ncepu.edu.cn/authserver/login"
	casMarker       = "ids.ncepu.edu.cn"
)

// UserInfo holds non-sensitive user identity information returned
// by the session probe.
type UserInfo struct {
	Account string `json:"account"`
	Name    string `json:"name"`
	UserID  string `json:"userid"`
	Type    string `json:"type"`
}

// ProbeSession verifies a WebView2-extracted session by calling
// POST /api/eacp/v1/user/get with the given cookies injected into
// a fresh cookie jar. It classifies the result into the appropriate
// sentinel error or returns user information on success.
//
// Security: Cookie values and response bodies are never logged.
// Only probe status, user ID presence, and HTTP status codes are logged.
func ProbeSession(ctx context.Context, cookies []StoredCookie) (*UserInfo, error) {
	return ProbeSessionWithToken(ctx, cookies, "")
}

// ProbeSessionWithToken is like ProbeSession but also sends an
// optional Bearer token extracted from the login session.
func ProbeSessionWithToken(ctx context.Context, cookies []StoredCookie, bearerToken string) (*UserInfo, error) {
	targetURL, _ := url.Parse("https://pan.ncepu.edu.cn/")

	jar, err := NewCookieJarWithCookies(targetURL, cookies)
	if err != nil {
		return nil, fmt.Errorf("auth probe: %w", err)
	}

	client := &http.Client{
		Jar:     jar,
		Timeout: probeTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return http.ErrUseLastResponse
			}
			loc := req.URL.String()
			if strings.Contains(loc, loginPageMarker) || strings.Contains(loc, casMarker+"/authserver") {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", probeEndpoint, bytes.NewReader([]byte{}))
	if err != nil {
		return nil, fmt.Errorf("auth probe: %w", err)
	}
	req.Header.Set("User-Agent", "HuadianDrive/1.0")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN")
	req.Header.Set("Content-Type", "application/json")
	if bearerToken != "" {
		req.Header.Set("Authorization", "Bearer "+bearerToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		securelog.LogSecurityEvent(ctx, securelog.SecurityEvent{Operation: "session_probe", Method: req.Method, Path: probeEndpoint, ErrorClass: "network"})
		return classifyNetworkError(err)
	}

	defer resp.Body.Close()

	// Classify HTTP-level responses.
	var statusInfo string
	switch resp.StatusCode {
	case 200:
		ui, err := parseUserInfo(resp.Body)
		if err != nil {
			return nil, err
		}
		if ui.Account == "" && ui.UserID == "" {
			securelog.LogSecurityEvent(ctx, securelog.SecurityEvent{Operation: "session_probe", Method: req.Method, Path: probeEndpoint, Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID"), ErrorClass: "invalid_session"})
			return nil, ErrInvalidSession
		}
		securelog.LogSecurityEvent(ctx, securelog.SecurityEvent{Operation: "session_probe", Method: req.Method, Path: probeEndpoint, Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID")})
		return ui, nil

	case 401, 403:
		securelog.LogSecurityEvent(ctx, securelog.SecurityEvent{Operation: "session_probe", Method: req.Method, Path: probeEndpoint, Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID"), ErrorClass: "unauthorized"})
		return nil, ErrSessionExpired

	case 302, 301, 303, 307, 308:
		loc := resp.Header.Get("Location")
		if strings.Contains(loc, loginPageMarker) || strings.Contains(loc, "login") {
			securelog.LogSecurityEvent(ctx, securelog.SecurityEvent{Operation: "session_probe", Method: req.Method, Path: probeEndpoint, Status: resp.StatusCode, RequestID: resp.Header.Get("X-Request-ID"), ErrorClass: "interactive_login_required"})
			return nil, ErrInteractiveLoginRequired
		}
		statusInfo = fmt.Sprintf("%d (redirect)", resp.StatusCode)

	default:
		statusInfo = fmt.Sprintf("%d", resp.StatusCode)
	}

	return nil, fmt.Errorf("auth probe: unexpected status %s", statusInfo)
}

func parseUserInfo(r io.Reader) (*UserInfo, error) {
	var ui UserInfo
	body, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil {
		return nil, fmt.Errorf("auth probe read: %w", err)
	}
	if err := json.Unmarshal(body, &ui); err != nil {
		return nil, fmt.Errorf("auth probe parse: %w", err)
	}
	return &ui, nil
}

func classifyNetworkError(err error) (*UserInfo, error) {
	if err == nil {
		return nil, nil
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return nil, fmt.Errorf("%w: timeout", ErrNetworkUnavailable)
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return nil, fmt.Errorf("%w: dns lookup failed", ErrNetworkUnavailable)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("%w: deadline exceeded", ErrNetworkUnavailable)
	}
	return nil, fmt.Errorf("%w: %s", ErrNetworkUnavailable, redactError(err))
}

// redactError strips potentially sensitive information from error messages.
func redactError(err error) string {
	msg := err.Error()
	// Strip URL-encoded query parameters that might contain tokens.
	if idx := strings.Index(msg, "?"); idx >= 0 {
		msg = msg[:idx]
	}
	// Strip Authorization headers that might appear in errors.
	if idx := strings.Index(strings.ToLower(msg), "authorization:"); idx >= 0 {
		end := strings.Index(msg[idx:], "\n")
		if end < 0 {
			end = len(msg) - idx
		}
		msg = msg[:idx] + "Authorization:***" + msg[idx+end:]
	}
	return msg
}

// ProbeSessionWithJar is like ProbeSession but injects cookies into an
// existing cookie jar instead of creating a new one.
func ProbeSessionWithJar(ctx context.Context, jar *cookiejar.Jar, cookies []StoredCookie) (*UserInfo, error) {
	targetURL, _ := url.Parse("https://pan.ncepu.edu.cn/")
	InjectCookiesIntoJar(jar, targetURL, cookies)
	return ProbeSession(ctx, nil)
}

// DiagnoseHeaders compares request headers sent to the user/get and
// dir/list endpoints. Only header names and presence are printed;
// header values are never exposed. This helps diagnose 200-vs-403
// discrepancies caused by missing or misconfigured request headers.
func DiagnoseHeaders() {
	var accessToken string

	// Try loading session from the default credential store.
	store, err := NewFileCredentialStore("")
	if err == nil {
		mgr := NewSessionManager(store)
		sess, loadErr := mgr.LoadSession()
		if loadErr == nil && sess != nil {
			accessToken = sess.AccessToken
		}
	}
	_ = accessToken

	// Probe user/get.
	userHeaders := map[string]bool{}
	{
		req, _ := http.NewRequest("POST", "https://pan.ncepu.edu.cn/api/eacp/v1/user/get", nil)
		req.Header.Set("Content-Type", "application/json")
		if accessToken != "" {
			req.Header.Set("Authorization", "Bearer "+accessToken)
		}
		setWafHeaders(req)
		// Dump request headers (names only, no values).
		for k := range req.Header {
			userHeaders[strings.ToLower(k)] = true
		}
	}

	// Probe dir/list.
	dirHeaders := map[string]bool{}
	{
		body := strings.NewReader(`{"docid":"","sort":"asc","by":"name"}`)
		req, _ := http.NewRequest("POST", "https://pan.ncepu.edu.cn/api/efast/v1/dir/list", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
		req.Header.Set("X-Language", "zh-CN")
		req.Header.Set("Referer", "https://pan.ncepu.edu.cn/anyshare/")
		req.Header.Set("Origin", "https://pan.ncepu.edu.cn")
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		req.Header.Set("Sec-Fetch-Mode", "cors")
		req.Header.Set("Sec-Fetch-Dest", "empty")
		if accessToken != "" {
			req.Header.Set("Authorization", "Bearer "+accessToken)
		}
		setWafHeaders(req)
		for k := range req.Header {
			dirHeaders[strings.ToLower(k)] = true
		}
	}

	fmt.Fprintf(os.Stderr, "[diagnose] user/get headers (%d):\n", len(userHeaders))
	for k := range userHeaders {
		fmt.Fprintf(os.Stderr, "  %s\n", k)
	}
	fmt.Fprintf(os.Stderr, "[diagnose] dir/list headers (%d):\n", len(dirHeaders))
	for k := range dirHeaders {
		_, inUser := userHeaders[k]
		marker := " "
		if !inUser {
			marker = "+"
		}
		fmt.Fprintf(os.Stderr, " %s %s\n", marker, k)
	}
	// Show headers present in user/get but NOT dir/list.
	fmt.Fprintf(os.Stderr, "[diagnose] only in user/get:\n")
	for k := range userHeaders {
		if !dirHeaders[k] {
			fmt.Fprintf(os.Stderr, "  - %s\n", k)
		}
	}
	// Show headers present in dir/list but NOT user/get.
	fmt.Fprintf(os.Stderr, "[diagnose] only in dir/list:\n")
	for k := range dirHeaders {
		if !userHeaders[k] {
			fmt.Fprintf(os.Stderr, "  + %s\n", k)
		}
	}
}

// setWafHeaders adds the browser-mimic headers required by the CDN/WAF.
func setWafHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36 Edg/149.0.0.0")
	req.Header.Set("sec-ch-ua", `"Microsoft Edge";v="149", "Chromium";v="149", "Not)A;Brand";v="24"`)
	req.Header.Set("sec-ch-ua-mobile", "?0")
	req.Header.Set("sec-ch-ua-platform", `"Windows"`)
	req.Header.Set("Date", time.Now().UTC().Format(time.RFC1123))
}

func mustParseURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

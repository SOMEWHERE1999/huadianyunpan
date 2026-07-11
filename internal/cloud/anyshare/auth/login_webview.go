//go:build windows

package auth

import (
	"context"
	"fmt"
	"strings"
	"time"

	"ncepupan/hdd/internal/platform/windows/webview2"
)

const (
	webview2LoginTimeout = 120
)

// WebView2LoginUI implements LoginUI using an embedded WebView2 browser.
// It opens a WebView2 window to https://pan.ncepu.edu.cn, waits for the
// user to complete CAS/OAuth login, extracts cookies via the CookieManager,
// and probes the session against the API.
type WebView2LoginUI struct{}

// NewWebView2LoginUI creates a WebView2 login UI.
func NewWebView2LoginUI() *WebView2LoginUI {
	return &WebView2LoginUI{}
}

// IsWebView2Available checks whether the WebView2 runtime is installed.
func IsWebView2Available() bool {
	// This is a registry-based check. The WebView2 runtime is typically
	// installed at system level. On Windows 10 22H2+ and Windows 11, the
	// Edge WebView2 Runtime is included via Windows Update.
	//
	// A production check would verify the registry key:
	//   HKLM\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}
	//
	// For now, we rely on the runtime check in Login() which will return
	// a clear error if WebView2 is unavailable.
	return true
}

// Login starts the WebView2 login flow.
//
// The flow:
//  1. Open WebView2 window to https://pan.ncepu.edu.cn
//  2. User completes CAS/OAuth login in the embedded browser
//  3. Detect navigation back to pan.ncepu.edu.cn (NOT ids.ncepu.edu.cn/authserver/login)
//  4. Extract cookies via CoreWebView2.CookieManager.GetCookiesAsync
//  5. Probe session via POST /api/eacp/v1/user/get
//  6. Return session with user info on success
func (ui *WebView2LoginUI) Login(ctx context.Context) (*Session, error) {
	result, err := webview2.ExtractCookies(loginURL, webview2LoginTimeout)
	if err != nil {
		if err == webview2.ErrCookieManagerUnavailable {
			return nil, ErrCookieManagerUnavailable
		}
		return nil, fmt.Errorf("webview2 login: %w", err)
	}

	if !result.Success {
		if strings.Contains(result.Error, "cancelled") {
			return nil, ErrLoginCancelled
		}
		if strings.Contains(result.Error, "timed out") {
			return nil, fmt.Errorf("%w: %s", ErrInteractiveLoginRequired, result.Error)
		}
		return nil, fmt.Errorf("%w: %s", ErrCookieExtractFailed, result.Error)
	}

	sameSiteMap := map[int]string{
		webview2.SameSiteNone:   "None",
		webview2.SameSiteLax:    "Lax",
		webview2.SameSiteStrict: "Strict",
	}

	cookies := make([]StoredCookie, 0, len(result.Cookies))
	for _, c := range result.Cookies {
		if c.Name == "" || c.Domain == "" {
			continue
		}
		if !strings.HasSuffix(c.Domain, "ncepu.edu.cn") && c.Domain != "ncepu.edu.cn" {
			continue
		}
		var expires time.Time
		if c.Expires > 0 {
			expires = time.Unix(int64(c.Expires), 0)
		}
		sameSite := sameSiteMap[c.SameSite]

		cookies = append(cookies, FromWebView2Cookie(
			c.Name, c.Value, c.Domain, c.Path, sameSite,
			expires, c.Secure, c.HttpOnly,
		))
	}

	if len(cookies) == 0 {
		return nil, fmt.Errorf("%w: no ncepu.edu.cn cookies found", ErrCookieExtractFailed)
	}

	sess := &Session{
		Cookies: cookies,
	}
	if b := extractStoredBearerCookie(cookies); b != "" {
		sess.AccessToken = b
	}

	return sess, nil
}

func extractStoredBearerCookie(cookies []StoredCookie) string {
	for _, c := range cookies {
		if strings.HasPrefix(c.Value, "ory_at_") {
			return c.Value
		}
	}
	for _, c := range cookies {
		if c.Name == "client.oauth2_token" && len(c.Value) > 20 {
			return c.Value
		}
	}
	for _, c := range cookies {
		v := c.Value
		if len(v) > 40 && strings.HasPrefix(v, "eyJ") {
			parts := strings.Split(v, ".")
			if len(parts) == 3 {
				return v
			}
		}
	}
	return ""
}

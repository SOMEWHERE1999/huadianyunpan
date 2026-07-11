//go:build !webview2

package webview2

import "fmt"

// ErrCookieManagerUnavailable is returned when WebView2 support is not
// compiled in. Build with -tags webview2 to enable embedded WebView2 login.
var ErrCookieManagerUnavailable = fmt.Errorf("webview2: CookieManager not available; rebuild with -tags webview2")

// CookieResult holds the results of a WebView2 cookie extraction.
type CookieResult struct {
	Success bool        `json:"success"`
	Cookies []RawCookie `json:"cookies"`
	Error   string      `json:"error"`
}

// RawCookie is a cookie as returned by the WebView2 CookieManager.
type RawCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	Secure   bool    `json:"secure"`
	HttpOnly bool    `json:"http_only"`
	SameSite int     `json:"same_site"`
}

// SameSite values returned by WebView2 CookieManager.
const (
	SameSiteNone   = 0
	SameSiteLax    = 1
	SameSiteStrict = 2
)

// ExtractCookies returns ErrCookieManagerUnavailable when WebView2 is
// not compiled in.
func ExtractCookies(loginURL string, timeoutSeconds int) (*CookieResult, error) {
	return nil, ErrCookieManagerUnavailable
}

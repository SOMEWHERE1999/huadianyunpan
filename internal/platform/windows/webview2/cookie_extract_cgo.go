//go:build windows && webview2

package webview2

import (
	"ncepupan/hdd/internal/platform/windows/webview2/native"
)

// CookieResult holds the results of a WebView2 cookie extraction.
type CookieResult = native.CookieResult

// RawCookie is a cookie as returned by the WebView2 CookieManager.
type RawCookie = native.RawCookie

// SameSite values returned by WebView2 CookieManager.
const (
	SameSiteNone   = native.SameSiteNone
	SameSiteLax    = native.SameSiteLax
	SameSiteStrict = native.SameSiteStrict
)

// ExtractCookies delegates to the native CGo WebView2 implementation.
func ExtractCookies(loginURL string, timeoutSeconds int) (*CookieResult, error) {
	return native.ExtractCookies(loginURL, timeoutSeconds)
}

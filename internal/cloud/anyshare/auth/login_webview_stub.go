//go:build !windows

package auth

import (
	"context"
)

// WebView2LoginUI is not available on non-Windows platforms.
type WebView2LoginUI struct{}

// NewWebView2LoginUI returns a stub that always returns an error.
func NewWebView2LoginUI() *WebView2LoginUI {
	return &WebView2LoginUI{}
}

// IsWebView2Available returns false on non-Windows.
func IsWebView2Available() bool {
	return false
}

// Login returns ErrWebViewUnavailable on non-Windows.
func (ui *WebView2LoginUI) Login(_ context.Context) (*Session, error) {
	return nil, ErrWebViewUnavailable
}

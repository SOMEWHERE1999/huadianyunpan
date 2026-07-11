package auth

// Sentinel errors for authentication flows.
import "errors"

var (
	ErrInteractiveLoginRequired = errors.New("auth: interactive login required; run hddctl login")
	ErrSessionExpired           = errors.New("auth: session expired; run hddctl login to re-authenticate")
	ErrInvalidSession           = errors.New("auth: invalid session; no user identity returned")
	ErrUnauthorized             = errors.New("auth: unauthorized")
	ErrNotAuthenticated         = errors.New("auth: not authenticated")
	ErrCookieExtractFailed      = errors.New("auth: failed to extract cookies from browser")
	ErrWebViewUnavailable       = errors.New("auth: WebView2 runtime not available; use hddctl login --console")
	ErrCookieManagerUnavailable = errors.New("auth: WebView2 CookieManager not available; fallback to CDP login")
	ErrNetworkUnavailable       = errors.New("auth: network unavailable; verify connectivity to pan.ncepu.edu.cn")
	ErrLoginCancelled           = errors.New("auth: login cancelled by user")
)

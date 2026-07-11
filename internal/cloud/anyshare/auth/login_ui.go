package auth

import (
	"context"
	"net/http"
)

// LoginUI abstracts the interactive login user interface.
// Implementations include ConsoleLoginUI (manual token paste),
// WebView2LoginUI (embedded browser), and future CDP-based login.
type LoginUI interface {
	// Login starts an interactive login flow and returns cookies
	// and/or an access token when the user has authenticated.
	Login(ctx context.Context) (*Session, error)
}

// LoginSuccessDetector checks whether a given response or state
// indicates that the user has successfully logged in.
type LoginSuccessDetector interface {
	// IsLoginSuccess returns true if the current URL and cookies
	// indicate a successful login.
	// url is the current page URL.
	// cookies are the cookies for the target domain.
	IsLoginSuccess(url string, cookies []*http.Cookie) bool
}

// defaultDetector implements the login success detection rules.
type defaultDetector struct{}

func (d *defaultDetector) IsLoginSuccess(url string, cookies []*http.Cookie) bool {
	// Rule 1: URL contains pan.ncepu.edu.cn/anyshare
	if contains(url, "pan.ncepu.edu.cn/anyshare") {
		return true
	}

	// Rule 2: Domain is pan.ncepu.edu.cn AND session cookie present
	if contains(url, "pan.ncepu.edu.cn") {
		for _, c := range cookies {
			if c.Name == "SESSION" || contains(c.Name, "JSESSIONID") ||
				contains(c.Name, "session") || contains(c.Name, "token") {
				return true
			}
		}
	}

	// Rule 3: Not on CAS login page AND domain is pan.ncepu.edu.cn
	if contains(url, "pan.ncepu.edu.cn") && !contains(url, "ids.ncepu.edu.cn/authserver/login") {
		// Only if we've actually navigated away from CAS
		for _, c := range cookies {
			if c.Domain == "pan.ncepu.edu.cn" || contains(c.Domain, "ncepu.edu.cn") {
				if contains(c.Name, "SESSION") || contains(c.Name, "token") ||
					contains(c.Name, "ory_at") {
					return true
				}
			}
		}
	}

	return false
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

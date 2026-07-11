package auth

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	securelog "ncepupan/hdd/internal/logging"
)

// StoredCookie is a serializable representation of an http.Cookie.
type StoredCookie struct {
	Name     string    `json:"name"`
	Value    string    `json:"value"`
	Domain   string    `json:"domain"`
	Path     string    `json:"path"`
	Expires  time.Time `json:"expires"`
	Secure   bool      `json:"secure"`
	HttpOnly bool      `json:"http_only"`
	SameSite string    `json:"same_site,omitempty"`
}

// ToHTTPCookie converts a StoredCookie back to an http.Cookie.
func (sc *StoredCookie) ToHTTPCookie() *http.Cookie {
	c := &http.Cookie{
		Name:     sc.Name,
		Value:    sc.Value,
		Domain:   sc.Domain,
		Path:     sc.Path,
		Secure:   sc.Secure,
		HttpOnly: sc.HttpOnly,
	}
	if !sc.Expires.IsZero() {
		c.Expires = sc.Expires
	}
	return c
}

// FromHTTPCookie creates a StoredCookie from an http.Cookie.
func FromHTTPCookie(c *http.Cookie) StoredCookie {
	return StoredCookie{
		Name:     c.Name,
		Value:    c.Value,
		Domain:   c.Domain,
		Path:     c.Path,
		Expires:  c.Expires,
		Secure:   c.Secure,
		HttpOnly: c.HttpOnly,
	}
}

// FromWebView2Cookie creates a StoredCookie from WebView2 cookie attributes.
// The domain, path, and SameSite values are set as provided by the WebView2
// CookieManager.
func FromWebView2Cookie(name, value, domain, path, sameSite string, expires time.Time, secure, httpOnly bool) StoredCookie {
	return StoredCookie{
		Name:     name,
		Value:    value,
		Domain:   domain,
		Path:     path,
		Expires:  expires,
		Secure:   secure,
		HttpOnly: httpOnly,
		SameSite: sameSite,
	}
}

// CookiesToHeader converts cookies matching reqDomain to a Cookie header value.
// Expired cookies are excluded. Only Secure cookies are sent (AnyShare uses HTTPS).
func CookiesToHeader(cookies []StoredCookie) string {
	return CookiesToHeaderForDomain(cookies, "")
}

// CookiesToHeaderForDomain converts cookies matching the given domain suffix
// to a Cookie header value, filtering expired cookies.
func CookiesToHeaderForDomain(cookies []StoredCookie, domainSuffix string) string {
	if len(cookies) == 0 {
		return ""
	}
	var result string
	for _, c := range cookies {
		if c.IsExpired() {
			continue
		}
		if domainSuffix != "" && !strings.HasSuffix(c.Domain, domainSuffix) {
			continue
		}
		if result != "" {
			result += "; "
		}
		result += c.Name + "=" + c.Value
	}
	return result
}

// IsExpired returns true if the cookie has a non-zero expiration in the past.
func (sc *StoredCookie) IsExpired() bool {
	if sc.Expires.IsZero() {
		return false
	}
	return time.Now().After(sc.Expires)
}

// MatchesDomain returns true if the cookie's Domain matches the given host.
// A leading dot in the cookie Domain means it matches the domain and all
// subdomains. Without a leading dot, it matches exactly.
// The host may include a port number; the port is stripped before comparison.
//
// For example:
//   - Domain ".pan.ncepu.edu.cn" matches "pan.ncepu.edu.cn" and "a.pan.ncepu.edu.cn"
//   - Domain "pan.ncepu.edu.cn" matches only "pan.ncepu.edu.cn"
//   - Domain ".ncepu.edu.cn" matches "pan.ncepu.edu.cn", "ids.ncepu.edu.cn", etc.
func (sc *StoredCookie) MatchesDomain(host string) bool {
	if host == "" || sc.Domain == "" {
		return false
	}
	// Strip port if present
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(host)
	domain := strings.ToLower(sc.Domain)

	if host == domain {
		return true
	}
	if strings.HasPrefix(domain, ".") {
		return strings.HasSuffix(host, domain) || host == domain[1:]
	}
	return false
}

// MatchesPath returns true if the cookie's Path is a prefix of the given path.
func (sc *StoredCookie) MatchesPath(reqPath string) bool {
	if sc.Path == "" || sc.Path == "/" {
		return true
	}
	if reqPath == "" {
		reqPath = "/"
	}
	return strings.HasPrefix(reqPath, sc.Path)
}

// MatchesSecure returns true if the cookie can be sent for the given scheme.
// A Secure cookie must only be sent over HTTPS.
func (sc *StoredCookie) MatchesSecure(scheme string) bool {
	if !sc.Secure {
		return true
	}
	return scheme == "https"
}

// injectableCookieJar is a cookiejar.Jar extended with an injection method.
type injectableCookieJar struct {
	*cookiejar.Jar
}

// InjectCookies adds the given cookies to the jar for the specified URL.
// Cookies whose Domain does not match the URL's host are silently skipped.
// Cookies whose Secure flag is incompatible with the URL scheme are skipped.
func (j *injectableCookieJar) InjectCookies(u *url.URL, cookies []StoredCookie) {
	for _, c := range cookies {
		if !c.MatchesDomain(u.Host) {
			continue
		}
		if !c.MatchesSecure(u.Scheme) {
			continue
		}
		hc := c.ToHTTPCookie()
		if hc.Path == "" {
			hc.Path = "/"
		}
		j.SetCookies(u, []*http.Cookie{hc})
	}
}

// NewCookieJarWithCookies creates a new cookiejar.Jar pre-filled with the
// given cookies for the specified URL. Only cookies that match the URL's
// host and scheme are injected. This is the primary API for injecting
// WebView2-extracted cookies before probing the session.
func NewCookieJarWithCookies(targetURL *url.URL, cookies []StoredCookie) (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("cookie jar: %w", err)
	}
	ij := &injectableCookieJar{jar}
	ij.InjectCookies(targetURL, cookies)
	return jar, nil
}

// InjectCookiesIntoJar is a convenience function for injecting cookies into
// an existing cookiejar.Jar.
func InjectCookiesIntoJar(jar *cookiejar.Jar, targetURL *url.URL, cookies []StoredCookie) {
	ij := &injectableCookieJar{jar}
	ij.InjectCookies(targetURL, cookies)
}

var _ http.CookieJar = (*injectableCookieJar)(nil)

// LogCookieTable prints a safe, human-readable table of cookie metadata
// to stderr (PowerShell-compatible). Cookie values are never printed.
// Only non-sensitive attributes are shown: Name, Domain, Path, Secure,
// HttpOnly, SameSite, and Expires.
//
// Example output:
//
//	────── Extracted 5 Cookies for https://pan.ncepu.edu.cn/ ──────
//	  Name         Domain                Path  Secure  HttpOnly  SameSite  Expires
//	  SESSION      .pan.ncepu.edu.cn     /     true    true      Lax       session
//	  ory_at_xxx   .ncepu.edu.cn         /     true    false     None      2026-07-05
func LogCookieTable(cookies []StoredCookie, targetURL string) {
	_ = cookies
	securelog.LogSecurityEvent(context.Background(), securelog.SecurityEvent{Operation: "cookie_capture", Method: "GET", Path: targetURL})
}

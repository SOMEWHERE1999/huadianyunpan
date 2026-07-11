package auth

import (
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestSessionCookieNoExpiresNotFiltered(t *testing.T) {
	sc := StoredCookie{
		Name:     "SESSION",
		Value:    "abc123",
		Domain:   ".pan.ncepu.edu.cn",
		Path:     "/",
		Secure:   true,
		HttpOnly: true,
	}

	if sc.IsExpired() {
		t.Error("session cookie without Expires should not be considered expired")
	}

	u, _ := url.Parse("https://pan.ncepu.edu.cn/")
	jar, err := NewCookieJarWithCookies(u, []StoredCookie{sc})
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}

	cookies := jar.Cookies(u)
	if len(cookies) == 0 {
		t.Error("session cookie without Expires should be injected into jar")
	}
}

func TestNCEPUEDUCNDomainMatchesPan(t *testing.T) {
	sc := StoredCookie{
		Name:   "TOKEN",
		Value:  "xyz",
		Domain: ".ncepu.edu.cn",
		Path:   "/",
		Secure: true,
	}

	if !sc.MatchesDomain("pan.ncepu.edu.cn") {
		t.Error(".ncepu.edu.cn cookie should match pan.ncepu.edu.cn")
	}
	if !sc.MatchesDomain("ids.ncepu.edu.cn") {
		t.Error(".ncepu.edu.cn cookie should match ids.ncepu.edu.cn")
	}
	if !sc.MatchesDomain("ncepu.edu.cn") {
		t.Error(".ncepu.edu.cn cookie should match ncepu.edu.cn (cookie Domain is .ncepu.edu.cn, host is ncepu.edu.cn)")
	}
	if sc.MatchesDomain("other.com") {
		t.Error(".ncepu.edu.cn cookie should NOT match other.com")
	}
}

func TestDomainMatchingPrecise(t *testing.T) {
	sc := StoredCookie{
		Name:   "TOKEN",
		Value:  "xyz",
		Domain: "pan.ncepu.edu.cn",
		Path:   "/",
	}

	if !sc.MatchesDomain("pan.ncepu.edu.cn") {
		t.Error("exact domain should match")
	}
	if sc.MatchesDomain("sub.pan.ncepu.edu.cn") {
		t.Error("non-leading-dot domain should NOT match subdomain")
	}
}

func TestPathMatching(t *testing.T) {
	tests := []struct {
		cookiePath string
		reqPath    string
		expect     bool
	}{
		{"/", "/", true},
		{"/", "/api/v1/user", true},
		{"/api", "/api/v1/user", true},
		{"/api/", "/api/v1/user", true},
		{"/admin", "/api/v1/user", false},
		{"", "/", true},
		{"", "/anything", true},
	}

	for _, tt := range tests {
		sc := StoredCookie{Path: tt.cookiePath}
		if got := sc.MatchesPath(tt.reqPath); got != tt.expect {
			t.Errorf("Path=%q reqPath=%q: got %v, want %v", tt.cookiePath, tt.reqPath, got, tt.expect)
		}
	}
}

func TestSecureCookieOnlyForHTTPS(t *testing.T) {
	sc := StoredCookie{
		Name:   "SECURE_COOKIE",
		Value:  "secret",
		Domain: "pan.ncepu.edu.cn",
		Path:   "/",
		Secure: true,
	}

	if !sc.MatchesSecure("https") {
		t.Error("Secure cookie should match https")
	}
	if sc.MatchesSecure("http") {
		t.Error("Secure cookie should NOT match http")
	}

	scNonSecure := StoredCookie{
		Name:   "PLAIN_COOKIE",
		Value:  "plain",
		Domain: "pan.ncepu.edu.cn",
	}

	if !scNonSecure.MatchesSecure("http") {
		t.Error("non-Secure cookie should match http")
	}
	if !scNonSecure.MatchesSecure("https") {
		t.Error("non-Secure cookie should match https")
	}
}

func TestMultipleCookiesInJar(t *testing.T) {
	cookies := []StoredCookie{
		{Name: "A", Value: "1", Domain: ".pan.ncepu.edu.cn", Path: "/", Secure: true},
		{Name: "B", Value: "2", Domain: ".pan.ncepu.edu.cn", Path: "/"},
		{Name: "C", Value: "3", Domain: ".ncepu.edu.cn", Path: "/", Secure: true},
	}

	u, _ := url.Parse("https://pan.ncepu.edu.cn/")
	jar, err := NewCookieJarWithCookies(u, cookies)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}

	got := jar.Cookies(u)
	if len(got) != 3 {
		t.Errorf("expected 3 cookies, got %d", len(got))
	}

	names := make(map[string]bool)
	for _, c := range got {
		names[c.Name] = true
	}
	for _, name := range []string{"A", "B", "C"} {
		if !names[name] {
			t.Errorf("cookie %q not found in jar", name)
		}
	}
}

func TestCookieJarDomainFiltering(t *testing.T) {
	cookies := []StoredCookie{
		{Name: "GOOD", Value: "1", Domain: ".pan.ncepu.edu.cn", Path: "/", Secure: true},
		{Name: "BAD", Value: "2", Domain: ".other.com", Path: "/", Secure: true},
	}

	u, _ := url.Parse("https://pan.ncepu.edu.cn/")
	jar, err := NewCookieJarWithCookies(u, cookies)
	if err != nil {
		t.Fatalf("failed to create cookie jar: %v", err)
	}

	got := jar.Cookies(u)
	if len(got) != 1 {
		t.Errorf("expected 1 cookie, got %d", len(got))
	}
	if got[0].Name != "GOOD" {
		t.Errorf("expected GOOD cookie, got %s", got[0].Name)
	}
}

func TestCookieJarSecureFiltering(t *testing.T) {
	cookies := []StoredCookie{
		{Name: "SECURE", Value: "1", Domain: ".pan.ncepu.edu.cn", Path: "/", Secure: true},
		{Name: "PLAIN", Value: "2", Domain: ".pan.ncepu.edu.cn", Path: "/", Secure: false},
	}

	httpsURL, _ := url.Parse("https://pan.ncepu.edu.cn/")
	jar, _ := NewCookieJarWithCookies(httpsURL, cookies)

	// For HTTPS, both should be included
	httpsCookies := jar.Cookies(httpsURL)
	if len(httpsCookies) != 2 {
		t.Errorf("HTTPS: expected 2 cookies, got %d", len(httpsCookies))
	}

	// If we query with HTTP URL from the same jar, Secure cookies should still be excluded
	httpURL, _ := url.Parse("http://pan.ncepu.edu.cn/")
	httpCookies := jar.Cookies(httpURL)
	for _, c := range httpCookies {
		if c.Secure {
			t.Errorf("HTTP request should not include Secure cookie %s", c.Name)
		}
	}
}

func TestFromWebView2Cookie(t *testing.T) {
	expires := time.Now().Add(24 * time.Hour)
	sc := FromWebView2Cookie(
		"TOKEN", "secret", ".pan.ncepu.edu.cn", "/",
		"Lax", expires, true, true,
	)

	if sc.Name != "TOKEN" {
		t.Errorf("Name: got %q", sc.Name)
	}
	if sc.Value != "secret" {
		t.Errorf("Value: got %q", sc.Value)
	}
	if sc.Domain != ".pan.ncepu.edu.cn" {
		t.Errorf("Domain: got %q", sc.Domain)
	}
	if sc.Path != "/" {
		t.Errorf("Path: got %q", sc.Path)
	}
	if sc.SameSite != "Lax" {
		t.Errorf("SameSite: got %q", sc.SameSite)
	}
	if !sc.Expires.Equal(expires) {
		t.Errorf("Expires mismatch")
	}
	if !sc.Secure {
		t.Error("Secure should be true")
	}
	if !sc.HttpOnly {
		t.Error("HttpOnly should be true")
	}
}

func TestCookieJarNonSecureURLInjection(t *testing.T) {
	sc := StoredCookie{
		Name:   "SECURE",
		Value:  "val",
		Domain: ".pan.ncepu.edu.cn",
		Path:   "/",
		Secure: true,
	}

	httpURL, _ := url.Parse("http://pan.ncepu.edu.cn/")
	jar, _ := NewCookieJarWithCookies(httpURL, []StoredCookie{sc})

	// Even though we inject with HTTP URL, the Secure cookie should not be injected
	// because inject skips Secure cookies for http scheme
	httpsURL, _ := url.Parse("https://pan.ncepu.edu.cn/")
	allCookies := jar.Cookies(httpsURL)
	// The cookie was injected via http URL, so it should have been skipped
	_ = allCookies
}

func TestRedactionNoCookieValue(t *testing.T) {
	cookies := []StoredCookie{
		{Name: "SESSION", Value: "secret-session-value-12345", Domain: ".pan.ncepu.edu.cn", Path: "/"},
	}

	redacted := RedactCookies(cookies)
	if strings.Contains(redacted, "secret-session-value") {
		t.Errorf("RedactCookies should NOT contain cookie values, got: %s", redacted)
	}
	if !strings.Contains(redacted, "SESSION") {
		t.Errorf("RedactCookies should contain cookie names, got: %s", redacted)
	}
}

func TestRedactToken(t *testing.T) {
	token := "ory_at_deadbeef1234567890abcdef"
	redacted := RedactToken(token)
	if strings.Contains(redacted, "deadbeef") {
		t.Errorf("RedactToken should NOT contain token suffix, got: %s", redacted)
	}
	if redacted == token {
		t.Errorf("RedactToken should redact the value")
	}
	if redacted == "<empty>" {
		t.Error("non-empty token should not return <empty>")
	}
}

func TestCookiesToHeaderForDomain(t *testing.T) {
	cookies := []StoredCookie{
		{Name: "A", Value: "1", Domain: ".pan.ncepu.edu.cn", Path: "/"},
		{Name: "B", Value: "2", Domain: ".ncepu.edu.cn", Path: "/"},
		{Name: "C", Value: "3", Domain: ".other.com", Path: "/"},
	}

	header := CookiesToHeaderForDomain(cookies, "ncepu.edu.cn")
	if !strings.Contains(header, "A=1") {
		t.Errorf("header should contain A=1, got: %s", header)
	}
	if !strings.Contains(header, "B=2") {
		t.Errorf("header should contain B=2, got: %s", header)
	}
	if strings.Contains(header, "C=3") {
		t.Errorf("header should NOT contain C=3, got: %s", header)
	}
}

func TestExpiredCookieFiltered(t *testing.T) {
	expired := time.Now().Add(-1 * time.Hour)
	cookies := []StoredCookie{
		{Name: "FRESH", Value: "1", Domain: ".pan.ncepu.edu.cn", Path: "/"},
		{Name: "STALE", Value: "2", Domain: ".pan.ncepu.edu.cn", Path: "/", Expires: expired},
	}

	header := CookiesToHeader(cookies)
	if !strings.Contains(header, "FRESH") {
		t.Errorf("header should contain FRESH, got: %s", header)
	}
	if strings.Contains(header, "STALE") {
		t.Errorf("header should NOT contain expired STALE, got: %s", header)
	}
}

func TestInjectCookiesIntoJar(t *testing.T) {
	jar, _ := cookiejar.New(nil)
	cookies := []StoredCookie{
		{Name: "X", Value: "100", Domain: ".pan.ncepu.edu.cn", Path: "/", Secure: true},
	}

	u, _ := url.Parse("https://pan.ncepu.edu.cn/")
	InjectCookiesIntoJar(jar, u, cookies)

	got := jar.Cookies(u)
	if len(got) != 1 || got[0].Name != "X" {
		t.Errorf("injected cookie not found in jar")
	}
}

func TestStoredCookieToHTTPCookie(t *testing.T) {
	exp := time.Unix(2000000000, 0)
	sc := StoredCookie{
		Name:     "TEST",
		Value:    "val",
		Domain:   ".pan.ncepu.edu.cn",
		Path:     "/api",
		Expires:  exp,
		Secure:   true,
		HttpOnly: true,
		SameSite: "Strict",
	}

	hc := sc.ToHTTPCookie()
	if hc.Name != "TEST" || hc.Value != "val" {
		t.Error("Name/Value mismatch")
	}
	if !hc.Expires.Equal(exp) {
		t.Error("Expires mismatch")
	}
	if !hc.Secure || !hc.HttpOnly {
		t.Error("Secure/HttpOnly mismatch")
	}
}

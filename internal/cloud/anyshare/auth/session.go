package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	checkTimeout = 10 * time.Second
	baseURL      = "https://pan.ncepu.edu.cn"
)

type SessionManager struct {
	store   CredentialStore
	client  *http.Client
	loginUI LoginUI
}

func NewSessionManager(store CredentialStore) *SessionManager {
	return &SessionManager{
		store:   store,
		client:  &http.Client{Timeout: checkTimeout},
		loginUI: nil, // determined at login time
	}
}

func (sm *SessionManager) SetLoginUI(ui LoginUI) { sm.loginUI = ui }

func (sm *SessionManager) resolveLoginUI() LoginUI {
	if sm.loginUI != nil {
		return sm.loginUI
	}
	if IsWebView2Available() {
		return NewWebView2LoginUI()
	}
	return NewCDPLoginUI()
}

// LoginInteractive starts interactive login. It tries WebView2 first,
// then falls back to CDP (external browser via Chrome DevTools Protocol)
// if WebView2 is unavailable.
func (sm *SessionManager) LoginInteractive() error {
	ui := sm.resolveLoginUI()
	ctx := context.Background()

	sess, err := ui.Login(ctx)
	if err != nil {
		// If WebView2 CookieManager is not compiled in, fall back to CDP.
		if errors.Is(err, ErrCookieManagerUnavailable) || errors.Is(err, ErrWebViewUnavailable) {
			fmt.Println("[auth] WebView2 unavailable; falling back to CDP login...")
			cdpUI := NewCDPLoginUI()
			sess, err = cdpUI.Login(ctx)
		}
	}
	if err != nil {
		return err
	}

	if len(sess.Cookies) > 0 {
		var ui *UserInfo
		var probeErr error
		maxRetries := 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			ui, probeErr = ProbeSessionWithToken(ctx, sess.Cookies, sess.AccessToken)
			if probeErr == nil {
				break
			}
			// If session not yet established (OAuth2 token exchange in progress),
			// wait and retry.
			if errors.Is(probeErr, ErrSessionExpired) && attempt < maxRetries {
				time.Sleep(3 * time.Second)
				continue
			}
			break
		}
		if probeErr != nil {
			return fmt.Errorf("session validation failed: %w", probeErr)
		}
		sess.Account = ui.Account
		sess.Name = ui.Name
		sess.UserID = ui.UserID
		if sess.RootDocID == "" {
			return fmt.Errorf("auth: login incomplete — root docid not captured. The browser may have closed too early.")
		}
	} else if sess.AccessToken != "" {
		if err := sm.CheckSession(sess); err != nil {
			return fmt.Errorf("session validation failed: %w", err)
		}
	} else {
		return fmt.Errorf("%w: no cookies or token", ErrNotAuthenticated)
	}

	if err := sm.SaveSession(sess); err != nil {
		return err
	}
	fmt.Println("Login Success.")
	return nil
}

func (sm *SessionManager) LoadSession() (*Session, error) {
	token, err := sm.store.Get("access_token")
	if err != nil || token == "" {
		return nil, ErrNotAuthenticated
	}
	user, _ := sm.store.Get("user")
	account, _ := sm.store.Get("account")
	name, _ := sm.store.Get("name")
	userid, _ := sm.store.Get("userid")
	s := &Session{
		AccessToken: token,
		User:        user,
		Account:     account,
		Name:        name,
		UserID:      userid,
	}
	if v, err := sm.store.Get("root_docid"); err == nil && v != "" {
		s.RootDocID = v
	}
	if v, err := sm.store.Get("expires_at"); err == nil && v != "" {
		if ts, _ := strconv.ParseInt(v, 10, 64); ts > 0 {
			s.ExpiresAt = time.Unix(ts, 0)
		}
	}
	if v, err := sm.store.Get("cookies"); err == nil && v != "" {
		json.Unmarshal([]byte(v), &s.Cookies)
	}
	if s.IsExpired() {
		sm.InvalidateSession()
		return nil, ErrSessionExpired
	}
	return s, nil
}

func (sm *SessionManager) SaveSession(s *Session) error {
	if s == nil {
		return fmt.Errorf("auth: cannot save nil session")
	}
	if s.AccessToken != "" {
		sm.store.Set("access_token", s.AccessToken)
	}
	if len(s.Cookies) > 0 {
		data, _ := json.Marshal(s.Cookies)
		sm.store.Set("cookies", string(data))
	}
	if s.User != "" {
		sm.store.Set("user", s.User)
	}
	if s.Account != "" {
		sm.store.Set("account", s.Account)
	}
	if s.Name != "" {
		sm.store.Set("name", s.Name)
	}
	if s.UserID != "" {
		sm.store.Set("userid", s.UserID)
	}
	if s.RootDocID != "" {
		sm.store.Set("root_docid", s.RootDocID)
	}
	if !s.ExpiresAt.IsZero() {
		sm.store.Set("expires_at", strconv.FormatInt(s.ExpiresAt.Unix(), 10))
	}
	return nil
}

func (sm *SessionManager) InvalidateSession() error {
	sm.store.Delete("access_token")
	sm.store.Delete("cookies")
	sm.store.Delete("user")
	sm.store.Delete("account")
	sm.store.Delete("name")
	sm.store.Delete("userid")
	sm.store.Delete("root_docid")
	sm.store.Delete("expires_at")
	if fs, ok := sm.store.(*fileCredentialStore); ok {
		fs.Destroy()
	}
	return nil
}

func (sm *SessionManager) CheckSession(s *Session) error {
	if s == nil || s.AccessToken == "" {
		return ErrNotAuthenticated
	}
	if s.IsExpired() {
		return ErrSessionExpired
	}
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", baseURL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+s.AccessToken)
	req.Header.Set("User-Agent", "HuadianDrive/1.0")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN")
	resp, err := sm.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth check: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		sm.InvalidateSession()
		return ErrSessionExpired
	}
	if resp.StatusCode < 400 {
		return nil
	}
	return fmt.Errorf("auth check: remote status %d", resp.StatusCode)
}

func (sm *SessionManager) Attach(req *http.Request) error {
	sess, err := sm.LoadSession()
	if err != nil {
		return err
	}
	if sess.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+sess.AccessToken)
	}
	ch := CookiesToHeaderForDomain(sess.Cookies, "ncepu.edu.cn")
	if ch != "" {
		existing := req.Header.Get("Cookie")
		if existing != "" {
			ch = existing + "; " + ch
		}
		req.Header.Set("Cookie", ch)
	}
	return nil
}

func (sm *SessionManager) SetHTTPClient(c *http.Client) { sm.client = c }
func (sm *SessionManager) HasSession() bool {
	t, err := sm.store.Get("access_token")
	return err == nil && t != ""
}

func (sm *SessionManager) Status() AuthStatus {
	s, err := sm.LoadSession()
	if err != nil || s == nil {
		return AuthStatus{Server: "pan.ncepu.edu.cn", Authenticated: false, Message: err.Error()}
	}
	return AuthStatus{Server: "pan.ncepu.edu.cn", Authenticated: true, User: s.User, ExpiresAt: s.ExpiresAt}
}

type AuthStatus struct {
	Server, User, Message string
	Authenticated         bool
	ExpiresAt             time.Time
}

func (as AuthStatus) String() string {
	var b strings.Builder
	b.WriteString("Server: " + as.Server + "\n")
	if as.Authenticated {
		b.WriteString("Authenticated: true\n")
		if as.User != "" {
			b.WriteString("User: " + as.User + "\n")
		}
		if !as.ExpiresAt.IsZero() {
			b.WriteString("Expires: " + as.ExpiresAt.Format("2006-01-02 15:04:05") + "\n")
		}
	} else {
		b.WriteString("Authenticated: false\n")
		if as.Message != "" {
			b.WriteString("Message: " + as.Message + "\n")
		}
	}
	return b.String()
}

// Diagnose prints a detailed but redacted summary of the stored
// session to stderr. No secret values (tokens, cookies, user IDs)
// are exposed. Only existence, count, and state booleans are shown.
func (sm *SessionManager) Diagnose() error {
	sess, err := sm.LoadSession()
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Server: pan.ncepu.edu.cn\n")
	fmt.Fprintf(os.Stderr, "Session:\n")
	fmt.Fprintf(os.Stderr, "  AccessToken exists: %v\n", sess.AccessToken != "")
	fmt.Fprintf(os.Stderr, "  Cookie count:       %d\n", len(sess.Cookies))
	for _, c := range sess.Cookies {
		fmt.Fprintf(os.Stderr, "    - name=%s domain=%s secure=%v httpOnly=%v\n",
			c.Name, c.Domain, c.Secure, c.HttpOnly)
	}
	fmt.Fprintf(os.Stderr, "  Account set:        %v\n", sess.Account != "")
	fmt.Fprintf(os.Stderr, "  UserID set:         %v\n", sess.UserID != "")
	fmt.Fprintf(os.Stderr, "  RootDocID set:      %v\n", sess.RootDocID != "")
	fmt.Fprintf(os.Stderr, "  Expired:            %v\n", sess.IsExpired())
	fmt.Fprintf(os.Stderr, "Credentials:\n")
	fmt.Fprintf(os.Stderr, "  access_token_present: %v\n", sess.AccessToken != "")
	fmt.Fprintf(os.Stderr, "  root_doc_id_present:  %v\n", sess.RootDocID != "")
	fmt.Fprintf(os.Stderr, "  cookie_count:         %d\n", len(sess.Cookies))
	fmt.Fprintf(os.Stderr, "  account_set:          %v\n", sess.Account != "")
	fmt.Fprintf(os.Stderr, "  userid_set:           %v\n", sess.UserID != "")
	DiagnoseHeaders()
	return nil
}

func (sm *SessionManager) AuthTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &Transport{Base: base, GetToken: func() string {
		s, _ := sm.LoadSession()
		if s != nil {
			return s.AccessToken
		}
		return ""
	}}
}

func (sm *SessionManager) Store() CredentialStore { return sm.store }

var _ Authenticator = (*SessionManager)(nil)

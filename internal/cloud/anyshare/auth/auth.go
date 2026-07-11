// Package auth provides authentication and session management
// for the AnyShare cloud provider.
package auth

import (
	"net/http"
	"time"
)

// Authenticator manages authentication lifecycle.
type Authenticator interface {
	LoadSession() (*Session, error)
	SaveSession(s *Session) error
	InvalidateSession() error
	CheckSession(s *Session) error
}

// CredentialStore abstracts secure storage of credentials.
type CredentialStore interface {
	Get(key string) (string, error)
	Set(key, value string) error
	Delete(key string) error
}

// Session holds AnyShare authentication state.
type Session struct {
	AccessToken string         `json:"access_token"`
	Cookies     []StoredCookie `json:"cookies,omitempty"`
	Account     string         `json:"account,omitempty"`
	Name        string         `json:"name,omitempty"`
	UserID      string         `json:"userid,omitempty"`
	User        string         `json:"user,omitempty"`
	RootDocID   string         `json:"root_docid,omitempty"`
	ExpiresAt   time.Time      `json:"expires_at"`
}

// IsExpired returns true if the session has an expiration time
// that has already passed.
func (s *Session) IsExpired() bool {
	if s == nil {
		return true
	}
	if s.ExpiresAt.IsZero() {
		return false
	}
	return time.Now().After(s.ExpiresAt)
}

// Transport is an http.RoundTripper that injects the current
// session token into requests.
type Transport struct {
	Base     http.RoundTripper
	GetToken func() string
}

func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	token := t.GetToken()
	if token != "" {
		req2 := new(http.Request)
		*req2 = *req
		req2.Header = make(http.Header, len(req.Header))
		for k, vs := range req.Header {
			req2.Header[k] = append([]string(nil), vs...)
		}
		req2.Header.Set("Authorization", "Bearer "+token)
		return base.RoundTrip(req2)
	}
	return base.RoundTrip(req)
}

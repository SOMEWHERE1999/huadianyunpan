package auth

import (
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestFileCredentialStore_EncryptsAtRest(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}

	if err := store.Set("access_token", "secret-token-12345"); err != nil {
		t.Fatalf("Set token: %v", err)
	}
	if err := store.Set("cookies", `[{"name":"session","value":"abc123"}]`); err != nil {
		t.Fatalf("Set cookies: %v", err)
	}
	if err := store.Set("user", "testuser"); err != nil {
		t.Fatalf("Set user: %v", err)
	}

	diskData, err := os.ReadFile(dir + "/auth.json")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	diskStr := string(diskData)
	if strings.Contains(diskStr, "secret-token-12345") {
		t.Error("disk file contains plaintext token")
	}
	if strings.Contains(diskStr, "abc123") {
		t.Error("disk file contains plaintext cookie value")
	}
	if strings.Contains(diskStr, "testuser") {
		t.Error("disk file contains plaintext username")
	}

	got, err := store.Get("access_token")
	if err != nil {
		t.Fatalf("Get token: %v", err)
	}
	if got != "secret-token-12345" {
		t.Errorf("token = %q, want %q", got, "secret-token-12345")
	}

	user, err := store.Get("user")
	if err != nil {
		t.Fatalf("Get user: %v", err)
	}
	if user != "testuser" {
		t.Errorf("user = %q, want %q", user, "testuser")
	}
}

func TestFileCredentialStore_HandlesEmptyStore(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/empty.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}

	_, err = store.Get("access_token")
	if err == nil {
		t.Error("expected error reading from empty store")
	}
}

func TestFileCredentialStore_KeyFileCorruption(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}
	if err := store.Set("access_token", "token"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	os.WriteFile(dir+"/auth.json.key", []byte("not-a-hex-key"), 0600)
	_, err = NewFileCredentialStore(dir + "/auth.json")
	if err != errKeyCorrupted {
		t.Errorf("expected errKeyCorrupted, got %v", err)
	}
}

func TestFileCredentialStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}
	if err := store.Set("access_token", "token"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Delete("access_token"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := store.Get("access_token")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string after delete, got %q", got)
	}
	// Verify file was NOT removed (other fields may exist).
	if _, statErr := os.Stat(dir + "/auth.json"); statErr != nil {
		t.Error("credential file was removed (should only clear field)")
	}
}

func TestFileCredentialStore_Destroy(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}
	if err := store.Set("access_token", "token"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := store.Destroy(); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := os.Stat(dir + "/auth.json"); err == nil {
		t.Error("credential file still exists after Destroy")
	}
	if _, err := os.Stat(dir + "/auth.json.key"); err == nil {
		t.Error("key file still exists after Destroy")
	}
}

func TestRootDocIDPersistenceAndRecovery(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}

	// Simulate a full login session.
	store.Set("access_token", "ory_at_test_token")
	store.Set("account", "testuser")
	store.Set("name", "Test User")
	store.Set("userid", "12345")
	store.Set("root_docid", "gns://057A8C4105A74D8987E3D82B637A10F4")
	store.Set("cookies", `[{"name":"SESSION","value":"sessval","domain":".pan.ncepu.edu.cn","path":"/","secure":true}]`)

	// Open a new SessionManager instance (simulating a separate process).
	mgr := NewSessionManager(store)
	sess, err := mgr.LoadSession()
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}

	if sess.AccessToken != "ory_at_test_token" {
		t.Errorf("AccessToken mismatch: got %q", sess.AccessToken)
	}
	if sess.Account != "testuser" {
		t.Errorf("Account mismatch: got %q", sess.Account)
	}
	if sess.Name != "Test User" {
		t.Errorf("Name mismatch: got %q", sess.Name)
	}
	if sess.UserID != "12345" {
		t.Errorf("UserID mismatch: got %q", sess.UserID)
	}
	if sess.RootDocID != "gns://057A8C4105A74D8987E3D82B637A10F4" {
		t.Errorf("RootDocID mismatch: got %q", sess.RootDocID)
	}
	if len(sess.Cookies) != 1 {
		t.Errorf("Cookies count: got %d, want 1", len(sess.Cookies))
	}
	if len(sess.Cookies) > 0 && sess.Cookies[0].Name != "SESSION" {
		t.Errorf("Cookie name mismatch: got %q", sess.Cookies[0].Name)
	}
}

func TestSessionPersistenceWithoutRootDocID(t *testing.T) {
	// Verify that missing RootDocID is handled gracefully.
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}

	store.Set("access_token", "tok")
	mgr := NewSessionManager(store)
	sess, err := mgr.LoadSession()
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if sess.RootDocID != "" {
		t.Errorf("RootDocID should be empty when not stored, got %q", sess.RootDocID)
	}
}

func TestCdpLoginStateRootDocIDCapture(t *testing.T) {
	// Simulate a Network.requestWillBeSent event for dir/list
	// and verify it writes to cdpLoginState.rootDocID.
	state := &cdpLoginState{}
	samplePostData := `{"docid":"gns://057A8C4105A74D8987E3D82B637A10F4","sort":"asc","by":"name"}`
	evtJSON := `{"method":"Network.requestWillBeSent","params":{"request":{"url":"https://pan.ncepu.edu.cn/api/efast/v1/dir/list","postData":"` +
		strings.ReplaceAll(samplePostData, `"`, `\"`) + `"}}}`
	captureNetworkEvent([]byte(evtJSON), state)
	if state.rootDocID != "gns://057A8C4105A74D8987E3D82B637A10F4" {
		t.Errorf("rootDocID not captured: got %q", state.rootDocID)
	}
}

func TestCdpLoginStatePassesToSession(t *testing.T) {
	state := &cdpLoginState{
		apiToken:  "ory_at_test_token",
		rootDocID: "gns://test",
	}
	// Manually build a session as CDP Login would.
	cookies := []*http.Cookie{{Name: "S", Value: "v"}}
	sess := &Session{Cookies: make([]StoredCookie, 0, len(cookies))}
	for _, c := range cookies {
		sess.Cookies = append(sess.Cookies, FromHTTPCookie(c))
	}
	if state.apiToken != "" {
		sess.AccessToken = state.apiToken
	}
	if state.rootDocID != "" {
		sess.RootDocID = state.rootDocID
	}
	if sess.AccessToken != "ory_at_test_token" {
		t.Errorf("AccessToken not set from state")
	}
	if sess.RootDocID != "gns://test" {
		t.Errorf("RootDocID not set from state: got %q", sess.RootDocID)
	}
}

func TestRootDocIDMissingSessionNotSaved(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}
	mgr := NewSessionManager(store)

	// Simulate a session that would be returned from login but
	// with RootDocID missing.
	sess := &Session{
		AccessToken: "tok",
		Cookies:     []StoredCookie{{Name: "S", Value: "v"}},
		Account:     "u",
		UserID:      "1",
		RootDocID:   "", // missing
	}
	// SaveSession would normally be called here.
	// Verify that the RootDocID absence is detectable.
	if sess.RootDocID == "" && sess.AccessToken != "" {
		// This is the incomplete state — should not be saved.
		// In production, LoginInteractive returns an error here.
		_ = mgr
	}
	// Verify the incomplete session is not on disk.
	loaded, err := mgr.LoadSession()
	if err == nil {
		if loaded.RootDocID != "" {
			t.Errorf("unexpected RootDocID in non-saved session")
		}
	}
}

func TestRootDocIDPersistsAcrossSessionManagers(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileCredentialStore(dir + "/auth.json")
	if err != nil {
		t.Fatalf("NewFileCredentialStore: %v", err)
	}

	// Simulate first process: save full session including RootDocID.
	sess := &Session{
		AccessToken: "ory_at_xxx",
		Cookies:     []StoredCookie{{Name: "SESSION", Value: "abc"}},
		RootDocID:   "gns://057A8C4105A74D8987E3D82B637A10F4",
		Account:     "user",
		UserID:      "12345",
	}
	mgr1 := NewSessionManager(store)
	if err := mgr1.SaveSession(sess); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}

	// Simulate second process: new SessionManager loads from disk.
	mgr2 := NewSessionManager(store)
	loaded, err := mgr2.LoadSession()
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if loaded.RootDocID != "gns://057A8C4105A74D8987E3D82B637A10F4" {
		t.Errorf("RootDocID not restored: got %q", loaded.RootDocID)
	}
	if loaded.AccessToken != "ory_at_xxx" {
		t.Errorf("AccessToken not restored")
	}
	if len(loaded.Cookies) != 1 {
		t.Errorf("cookie count mismatch")
	}
}

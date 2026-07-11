package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type fileCredentialStore struct {
	path   string
	encKey []byte
}

type storedData struct {
	Token     string         `json:"token"`
	Cookies   []StoredCookie `json:"cookies,omitempty"`
	User      string         `json:"user,omitempty"`
	Account   string         `json:"account,omitempty"`
	Name      string         `json:"name,omitempty"`
	UserID    string         `json:"userid,omitempty"`
	RootDocID string         `json:"root_docid,omitempty"`
	ExpiresAt int64          `json:"expires_at"`
}

func NewFileCredentialStore(path string) (*fileCredentialStore, error) {
	if path == "" {
		appData := os.Getenv("LOCALAPPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Local")
		}
		path = filepath.Join(appData, "HuadianDrive", "auth.json")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	key, err := loadOrCreateKey(deriveKeyPath(path))
	if err != nil {
		return nil, err
	}
	return &fileCredentialStore{path: path, encKey: key}, nil
}

func (s *fileCredentialStore) readData() (*storedData, error) {
	ciphertext, err := os.ReadFile(s.path)
	if err != nil {
		return nil, err
	}
	plaintext, err := decryptData(s.encKey, ciphertext)
	if err != nil {
		return nil, err
	}
	var d storedData
	if err := json.Unmarshal(plaintext, &d); err != nil {
		return nil, err
	}
	return &d, nil
}

func (s *fileCredentialStore) writeData(d *storedData) error {
	plaintext, err := json.Marshal(d)
	if err != nil {
		return err
	}
	ciphertext, err := encryptData(s.encKey, plaintext)
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, ciphertext, 0600)
}

func (s *fileCredentialStore) Get(key string) (string, error) {
	d, err := s.readData()
	if err != nil {
		return "", err
	}
	switch key {
	case "access_token":
		return d.Token, nil
	case "user":
		return d.User, nil
	case "account":
		return d.Account, nil
	case "name":
		return d.Name, nil
	case "userid":
		return d.UserID, nil
	case "root_docid":
		return d.RootDocID, nil
	case "cookies":
		data, _ := json.Marshal(d.Cookies)
		return string(data), nil
	}
	return "", os.ErrNotExist
}

func (s *fileCredentialStore) Set(key, value string) error {
	d, _ := s.readData()
	if d == nil {
		d = &storedData{}
	}
	switch key {
	case "access_token":
		d.Token = value
	case "user":
		d.User = value
	case "account":
		d.Account = value
	case "name":
		d.Name = value
	case "userid":
		d.UserID = value
	case "root_docid":
		d.RootDocID = value
	case "cookies":
		json.Unmarshal([]byte(value), &d.Cookies)
	case "expires_at":
		var t int64
		json.Unmarshal([]byte(value), &t)
		d.ExpiresAt = t
	}
	return s.writeData(d)
}

func (s *fileCredentialStore) Delete(key string) error {
	d, err := s.readData()
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	switch key {
	case "access_token":
		d.Token = ""
	case "user":
		d.User = ""
	case "account":
		d.Account = ""
	case "name":
		d.Name = ""
	case "userid":
		d.UserID = ""
	case "root_docid":
		d.RootDocID = ""
	case "cookies":
		d.Cookies = nil
	case "expires_at":
		d.ExpiresAt = 0
	default:
		return nil
	}
	return s.writeData(d)
}

// Destroy removes the credential file and its encryption key.
func (s *fileCredentialStore) Destroy() error {
	os.Remove(deriveKeyPath(s.path))
	return os.Remove(s.path)
}

var _ CredentialStore = (*fileCredentialStore)(nil)

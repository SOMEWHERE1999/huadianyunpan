package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"ncepupan/hdd/internal/cloud"
	"ncepupan/hdd/internal/domain"
)

type Action struct {
	Type       ActionType
	LocalPath  string
	RemotePath string
}

type ActionType int

const (
	ActionUpload ActionType = iota
	ActionDownload
	ActionDeleteLocal
	ActionConflict
	ActionNothing
)

func (a ActionType) String() string {
	switch a {
	case ActionUpload:
		return "upload"
	case ActionDownload:
		return "download"
	case ActionDeleteLocal:
		return "delete-local"
	case ActionConflict:
		return "conflict"
	default:
		return "nothing"
	}
}

type State struct {
	LocalModTime  time.Time
	RemoteModTime time.Time
	RemoteETag    string
}

type Syncer struct {
	provider cloud.Provider
	state    map[string]State
}

func New(provider cloud.Provider) *Syncer {
	return &Syncer{provider: provider, state: make(map[string]State)}
}

func (s *Syncer) LoadState(localPath string, st State) {
	s.state[localPath] = st
}

// localToRemote converts a local Windows path to a remote path.
func localToRemote(local string) string {
	p := filepath.ToSlash(local)
	if len(p) >= 2 && p[1] == ':' {
		p = p[2:]
	}
	return p
}

func (s *Syncer) Diff(localFiles map[string]time.Time, remoteFiles map[string]domain.FileInfo) []Action {
	var actions []Action
	seenRemote := make(map[string]bool)

	// Process local files.
	for localPath, localMod := range localFiles {
		remotePath := localToRemote(localPath)
		fi, existsRemote := remoteFiles[remotePath]
		if existsRemote {
			seenRemote[remotePath] = true
		}
		st, known := s.state[localPath]

		switch {
		case !existsRemote && !known:
			actions = append(actions, Action{ActionUpload, localPath, remotePath})
		case !existsRemote && known:
			actions = append(actions, Action{ActionUpload, localPath, remotePath})
		case existsRemote && known:
			localChanged := !localMod.Equal(st.LocalModTime)
			remoteChanged := !fi.ModTime.Equal(st.RemoteModTime)
			if localChanged && !remoteChanged {
				actions = append(actions, Action{ActionUpload, localPath, remotePath})
			} else if !localChanged && remoteChanged {
				actions = append(actions, Action{ActionDownload, localPath, remotePath})
			} else if localChanged && remoteChanged {
				actions = append(actions, Action{ActionConflict, localPath, remotePath})
			}
		}

		if existsRemote {
			s.state[localPath] = State{localMod, fi.ModTime, fi.ETag}
		} else {
			delete(s.state, localPath)
		}
	}

	// Remote-only files → download.
	for remotePath, fi := range remoteFiles {
		if seenRemote[remotePath] {
			continue
		}
		localPath := filepath.FromSlash(strings.TrimPrefix(remotePath, "/"))
		if !strings.HasPrefix(remotePath, "/") {
			localPath = filepath.FromSlash(remotePath)
		}
		actions = append(actions, Action{ActionDownload, localPath, remotePath})
		s.state[localPath] = State{time.Time{}, fi.ModTime, fi.ETag}
	}

	return actions
}

func ConflictName(originalPath, suffix string, t time.Time) string {
	dir := filepath.Dir(originalPath)
	ext := filepath.Ext(originalPath)
	base := strings.TrimSuffix(filepath.Base(originalPath), ext)
	ts := t.Format("20060102-150405")
	return filepath.Join(dir, fmt.Sprintf("%s.conflict-%s-%s%s", base, suffix, ts, ext))
}

func CreateConflictCopies(localPath string, downloadFn func(string) error) (string, error) {
	now := time.Now()
	localCopy := ConflictName(localPath, "local", now)
	if err := copyFile(localPath, localCopy); err != nil {
		return "", fmt.Errorf("conflict local copy: %w", err)
	}
	remoteCopy := ConflictName(localPath, "remote", now)
	if err := downloadFn(remoteCopy); err != nil {
		return "", fmt.Errorf("conflict remote copy: %w", err)
	}
	return remoteCopy, nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0644)
}

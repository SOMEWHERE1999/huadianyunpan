// Package domain defines shared domain types used across the application.
package domain

import "time"

// SyncStatus represents the synchronization state of a remote file.
type SyncStatus string

const (
	StatusSynced     SyncStatus = "synced"
	StatusPending    SyncStatus = "pending"
	StatusConflicted SyncStatus = "conflicted"
	StatusError      SyncStatus = "error"
)

// FileInfo holds metadata for a remote file.
type FileInfo struct {
	Path       string
	Size       int64
	ModTime    time.Time
	IsDir      bool
	ETag       string
	SyncStatus SyncStatus
}

// SyncRoot represents a directory synchronized with the cloud.
type SyncRoot struct {
	LocalPath  string
	RemotePath string
}

// FileEvent represents a local filesystem change.
type FileEvent struct {
	Path string
	Op   FileOp
}

// FileOp describes the kind of filesystem operation.
type FileOp int

const (
	OpCreate FileOp = iota
	OpModify
	OpDelete
	OpRename
)

// String returns a human-readable name for the operation.
func (o FileOp) String() string {
	switch o {
	case OpCreate:
		return "create"
	case OpModify:
		return "modify"
	case OpDelete:
		return "delete"
	case OpRename:
		return "rename"
	default:
		return "unknown"
	}
}

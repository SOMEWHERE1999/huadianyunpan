// Package cloud defines the Provider interface for cloud storage backends.
package cloud

import (
	"context"
	"io"

	"ncepupan/hdd/internal/domain"
)

type UploadConflictPolicy string

const (
	UploadConflictFail       UploadConflictPolicy = "fail"
	UploadConflictAutoRename UploadConflictPolicy = "auto-rename"
	UploadConflictOverwrite  UploadConflictPolicy = "overwrite"
)

type DirectoryUploadConflictPolicy string

const (
	DirectoryConflictFail       DirectoryUploadConflictPolicy = "fail"
	DirectoryConflictAutoRename DirectoryUploadConflictPolicy = "auto-rename"
	DirectoryConflictMerge      DirectoryUploadConflictPolicy = "merge"
)

type TransferConflictPolicy string

const (
	TransferConflictFail       TransferConflictPolicy = "fail"
	TransferConflictAutoRename TransferConflictPolicy = "auto-rename"
	TransferConflictOverwrite  TransferConflictPolicy = "overwrite"
	TransferConflictMerge      TransferConflictPolicy = "merge"
)

type UploadResult struct {
	RequestedPath string
	FinalPath     string
	FinalName     string
	Size          int64
	Revision      string
	Updated       bool
}

type TransferResult struct {
	SourcePath           string
	DestinationDirectory string
	FinalPath            string
	FinalName            string
}

// DirectRemoteProvider contains operations used only by hddctl remote. Keeping
// this separate avoids coupling synchronization providers to interactive CLI
// conflict policies.
type DirectRemoteProvider interface {
	UploadFile(ctx context.Context, localPath, remotePath string, policy UploadConflictPolicy) (UploadResult, error)
	UploadDirectory(ctx context.Context, localDirectory, remoteParent string, policy DirectoryUploadConflictPolicy) (UploadResult, error)
	Copy(ctx context.Context, sourcePath, destinationDirectory string, policy TransferConflictPolicy) (TransferResult, error)
	Move(ctx context.Context, sourcePath, destinationDirectory string, policy TransferConflictPolicy) (TransferResult, error)
}

// Provider is the interface that every cloud backend must implement.
type Provider interface {
	// Name returns the provider name (e.g. "huadian", "mock").
	Name() string

	// Connect authenticates and establishes a session.
	Connect(ctx context.Context) error

	// Disconnect tears down the session.
	Disconnect(ctx context.Context) error

	// List returns file metadata for entries under remotePath.
	List(ctx context.Context, remotePath string) ([]domain.FileInfo, error)

	// Stat returns metadata for a single file or directory.
	Stat(ctx context.Context, remotePath string) (domain.FileInfo, error)

	// Upload reads from r and stores the content at remotePath.
	Upload(ctx context.Context, remotePath string, r io.Reader) error

	// Download retrieves a remote file and writes its content to w.
	Download(ctx context.Context, remotePath string, w io.Writer) error

	// Mkdir creates a directory at remotePath.
	Mkdir(ctx context.Context, remotePath string) error

	// Remove deletes a file or empty directory.
	Remove(ctx context.Context, remotePath string) error

	// Rename moves or renames a file or directory.
	Rename(ctx context.Context, oldPath, newPath string) error
}

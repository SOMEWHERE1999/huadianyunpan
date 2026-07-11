// Package ipc defines platform-independent IPC protocol types.
package ipc

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// MaxMessageSize is the largest allowed message in bytes.
const MaxMessageSize = 1 << 20 // 1 MB

// Request is a message sent from client to server.
type Request struct {
	Type string          `json:"type"`
	ID   string          `json:"id"`
	Data json.RawMessage `json:"data,omitempty"`
}

// Response is a message sent from server to client.
type Response struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// StatusData is the payload for a "status" response.
type StatusData struct {
	Provider string `json:"provider"`
}

// --- FUSE filesystem IPC types ---

// FSEntry is a single directory entry returned by fs.list.
type FSEntry struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	IsDir   bool   `json:"is_dir"`
	ModTime string `json:"mod_time"`
}

// FSListData is the payload for fs.list response.
type FSListData struct {
	Entries []FSEntry `json:"entries"`
}

// FSStatData is the payload for fs.stat response.
type FSStatData struct {
	Entry FSEntry `json:"entry"`
}

// FSOpenData is the payload for fs.open response.
type FSOpenData struct {
	CachePath string `json:"cache_path"`
	Size      int64  `json:"size"`
}

// FSCreateData is the payload for fs.create response.
type FSCreateData struct {
	CachePath string `json:"cache_path"`
}

// FSRenameRequest is the payload for fs.rename request.
type FSRenameRequest struct {
	OldPath string `json:"old"`
	NewPath string `json:"new"`
}

// FSRemoveRequest is the payload for fs.remove request.
type FSRemoveRequest struct {
	Path string `json:"path"`
}

// FSSetattrRequest is the payload for fs.setattr request.
type FSSetattrRequest struct {
	Path  string `json:"path"`
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
}

// FSUploadStagedRequest is the payload for fs.uploadStaged request.
// The staging path is relative to the daemon's staging root directory.
type FSUploadStagedRequest struct {
	RemotePath     string `json:"remotePath"`
	StagingPath    string `json:"stagingPath"`    // relative to staging root
	Size           int64  `json:"size"`
	ConflictPolicy string `json:"conflictPolicy"` // "fail", "overwrite", "auto_rename"
}

// FSUploadStagedResponse is the payload for fs.uploadStaged response.
type FSUploadStagedResponse struct {
	RemotePath string `json:"remotePath,omitempty"`
	Size       int64  `json:"size"`
}

// Encode writes a length-prefixed JSON message to w.
// Format: 4 bytes big-endian length, then JSON.
// The entire frame is written in a single write to preserve
// message boundaries on message-mode transports.
func Encode(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("ipc marshal: %w", err)
	}
	if len(data) > MaxMessageSize {
		return fmt.Errorf("ipc: message too large (%d bytes)", len(data))
	}

	frame := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(data)))
	copy(frame[4:], data)

	n, err := w.Write(frame)
	if err != nil {
		return fmt.Errorf("ipc write: %w", err)
	}
	if n != len(frame) {
		return fmt.Errorf("ipc write: short write (%d of %d bytes)", n, len(frame))
	}
	return nil
}

// Decode reads a length-prefixed JSON message from r into v.
func Decode(r io.Reader, v any) error {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return fmt.Errorf("ipc read header: %w", err)
	}

	size := binary.BigEndian.Uint32(header[:])
	if size > MaxMessageSize {
		return fmt.Errorf("ipc: message too large (%d bytes)", size)
	}

	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return fmt.Errorf("ipc read body: %w", err)
	}

	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("ipc unmarshal: %w", err)
	}
	return nil
}

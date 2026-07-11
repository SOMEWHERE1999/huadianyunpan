package logging

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
)

// SecurityEvent is the complete allow-list of diagnostic log fields.
type SecurityEvent struct {
	Operation  string
	Method     string
	Path       string
	Status     int
	RequestID  string
	ErrorClass string
}

// LogSecurityEvent emits a diagnostic event using only approved fields.
func LogSecurityEvent(ctx context.Context, event SecurityEvent) {
	slog.LogAttrs(ctx, slog.LevelInfo, "security_event",
		slog.String("operation", event.Operation),
		slog.String("method", event.Method),
		slog.String("redacted_path", RedactPath(event.Path)),
		slog.Int("status", event.Status),
		slog.String("request_id", event.RequestID),
		slog.String("error_class", event.ErrorClass),
	)
}

// RedactPath discards URL query/fragment data and returns an irreversible
// identifier instead of a URL, remote path, or local filesystem path.
func RedactPath(raw string) string {
	value := raw
	if parsed, err := url.Parse(raw); err == nil && strings.Contains(raw, "://") {
		value = parsed.Path
	}
	if i := strings.IndexAny(value, "?#"); i >= 0 {
		value = value[:i]
	}
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" {
		return ""
	}
	if value == "/" {
		return "/"
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", sum[:6])
}

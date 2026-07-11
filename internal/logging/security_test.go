package logging

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestLogSecurityEventAllowListAndRedaction(t *testing.T) {
	const secretPath = `C:\Users\student\Private File.txt?token=SECRET_QUERY`
	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&output, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	LogSecurityEvent(context.Background(), SecurityEvent{Operation: "download", Method: "GET", Path: secretPath, Status: 403, RequestID: "request-123", ErrorClass: "unauthorized"})

	got := output.String()
	for _, field := range []string{"operation=download", "method=GET", "redacted_path=sha256:", "status=403", "request_id=request-123", "error_class=unauthorized"} {
		if !strings.Contains(got, field) {
			t.Errorf("log missing allowed field %q: %s", field, got)
		}
	}
	for _, secret := range []string{"Users", "student", "Private", "SECRET_QUERY", "token="} {
		if strings.Contains(got, secret) {
			t.Errorf("log leaked %q: %s", secret, got)
		}
	}
}

func TestRedactPathDropsURLQuery(t *testing.T) {
	first := RedactPath("https://storage.example/private/file?signature=FIRST")
	second := RedactPath("https://storage.example/private/file?signature=SECOND")
	if first != second {
		t.Fatalf("query affected redacted path: %q != %q", first, second)
	}
	if strings.Contains(first, "private") || strings.Contains(first, "signature") {
		t.Fatalf("redacted path leaked input: %q", first)
	}
}

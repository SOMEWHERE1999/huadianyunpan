package auth

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// ConsoleLoginUI implements LoginUI by prompting the user to
// paste a Bearer token in the terminal.
type ConsoleLoginUI struct {
	In  io.Reader
	Out io.Writer
}

// NewConsoleLoginUI creates a ConsoleLoginUI using stdin/stdout.
func NewConsoleLoginUI() *ConsoleLoginUI {
	return &ConsoleLoginUI{In: os.Stdin, Out: os.Stdout}
}

func (ui *ConsoleLoginUI) Login(ctx context.Context) (*Session, error) {
	ui.printLoginGuide()

	var token string
	fmt.Fscanln(ui.In, &token)
	token = parseBearerToken(token)
	if token == "" {
		return nil, fmt.Errorf("auth: %w", ErrInteractiveLoginRequired)
	}

	return &Session{
		AccessToken: token,
	}, nil
}

func (ui *ConsoleLoginUI) printLoginGuide() {
	io.WriteString(ui.Out, "To authenticate with AnyShare, follow these steps:\n\n")
	io.WriteString(ui.Out, "  1. Open your browser to https://pan.ncepu.edu.cn\n")
	io.WriteString(ui.Out, "  2. Complete the CAS login (student ID + password + SMS)\n")
	io.WriteString(ui.Out, "  3. Open Developer Tools (F12) -> Network tab\n")
	io.WriteString(ui.Out, "  4. Find any request to pan.ncepu.edu.cn\n")
	io.WriteString(ui.Out, "  5. Copy the 'Authorization' header value\n")
	io.WriteString(ui.Out, "     (it starts with 'Bearer ')\n\n")
	io.WriteString(ui.Out, "Paste Bearer token here: ")
}

func parseBearerToken(raw string) string {
	t := strings.TrimSpace(raw)
	t = strings.TrimPrefix(t, "Bearer ")
	t = strings.TrimPrefix(t, "bearer ")
	return strings.TrimSpace(t)
}

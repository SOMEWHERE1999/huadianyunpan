//go:build !windows

package auth

import (
	"context"
	"errors"
	"fmt"
)

type cdpStubLoginUI struct{}

func (ui *cdpStubLoginUI) Login(ctx context.Context) (*Session, error) {
	return nil, fmt.Errorf("CDP login is not available on this platform")
}

// NewCDPLoginUI returns a stub that always returns an error on non-Windows platforms.
func NewCDPLoginUI() LoginUI {
	return &cdpStubLoginUI{}
}

var _ = errors.New

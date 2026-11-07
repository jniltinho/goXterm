//go:build !linux

package secret

import "errors"

// Service is a no-op on non-Linux until a macOS/Windows keyring is wired (migrate-gui-to-go-wails).
type Service struct{}

func New() *Service { return &Service{} }

// ErrUnavailable means there is no keyring backend on this platform yet.
var ErrUnavailable = errors.New("secret service unavailable on this platform")

func (*Service) Get(string) ([]byte, bool, error) { return nil, false, ErrUnavailable }
func (*Service) Set(string, []byte) error         { return ErrUnavailable }
func (*Service) Delete(string) error              { return ErrUnavailable }

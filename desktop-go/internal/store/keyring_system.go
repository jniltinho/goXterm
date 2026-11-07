//go:build linux || darwin || windows

package store

import "rustxterm-go/internal/secret"

type ssKeyring struct{ s *secret.Service }

func (k ssKeyring) get(id string) ([]byte, bool, error) { return k.s.Get(id) }
func (k ssKeyring) set(id string, b []byte) error       { return k.s.Set(id, b) }

// SystemKeyring returns the platform keyring: the freedesktop Secret Service on Linux, the login
// Keychain on macOS, the Credential Manager on Windows.
func SystemKeyring() keyring { return ssKeyring{secret.New()} }

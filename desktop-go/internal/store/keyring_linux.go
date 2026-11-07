//go:build linux

package store

import "rustxterm-go/internal/secret"

type ssKeyring struct{ s *secret.Service }

func (k ssKeyring) get(id string) ([]byte, bool, error) { return k.s.Get(id) }

// SystemKeyring returns the platform keyring (Linux: the freedesktop Secret Service).
func SystemKeyring() keyring { return ssKeyring{secret.New()} }

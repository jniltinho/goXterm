//go:build !linux

package store

// SystemKeyring has no backend yet on non-Linux; callers fall back to the master password.
func SystemKeyring() keyring { return NoKeyring{} }

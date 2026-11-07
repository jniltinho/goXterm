//go:build !linux && !darwin && !windows

package store

// SystemKeyring has no backend yet on this platform; callers fall back to the master password.
func SystemKeyring() keyring { return NoKeyring{} }

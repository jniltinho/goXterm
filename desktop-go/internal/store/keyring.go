package store

import "errors"

// keyring fetches the random wrapping secret stored by rustXterm in the platform keyring
// (Linux: the freedesktop Secret Service, items with attributes {application: "rustxterm", id}).
type keyring interface {
	// get returns the secret for id, ok=false when absent, an error when the keyring is unavailable.
	get(id string) (secret []byte, ok bool, err error)
	// set stores the secret for id (store creation with "Unlock with system keyring").
	set(id string, secret []byte) error
}

// NoKeyring always reports absent.
type NoKeyring struct{}

func (NoKeyring) get(string) ([]byte, bool, error) { return nil, false, nil }
func (NoKeyring) set(string, []byte) error         { return errors.New("no system keyring on this platform") }

// Keyring is the platform keyring as seen by callers (SystemKeyring); nil means none.
type Keyring = keyring

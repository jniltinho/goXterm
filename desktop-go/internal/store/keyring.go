package store

// keyring fetches the random wrapping secret stored by rustXterm in the platform keyring
// (Linux: the freedesktop Secret Service, items with attributes {application: "rustxterm", id}).
// The real Secret Service client is wired in migrate-gui-to-go-wails task 2.2; until then the
// no-op keyring makes OpenWithKeyring report "no keyring" and callers fall back to the password.
type keyring interface {
	// get returns the secret for id, ok=false when absent, an error when the keyring is unavailable.
	get(id string) (secret []byte, ok bool, err error)
}

// NoKeyring always reports absent.
type NoKeyring struct{}

func (NoKeyring) get(string) ([]byte, bool, error) { return nil, false, nil }

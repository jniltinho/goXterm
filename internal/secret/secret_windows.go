//go:build windows

package secret

import (
	"errors"

	"github.com/danieljoos/wincred"
)

// Service is the Windows Credential Manager (pure Go, advapi32 Cred* calls): generic credentials named
// "rustxterm:<id>".
type Service struct{}

func New() *Service { return &Service{} }

// ErrUnavailable means the Credential Manager could not be used.
var ErrUnavailable = errors.New("credential manager unavailable")

func target(id string) string { return application + ":" + id }

func (s *Service) Get(id string) ([]byte, bool, error) {
	c, err := wincred.GetGenericCredential(target(id))
	if errors.Is(err, wincred.ErrElementNotFound) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrUnavailable
	}
	return c.CredentialBlob, true, nil
}

func (s *Service) Set(id string, secret []byte) error {
	c := wincred.NewGenericCredential(target(id))
	c.CredentialBlob = secret
	c.Persist = wincred.PersistLocalMachine
	if err := c.Write(); err != nil {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) Delete(id string) error {
	c, err := wincred.GetGenericCredential(target(id))
	if errors.Is(err, wincred.ErrElementNotFound) {
		return nil
	}
	if err != nil {
		return ErrUnavailable
	}
	return c.Delete()
}

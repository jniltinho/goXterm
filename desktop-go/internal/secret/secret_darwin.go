//go:build darwin

package secret

import (
	"bytes"
	"encoding/hex"
	"errors"
	"os/exec"
	"strings"
)

// Service is the macOS login Keychain through /usr/bin/security (no cgo): generic passwords with
// service "rustxterm" and account <id>, the secret stored hex-encoded. Secrets are written through the
// tool's stdin (`security -i`), never on a command line other processes could read.
type Service struct{}

func New() *Service { return &Service{} }

// ErrUnavailable means the Keychain tool could not be run.
var ErrUnavailable = errors.New("keychain unavailable")

const securityTool = "/usr/bin/security"

func (s *Service) Get(id string) ([]byte, bool, error) {
	out, err := exec.Command(securityTool, "find-generic-password", "-s", application, "-a", id, "-w").Output()
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 44 { // errSecItemNotFound
		return nil, false, nil
	}
	if err != nil {
		return nil, false, ErrUnavailable
	}
	b, err := hex.DecodeString(strings.TrimSpace(string(out)))
	if err != nil {
		return nil, false, errors.New("keychain item is not a rustxterm secret")
	}
	return b, true, nil
}

func (s *Service) Set(id string, secret []byte) error {
	if strings.ContainsAny(id, " \t\n\"'") {
		return errors.New("invalid keychain id")
	}
	cmd := exec.Command(securityTool, "-i")
	cmd.Stdin = bytes.NewBufferString("add-generic-password -U -s " + application + " -a " + id + " -w " + hex.EncodeToString(secret) + "\n")
	if out, err := cmd.CombinedOutput(); err != nil || bytes.Contains(out, []byte("rror")) {
		return ErrUnavailable
	}
	return nil
}

func (s *Service) Delete(id string) error {
	err := exec.Command(securityTool, "delete-generic-password", "-s", application, "-a", id).Run()
	var ee *exec.ExitError
	if err == nil || (errors.As(err, &ee) && ee.ExitCode() == 44) {
		return nil
	}
	return ErrUnavailable
}

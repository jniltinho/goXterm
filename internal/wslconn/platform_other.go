//go:build !windows

package wslconn

// Exe has no meaning off Windows.
func Exe() string { return "" }

// Available is false: WSL exists only on Windows.
func Available() bool { return false }

// SupportsCD is false off Windows.
func SupportsCD() bool { return false }

// Distributions fails off Windows.
func Distributions() ([]Distribution, error) { return nil, ErrUnavailable }

// WorkDir has no meaning off Windows.
func WorkDir() string { return "" }

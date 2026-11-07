package fsentry

import (
	"os"
	"testing"
)

func TestLsMode(t *testing.T) {
	for m, want := range map[os.FileMode]string{
		os.ModeDir | 0o755:     "drwxr-xr-x",
		0o600:                  "-rw-------",
		os.ModeSymlink | 0o777: "lrwxrwxrwx",
		0o644:                  "-rw-r--r--",
	} {
		if got := LsMode(m); got != want {
			t.Errorf("%v: %s, want %s", m, got, want)
		}
	}
}

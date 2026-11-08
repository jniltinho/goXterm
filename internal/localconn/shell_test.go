package localconn

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPasswdShell(t *testing.T) {
	p := filepath.Join(t.TempDir(), "passwd")
	_ = os.WriteFile(p, []byte("root:x:0:0:root:/root:/bin/sh\nalice:x:1000:1000::/home/alice:/bin/sh\nbob:x:1001:1001::/home/bob:/no/such/shell\n"), 0o644)
	if got := passwdShell(p, 1000); got != "/bin/sh" {
		t.Fatalf("uid 1000: %q", got)
	}
	if got := passwdShell(p, 1001); got != "" {
		t.Fatalf("missing shell accepted: %q", got)
	}
	if got := passwdShell(p, 4242); got != "" {
		t.Fatalf("unknown uid: %q", got)
	}
}

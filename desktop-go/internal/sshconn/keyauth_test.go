package sshconn

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestLabKeyAuth connects to the lab sshd as bob (AuthenticationMethods publickey) using a private
// key file, proving public-key auth works. Gated: RXT_SSH_KEY_TEST=1 with RXT_SSH_KEY pointing at
// bob's key (lab/.generated/bob_ed25519) and the lab up on 127.0.0.1:2222.
func TestLabKeyAuth(t *testing.T) {
	if os.Getenv("RXT_SSH_KEY_TEST") == "" {
		t.Skip("set RXT_SSH_KEY_TEST=1 and RXT_SSH_KEY to bob's key, lab on 127.0.0.1:2222")
	}
	c, err := DialClient(Config{
		Host: "127.0.0.1", Port: 2222, User: "bob",
		KeyFile: os.Getenv("RXT_SSH_KEY"),
		HostKey: ssh.InsecureIgnoreHostKey(),
	})
	if err != nil {
		t.Fatalf("key-auth dial: %v", err)
	}
	defer c.Close()
	sess, err := c.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	out, err := sess.CombinedOutput("echo keyok")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(string(out), "keyok") {
		t.Fatalf("output = %q", out)
	}
}

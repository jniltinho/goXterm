package gui

import (
	"errors"
	"testing"

	"rustxterm-go/internal/sshconn"
)

// TestEndPolicy: the Rust engine's rules — a remote command's exit closes the tab unless "Do not exit
// after command ends"; only a dropped connection reconnects, and only with "Reconnect automatically".
func TestEndPolicy(t *testing.T) {
	exit, drop := error(sshconn.ExitStatus(0)), errors.New("connection lost")
	cases := []struct {
		name string
		p    endPolicy
		err  error
		want TermEnd
	}{
		{"shell exit", endPolicy{}, exit, TermEnd{}},
		{"command exit closes", endPolicy{remoteCommand: true}, exit, TermEnd{CloseTab: true}},
		{"command exit kept open", endPolicy{remoteCommand: true, keepOpen: true}, exit, TermEnd{}},
		{"drop", endPolicy{}, drop, TermEnd{Dropped: true}},
		{"drop reconnects", endPolicy{autoReconnect: true}, drop, TermEnd{Dropped: true, Reconnect: true}},
		{"exit never reconnects", endPolicy{autoReconnect: true}, exit, TermEnd{}},
	}
	for _, c := range cases {
		if got := c.p.endOf(c.err); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}

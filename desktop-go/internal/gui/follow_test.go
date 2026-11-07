package gui

import (
	"strings"
	"testing"
)

// Same cases as the Rust app's engine/follow.rs tests.
const psTable = `TT           PID    PPID     ELAPSED COMMAND
?              1       0  1-02:03:04 /usr/sbin/sshd -D
?            100       1       05:00 sshd: alice [priv]
?            101     100       04:59 sshd: alice@pts/0,pts/1
pts/0        102     101       04:58 -bash
pts/0        150     102       01:00 sh
pts/1        160     101       00:10 -zsh
?            200     101       00:00 ps -eo tty,pid,ppid,etime,args
`

func without(table, sub string) string {
	var keep []string
	for _, l := range strings.Split(table, "\n") {
		if !strings.Contains(l, sub) {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

func TestFindShell(t *testing.T) {
	if got := findShell("200\n" + psTable); got != 160 {
		t.Errorf("youngest login shell beside the exec: got %d, want 160", got)
	}
	if got := findShell("200\n" + without(psTable, "-zsh")); got != 102 {
		t.Errorf("one PTY channel, never the child shell: got %d, want 102", got)
	}
	var none []string
	for _, l := range strings.Split(psTable, "\n") {
		if !strings.HasPrefix(l, "pts/") {
			none = append(none, l)
		}
	}
	if got := findShell("200\n" + strings.Join(none, "\n")); got != shellNotYet {
		t.Errorf("shell not started yet: got %d", got)
	}
	for _, out := range []string{"200\n", "sh: ps: not found\n", "999\n" + psTable} {
		if got := findShell(out); got != shellUnsupported {
			t.Errorf("findShell(%q) = %d, want unsupported", out[:min(len(out), 20)], got)
		}
	}
}

func TestElapsedSecs(t *testing.T) {
	for in, want := range map[string]int64{"00:07": 7, "01:02:03": 3723, "2-00:00:01": 172801} {
		if got, ok := elapsedSecs(in); !ok || got != want {
			t.Errorf("elapsedSecs(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
	if _, ok := elapsedSecs("x"); ok {
		t.Error(`elapsedSecs("x") must fail`)
	}
}

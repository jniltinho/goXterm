package gui

import (
	"fmt"
	"strings"
	"testing"
)

// Ported from the Rust engine tail tests.
func TestTailStripsEscapes(t *testing.T) {
	var tl tail
	tl.feed([]byte("\x1b[1;32mgreen\x1b[0m ok\r\n\x1b]0;title\x07pro\x1b(Bgress 10%\rprogress 100%\r\nab\x08c\n$ "))
	for n, want := range map[int]string{10: "green ok\nprogress 100%\nac\n$ ", 1: "$ ", 2: "ac\n$ "} {
		if got := tl.last(n); got != want {
			t.Errorf("last(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestTailBoundedAndMarked(t *testing.T) {
	var tl tail
	for i := 0; i < tailLines+10; i++ {
		tl.feed([]byte(fmt.Sprintf("line %d\n", i)))
	}
	if len(tl.lines) != tailLines || !strings.HasSuffix(tl.last(1), fmt.Sprintf("line %d", tailLines+9)) {
		t.Fatalf("lines %d, last %q", len(tl.lines), tl.last(1))
	}
	tl.setMark()
	tl.feed([]byte("uptime\r\n load average: 0.1\r\n$ "))
	if got := tl.sinceMark(); got != "uptime\n load average: 0.1\n$ " {
		t.Errorf("sinceMark = %q", got)
	}
	tl.feed([]byte(strings.Repeat("x", tailLineMax*2)))
	if len(tl.last(1)) > tailLineMax+2 {
		t.Error("a long line is not bounded")
	}
}

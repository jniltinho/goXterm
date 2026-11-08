package gui

import "strings"

// tail is the plain-text tail of a terminal tab, so agent read/wait work without the SPA (Rust
// engine/src/tail.rs). Escape sequences are stripped by a small state machine (CSI, OSC/DCS strings,
// two-byte escapes); \r followed by anything but \n restarts the line, backspace erases one byte.
// Screen-addressed output (full-screen apps) reads as its printable bytes, not as a screen.
type tail struct {
	lines []string // ring of completed lines, oldest first
	cur   []byte
	state tailState
	cr    bool
	total uint64 // completed lines ever
	mark  uint64 // line index where the last agent send started (wait searches from here)
}

type tailState uint8

const (
	tsGround tailState = iota
	tsEsc
	tsEscInter
	tsCsi
	tsStr
	tsStrEsc
)

const (
	tailLines   = 2000 // lines kept per tab
	tailLineMax = 4096 // bytes kept per line
)

func (t *tail) feed(b []byte) {
	for _, c := range b {
		t.byte(c)
	}
}

func (t *tail) byte(b byte) {
	switch t.state {
	case tsGround:
		if t.cr && b != '\n' {
			t.cur = t.cur[:0]
		}
		t.cr = false
		switch {
		case b == 0x1b:
			t.state = tsEsc
		case b == '\n':
			t.newline()
		case b == '\r':
			t.cr = true
		case b == 0x08:
			if len(t.cur) > 0 {
				t.cur = t.cur[:len(t.cur)-1]
			}
		case b == '\t' || (b >= 0x20 && b != 0x7f):
			if len(t.cur) < tailLineMax {
				t.cur = append(t.cur, b)
			}
		}
	case tsEsc:
		switch {
		case b == '[':
			t.state = tsCsi
		case b == ']' || b == 'P' || b == 'X' || b == '^' || b == '_':
			t.state = tsStr
		case b >= 0x20 && b <= 0x2f:
			t.state = tsEscInter
		default:
			t.state = tsGround
		}
	case tsEscInter:
		if b >= 0x30 && b <= 0x7e {
			t.state = tsGround
		}
	case tsCsi:
		if b >= 0x40 && b <= 0x7e {
			t.state = tsGround
		}
	case tsStr:
		if b == 0x07 {
			t.state = tsGround
		} else if b == 0x1b {
			t.state = tsStrEsc
		}
	case tsStrEsc:
		if b == '\\' {
			t.state = tsGround
		} else {
			t.state = tsStr
		}
	}
}

func (t *tail) newline() {
	t.lines = append(t.lines, string(t.cur))
	t.cur = t.cur[:0]
	t.total++
	if len(t.lines) > tailLines {
		t.lines = t.lines[1:]
	}
}

// setMark remembers where new output starts (called before an agent sends input).
func (t *tail) setMark() { t.mark = t.total }

// last is the last n lines, including the unfinished current line.
func (t *tail) last(n int) string {
	partial := 0
	if len(t.cur) > 0 {
		partial = 1
	}
	return t.text(max(len(t.lines)+partial-n, 0))
}

// sinceMark is the text from the line where the last agent send started.
func (t *tail) sinceMark() string {
	first := t.total - uint64(len(t.lines))
	if t.mark < first {
		return t.text(0)
	}
	return t.text(int(t.mark - first))
}

func (t *tail) text(skip int) string {
	var parts []string
	if skip < len(t.lines) {
		parts = append(parts, t.lines[skip:]...)
	}
	if len(t.cur) > 0 && skip <= len(t.lines) {
		parts = append(parts, string(t.cur))
	}
	return strings.Join(parts, "\n")
}

package gui

import (
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"testing"
)

// TestTermAttachBuffersUntilAttached: output before attach is buffered (not emitted) and returned by
// TermAttach with the banner; output after attach is emitted live.
func TestTermAttachBuffersUntilAttached(t *testing.T) {
	var mu sync.Mutex
	var emitted []string
	a := &App{version: "test", emit: func(name string, data any) {
		mu.Lock()
		emitted = append(emitted, name)
		mu.Unlock()
	}}
	tab, onData, _ := a.newStreamTab()
	onData([]byte("[rustxterm] offering key\r\n"))
	onData([]byte("Welcome"))
	a.setBanner(tab, a.sshBanner("root", "h.example", 22, 0, "SSH-2.0-OpenSSH_9.6", "ssh-ed25519", "SHA256:abcdefghijklmnopqrstuvwxyz0123456789", bannerFwd{}))
	if len(emitted) != 0 {
		t.Fatalf("nothing may be emitted before attach, got %v", emitted)
	}
	at := a.TermAttach(tab)
	early, _ := base64.StdEncoding.DecodeString(at.Early)
	if string(early) != "[rustxterm] offering key\r\nWelcome" {
		t.Fatalf("early = %q", early)
	}
	if at.Banner == nil || at.Banner.Lines[0].Target != "root@h.example" {
		t.Fatalf("banner = %+v", at.Banner)
	}
	last := at.Banner.Lines[len(at.Banner.Lines)-1].Text
	if !strings.Contains(last, "OpenSSH_9.6") || !strings.Contains(last, "ssh-ed25519 SHA256:abcdefghijklmnop…") {
		t.Fatalf("server line = %q", last)
	}
	onData([]byte("$ "))
	if len(emitted) != 1 || emitted[0] != "term:out:"+tab {
		t.Fatalf("after attach output must be live, got %v", emitted)
	}
	if again := a.TermAttach(tab); again.Banner != nil || again.Early != "" {
		t.Fatalf("attach is one-shot, got %+v", again)
	}
}

// TestTermAttachReportsEarlyClose: a stream that ends before the SPA attaches reports it in Attach.
func TestTermAttachReportsEarlyClose(t *testing.T) {
	a := &App{emit: func(string, any) { t.Fatal("must not emit before attach") }}
	tab, _, onClose := a.newStreamTab()
	onClose(errors.New("connection reset"))
	at := a.TermAttach(tab)
	if !at.Closed || at.CloseMsg != "connection reset" {
		t.Fatalf("attach = %+v", at)
	}
}

package control

import (
	"strings"
	"testing"
)

// Ported from the Rust platform-windows tests (refusal_reasons, filtered_peers, pipe_peers,
// server_elevation).
const testSID = "S-1-5-21-1-2-3-1001"

func TestPeerRefusal(t *testing.T) {
	me := Identity{SID: testSID, Integrity: 0x3000}
	with := func(f func(*Identity)) string { p := me; f(&p); return PeerRefusal(&p, &me) }
	contains := func(got, want string) {
		t.Helper()
		if !strings.Contains(got, want) {
			t.Errorf("refusal %q, want it to mention %q", got, want)
		}
	}
	if r := with(func(*Identity) {}); r != "" {
		t.Errorf("same identity refused: %q", r)
	}
	contains(PeerRefusal(nil, &me), "identify")
	contains(with(func(p *Identity) { p.SID = "S-1-5-18" }), "only the user")
	contains(with(func(p *Identity) { p.Restricted = true }), "AppContainer")
	meUsers := Identity{SID: testSID, Integrity: 0x3000, Enabled: []string{"S-1-5-32-545"}}
	sandbox := meUsers
	sandbox.DenyOnly = []string{"S-1-5-32-545"}
	contains(PeerRefusal(&sandbox, &meUsers), "filtered")
	contains(with(func(p *Identity) { p.Integrity = 0x1000 }), "low-integrity")
	if r := with(func(p *Identity) { p.Integrity = 0x2000 }); r != differentElevation {
		t.Errorf("non-elevated client of an elevated GUI: %q", r)
	}
}

func TestFilteredAndPipePeers(t *testing.T) {
	me := Identity{SID: testSID, Integrity: 0x2000, Enabled: []string{"S-1-5-32-545"}, DenyOnly: []string{administrators}}
	if r := PeerRefusal(&me, &me); r != "" {
		t.Errorf("same UAC filtering refused: %q", r)
	}
	sandbox := me
	sandbox.DenyOnly = []string{"S-1-5-32-545"}
	if PeerRefusal(&sandbox, &me) == "" {
		t.Error("a sandbox with Users deny-only must be refused")
	}
	elevated := Identity{SID: testSID, Integrity: 0x3000, Enabled: []string{"S-1-5-32-545", administrators}}
	if r := PeerRefusal(&elevated, &me); r != "" {
		t.Errorf("elevated peer refused: %q", r)
	}
	server := Identity{SID: testSID, Integrity: 0x2000}
	for _, c := range []struct {
		peer *Identity
		ok   bool
	}{
		{&server, true},
		{&Identity{SID: testSID, Integrity: 0x3000}, true},                    // elevated client of a normal server
		{&Identity{SID: testSID, Restricted: true, Integrity: 0x2000}, false}, // restricted / AppContainer
		{&Identity{SID: testSID, Integrity: 0x1000}, false},                   // low integrity
		{&Identity{SID: "S-1-5-18", Integrity: 0x4000}, false},                // another user
		{nil, false}, // unknown identity
	} {
		if got := PeerRefusal(c.peer, &server) == ""; got != c.ok {
			t.Errorf("peer %+v allowed=%v, want %v", c.peer, got, c.ok)
		}
	}
}

func TestServerRefusal(t *testing.T) {
	me := Identity{SID: testSID, Integrity: 0x2000}
	if r := ServerRefusal(&me, &me); r != "" {
		t.Errorf("same server refused: %q", r)
	}
	elevated := me
	elevated.Integrity = 0x3000
	if r := ServerRefusal(&elevated, &me); !strings.Contains(r, "elevation") {
		t.Errorf("elevated server: %q", r)
	}
	if r := ServerRefusal(&me, &elevated); !strings.Contains(r, "elevation") {
		t.Errorf("elevated client: %q", r)
	}
	other := me
	other.SID = "S-1-5-21-9"
	if r := ServerRefusal(&other, &me); !strings.Contains(r, "not served") {
		t.Errorf("other user's server: %q", r)
	}
	if ServerRefusal(nil, &me) == "" {
		t.Error("unknown server accepted")
	}
	filtered := Identity{SID: testSID, Integrity: 0x2000, DenyOnly: []string{administrators}}
	admin := Identity{SID: testSID, Integrity: 0x2000, Enabled: []string{administrators}}
	if ServerRefusal(&filtered, &admin) != differentElevation || ServerRefusal(&admin, &filtered) != differentElevation ||
		PeerRefusal(&filtered, &admin) != differentElevation {
		t.Error("UAC filtering without an integrity difference must report a different elevation")
	}
	if pipeName(testSID) != `\\.\pipe\rustxterm-S-1-5-21-1-2-3-1001-control` || pipeSDDL(testSID) != "D:P(A;;GA;;;S-1-5-21-1-2-3-1001)" {
		t.Error("pipe name / SDDL differ from the Rust app")
	}
}

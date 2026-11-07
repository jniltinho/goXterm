package control

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strings"
	"testing"
)

func TestFrameRoundTripAndCleanEOF(t *testing.T) {
	var buf bytes.Buffer
	req := Request{ID: 7, Client: "t", Entry: EntryCliLive, AgentOp: AgentOp{Op: "tabs.list"}}
	if err := WriteMessage(&buf, req); err != nil {
		t.Fatal(err)
	}
	var got Request
	ok, err := ReadMessage(&buf, &got)
	if err != nil || !ok {
		t.Fatalf("read: ok=%v err=%v", ok, err)
	}
	if got.ID != 7 || got.Op != "tabs.list" || got.Entry != EntryCliLive {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
	// A clean end of stream (empty) is (false, nil), not an error.
	if ok, err := ReadMessage(&buf, &got); ok || err != nil {
		t.Fatalf("clean EOF: ok=%v err=%v", ok, err)
	}
}

func TestFrameLimit(t *testing.T) {
	if err := WriteFrame(bytes.NewBuffer(nil), make([]byte, MAX_FRAME)); err != nil {
		t.Errorf("MAX_FRAME must be accepted: %v", err)
	}
	if err := WriteFrame(bytes.NewBuffer(nil), make([]byte, MAX_FRAME+1)); err == nil {
		t.Error("MAX_FRAME+1 must be rejected")
	}
	// An oversized length prefix is rejected before the body is allocated.
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], MAX_FRAME+1)
	if _, err := ReadFrame(bytes.NewReader(lenBuf[:])); err == nil {
		t.Error("oversized length prefix must be rejected")
	}
}

func TestTruncatedFrameIsError(t *testing.T) {
	var b bytes.Buffer
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], 10)
	b.Write(lenBuf[:])
	b.WriteString("abc") // short
	if _, err := ReadFrame(&b); err == nil {
		t.Error("a truncated frame must be an error")
	}
}

// TestWireCompatWithRust decodes the exact JSON the Rust agent sends (from its own tests) and checks
// the flattened op fields — proving the Go types are wire-compatible with the existing client.
func TestWireCompatWithRust(t *testing.T) {
	const raw = `{"id":1,"client":"t","entry":"mcp-live","op":"send","tab":3,"data":"uptime\n"}`
	var r Request
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		t.Fatalf("decode Rust request: %v", err)
	}
	if r.ID != 1 || r.Client != "t" || r.Entry != EntryMcpLive {
		t.Fatalf("header mismatch: %+v", r)
	}
	if r.Op != "send" || r.Tab == nil || *r.Tab != 3 || r.Data != "uptime\n" {
		t.Fatalf("op mismatch: op=%q tab=%v data=%q", r.Op, r.Tab, r.Data)
	}
	// Re-encoding keeps op flattened at the top level (no nested "agentop"/"op" object).
	out, _ := json.Marshal(r)
	s := string(out)
	for _, want := range []string{`"op":"send"`, `"tab":3`, `"data":"uptime\n"`, `"entry":"mcp-live"`} {
		if !strings.Contains(s, want) {
			t.Errorf("re-encoded request missing %s: %s", want, s)
		}
	}
}

func TestResponseJSONShape(t *testing.T) {
	ok, _ := json.Marshal(OK(5, map[string]int{"sent": 3}))
	if s := string(ok); !strings.Contains(s, `"status":"ok"`) || !strings.Contains(s, `"sent":3`) || !strings.Contains(s, `"id":5`) {
		t.Errorf("ok response: %s", s)
	}
	er, _ := json.Marshal(Err(5, CodeDenied, "denied by policy"))
	if s := string(er); !strings.Contains(s, `"status":"err"`) || !strings.Contains(s, `"code":"denied"`) || !strings.Contains(s, `"message":"denied by policy"`) {
		t.Errorf("err response: %s", s)
	}
}

func TestSessionRefUntagged(t *testing.T) {
	var byID SessionRef
	if err := json.Unmarshal([]byte(`2`), &byID); err != nil || byID.ID == nil || *byID.ID != 2 || byID.Name != nil {
		t.Fatalf("by id: %+v err=%v", byID, err)
	}
	var byName SessionRef
	if err := json.Unmarshal([]byte(`"web"`), &byName); err != nil || byName.Name == nil || *byName.Name != "web" || byName.ID != nil {
		t.Fatalf("by name: %+v err=%v", byName, err)
	}
	if b, _ := json.Marshal(byID); string(b) != "2" {
		t.Errorf("marshal id = %s, want 2", b)
	}
	if b, _ := json.Marshal(byName); string(b) != `"web"` {
		t.Errorf("marshal name = %s, want \"web\"", b)
	}
}

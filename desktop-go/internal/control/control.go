// Package control is the agent control-channel protocol and transport, wire-compatible with the Rust
// app (rxt_core::control + rxt_platform control socket): a length-prefixed JSON frame (4-byte
// big-endian length, at most MAX_FRAME bytes) over a same-user Unix socket, so the existing rustXterm
// CLI/MCP agent client drives this app unchanged (migrate-gui-to-go-wails task 3.3).
package control

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// MAX_FRAME is the largest accepted control-channel frame (matches rxt_core::control::MAX_FRAME).
const MAX_FRAME = 1 << 20

// Entry points (serde kebab-case).
const (
	EntryGui        = "gui"
	EntryCliLive    = "cli-live"
	EntryCliOffline = "cli-offline"
	EntryMcpLive    = "mcp-live"
	EntryMcpOffline = "mcp-offline"
)

// Error codes (serde snake_case); the CLI maps them to exit codes.
const (
	CodeUsage           = "usage"
	CodeLocked          = "locked"
	CodeDenied          = "denied"
	CodeNotRunning      = "not_running"
	CodeAgentControlOff = "agent_control_off"
	CodeFailed          = "failed"
)

// SessionRef is a session selector: a numeric id or an exact name (serde untagged).
type SessionRef struct {
	ID   *int64
	Name *string
}

func (r *SessionRef) UnmarshalJSON(b []byte) error {
	var id int64
	if err := json.Unmarshal(b, &id); err == nil {
		r.ID, r.Name = &id, nil
		return nil
	}
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return fmt.Errorf("session selector must be an id or a name")
	}
	r.ID, r.Name = nil, &name
	return nil
}

func (r SessionRef) MarshalJSON() ([]byte, error) {
	if r.ID != nil {
		return json.Marshal(*r.ID)
	}
	if r.Name != nil {
		return json.Marshal(*r.Name)
	}
	return []byte("null"), nil
}

// SessionPatch is the editable non-secret session fields for offline edits (serde deny_unknown_fields).
type SessionPatch struct {
	Name   *string `json:"name,omitempty"`
	Host   *string `json:"host,omitempty"`
	Port   *uint16 `json:"port,omitempty"`
	User   *string `json:"user,omitempty"`
	Folder *string `json:"folder,omitempty"`
}

// AgentOp is one operation, flattened into Request (the discriminant is "op", serde snake_case). Only
// the fields of the named op are set; the rest stay zero/omitted.
type AgentOp struct {
	Op        string        `json:"op"`
	Session   *SessionRef   `json:"session,omitempty"`
	Tab       *uint64       `json:"tab,omitempty"`
	Data      string        `json:"data,omitempty"`
	Name      string        `json:"name,omitempty"`
	URL       string        `json:"url,omitempty"`
	Patch     *SessionPatch `json:"patch,omitempty"`
	Path      string        `json:"path,omitempty"`
	Pattern   string        `json:"pattern,omitempty"`
	TimeoutMs *uint64       `json:"timeout_ms,omitempty"`
	Lines     *uint32       `json:"lines,omitempty"`
	Remote    string        `json:"remote,omitempty"`
	Local     string        `json:"local,omitempty"`
	Ms        *uint64       `json:"ms,omitempty"`
	Dtr       *bool         `json:"dtr,omitempty"`
	Rts       *bool         `json:"rts,omitempty"`
}

// Request is one control-channel request (id, declared client name, entry point, flattened op).
type Request struct {
	ID      uint64 `json:"id"`
	Client  string `json:"client"`
	Entry   string `json:"entry"`
	AgentOp        // flattened: op + its fields at the top level
}

// Response is a control-channel reply (serde tag "status"): ok carries result JSON, err carries a
// code and message.
type Response struct {
	Status  string          `json:"status"`
	ID      uint64          `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
}

// OK / Err build the two response kinds. result is marshaled now so callers pass a plain value.
func OK(id uint64, result any) Response {
	b, err := json.Marshal(result)
	if err != nil {
		return Err(id, CodeFailed, "control result: "+err.Error())
	}
	return Response{Status: "ok", ID: id, Result: b}
}

func Err(id uint64, code, message string) Response {
	return Response{Status: "err", ID: id, Code: code, Message: message}
}

// ReadFrame reads the next frame; (nil, nil) on a clean end of stream before a length prefix.
func ReadFrame(r io.Reader) ([]byte, error) {
	var lenBuf [4]byte
	n, err := io.ReadFull(r, lenBuf[:])
	if err == io.EOF && n == 0 {
		return nil, nil // clean end of stream
	}
	if err != nil {
		return nil, err
	}
	length := int(binary.BigEndian.Uint32(lenBuf[:]))
	if length > MAX_FRAME {
		return nil, fmt.Errorf("control frame of %d bytes exceeds %d", length, MAX_FRAME)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// WriteFrame writes one length-prefixed frame.
func WriteFrame(w io.Writer, data []byte) error {
	if len(data) > MAX_FRAME {
		return fmt.Errorf("control frame of %d bytes exceeds %d", len(data), MAX_FRAME)
	}
	var lenBuf [4]byte
	binary.BigEndian.PutUint32(lenBuf[:], uint32(len(data)))
	if _, err := w.Write(lenBuf[:]); err != nil {
		return err
	}
	_, err := w.Write(data)
	return err
}

// ReadMessage reads and decodes one JSON message into v; (false, nil) at a clean end of stream.
func ReadMessage(r io.Reader, v any) (bool, error) {
	frame, err := ReadFrame(r)
	if err != nil || frame == nil {
		return false, err
	}
	if err := json.Unmarshal(frame, v); err != nil {
		return false, fmt.Errorf("control message: %w", err)
	}
	return true, nil
}

// WriteMessage encodes and writes one JSON message.
func WriteMessage(w io.Writer, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("control message: %w", err)
	}
	return WriteFrame(w, data)
}

// Call connects to the running instance, sends one request and returns its response (one request per
// connection, as the server expects). A refused client still gets its denied reason even if the
// server closed before our write finished, so the answer is read before a write error is reported.
// IsNotRunning(err) is true when there is no instance to reach.
func Call(req Request) (Response, error) {
	dir, err := RuntimeDir()
	if err != nil {
		return Response{}, err
	}
	c, err := Connect(dir)
	if err != nil {
		return Response{}, err
	}
	defer c.Close()
	writeErr := WriteMessage(c, req)
	var resp Response
	ok, readErr := ReadMessage(c, &resp)
	if ok && readErr == nil {
		return resp, nil
	}
	if writeErr != nil {
		return Response{}, writeErr
	}
	if readErr != nil {
		return Response{}, readErr
	}
	return Response{}, fmt.Errorf("control channel closed without a response")
}

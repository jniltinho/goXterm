package cmd

import (
	"encoding/json"
	"io"
	"os"
	"sync/atomic"

	"github.com/spf13/cobra"

	"rustxterm-go/internal/control"
)

// The stdio MCP server (migrate-gui-to-go-wails task 3.2): JSON-RPC 2.0 over stdin/stdout, headless
// (it never starts the GUI). It exposes only the permitted agent tools — there are no session
// add/edit/remove/import tools, so store writes are not reachable from MCP; a call to a forbidden op
// is rejected, and the running instance's dispatcher audits it. Live tools go to the running instance
// over the control socket (entry mcp-live); with no instance, only the read-only store tools work.
func init() { rootCmd.AddCommand(mcpCmd) }

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the stdio MCP server",
	Args:  noArgs,
	RunE:  func(*cobra.Command, []string) error { return runMCP(os.Stdin, os.Stdout) },
}

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResp struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcErr         `json:"error,omitempty"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

var opSeq atomic.Uint64

// runMCP reads newline/whitespace-delimited JSON-RPC messages and answers each (notifications, with no
// id, get no reply).
func runMCP(in io.Reader, out io.Writer) error {
	dec := json.NewDecoder(in)
	enc := json.NewEncoder(out)
	for {
		var req rpcReq
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		resp, respond := handleRPC(req)
		if respond {
			if err := enc.Encode(resp); err != nil {
				return err
			}
		}
	}
}

func handleRPC(req rpcReq) (rpcResp, bool) {
	if len(req.ID) == 0 {
		return rpcResp{}, false // a notification (e.g. notifications/initialized)
	}
	r := rpcResp{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		r.Result = map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "rustxterm", "version": version},
		}
	case "ping":
		r.Result = map[string]any{}
	case "tools/list":
		r.Result = map[string]any{"tools": listTools()}
	case "tools/call":
		r.Result, r.Error = callTool(req.Params)
	default:
		r.Error = &rpcErr{Code: -32601, Message: "method not found: " + req.Method}
	}
	return r, true
}

// listTools returns the tools available now: the live tools only when an instance is reachable.
func listTools() []map[string]any {
	live := control.Live()
	var out []map[string]any
	for i := range mcpTools {
		t := &mcpTools[i]
		if t.liveOnly && !live {
			continue
		}
		out = append(out, map[string]any{"name": t.name, "description": t.desc, "inputSchema": t.jsonSchema()})
	}
	return out
}

// callTool validates and runs one tool. Unknown tools and bad parameters are JSON-RPC errors; an
// operation failure (denied/not-running/…) is a tool result with isError set.
func callTool(raw json.RawMessage) (any, *rpcErr) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, &rpcErr{Code: -32602, Message: "invalid tools/call params: " + err.Error()}
	}
	d := toolByName(p.Name)
	if d == nil {
		return nil, &rpcErr{Code: -32602, Message: "unknown tool: " + p.Name}
	}
	op, err := d.buildOp(p.Arguments)
	if err != nil {
		return nil, &rpcErr{Code: -32602, Message: err.Error()}
	}
	result, errMsg := runToolOp(op)
	if errMsg != "" {
		return toolResult(errMsg, true), nil
	}
	if d.name == "screenshot_tab" { // a desktop capture is an image block, as in the Rust agent
		var shot struct {
			PNG string `json:"png"`
		}
		if rm, ok := result.(json.RawMessage); ok && json.Unmarshal(rm, &shot) == nil && shot.PNG != "" {
			return map[string]any{"content": []any{map[string]any{"type": "image", "data": shot.PNG, "mimeType": "image/png"}}, "isError": false}, nil
		}
	}
	return toolResult(asText(result), false), nil
}

// runToolOp runs an op against the running instance (entry mcp-live); if none is reachable, a Read op
// falls back to the store (entry mcp-offline), and anything else reports not-running.
func runToolOp(op control.AgentOp) (any, string) {
	req := control.Request{ID: opSeq.Add(1), Client: "mcp", Entry: control.EntryMcpLive, AgentOp: op}
	resp, err := control.Call(req)
	if err == nil {
		if resp.Status == "ok" {
			return resp.Result, ""
		}
		if resp.Code != control.CodeNotRunning {
			return nil, resp.Message // denied / failed / locked
		}
		// not running → fall through to the offline store
	} else if !control.IsNotRunning(err) {
		return nil, err.Error()
	}
	if !control.Allowed(control.Group(op.Op), control.EntryMcpOffline) {
		return nil, "rustXterm is not running, or agent control is off"
	}
	v, oerr := offlineRead(op)
	if oerr != nil {
		return nil, oerr.Error()
	}
	return v, ""
}

func toolResult(text string, isErr bool) map[string]any {
	return map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}, "isError": isErr}
}

// asText renders a result as text: already-encoded JSON (from the control channel) passes through,
// any other value is JSON-encoded.
func asText(v any) string {
	if rm, ok := v.(json.RawMessage); ok {
		return string(rm)
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(b)
}

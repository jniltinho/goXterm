package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// codeNames are the wire error codes (rxt_core::control::ErrorCode) by exit code; --json errors carry
// the name, as the Rust CLI prints them.
var codeNames = map[int]string{1: "failed", 2: "usage", 3: "locked", 4: "denied", 5: "not_running", 6: "agent_control_off"}

// exitCodeOf maps a wire error code to the CLI exit code (Rust exit_code); unknown codes are failures.
func exitCodeOf(code string) int {
	for n, name := range codeNames {
		if name == code {
			return n
		}
	}
	return 1
}

// encodeJSON is JSON without HTML escaping (as serde_json), compact or indented by two spaces.
func encodeJSON(v any, pretty bool) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if pretty {
		enc.SetIndent("", "  ")
	}
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// generic turns any result into plain JSON values (maps, slices, strings, json.Number), so results
// from the store and from the control channel print the same way.
func generic(v any) any {
	d := json.NewDecoder(strings.NewReader(encodeJSON(v, false)))
	d.UseNumber()
	var out any
	_ = d.Decode(&out)
	return out
}

// printResult prints a command result like the Rust CLI (crates/agent cli.rs print_result): --json
// prints compact JSON; otherwise a string as text, an array one line per item, an object with a "text"
// string as that text, anything else as indented JSON.
func printResult(w io.Writer, v any, asJSON bool) {
	g := generic(v)
	if asJSON {
		fmt.Fprintln(w, encodeJSON(g, false))
		return
	}
	switch x := g.(type) {
	case string:
		fmt.Fprint(w, x)
		if !strings.HasSuffix(x, "\n") {
			fmt.Fprintln(w)
		}
	case []any:
		for _, item := range x {
			fmt.Fprintln(w, line(item))
		}
	case map[string]any:
		if t, ok := x["text"].(string); ok {
			fmt.Fprintln(w, t)
			return
		}
		fmt.Fprintln(w, encodeJSON(g, true))
	default:
		fmt.Fprintln(w, encodeJSON(g, true))
	}
}

// line is the one-line form of a list item: sessions as `id\tname\tscheme://user@host:port`, anything
// else as compact JSON (Rust cli.rs line).
func line(v any) string {
	m, ok := v.(map[string]any)
	if _, isSession := m["host"]; !ok || !isSession {
		return encodeJSON(v, false)
	}
	s := func(k string) string { x, _ := m[k].(string); return x }
	user := ""
	if u, ok := m["user"].(string); ok {
		user = u + "@"
	}
	return fmt.Sprintf("%s\t%s\t%s://%s%s:%s",
		encodeJSON(m["id"], false), s("name"), s("protocol"), user, s("host"), encodeJSON(m["port"], false))
}

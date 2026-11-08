package cmd

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"rustxterm-go/internal/control"
)

// Live commands (Rust crates/agent cli.rs): one request to the running instance over the control
// socket (entry cli-live); they never start the GUI.

func init() {
	sendCmd.Flags().Bool("enter", false, "append a newline")
	readTabCmd.Flags().Uint32("lines", 0, "last N lines")
	waitCmd.Flags().String("timeout", "10s", "e.g. 10s, 500ms, 2m (plain numbers are seconds)")
	screenshotCmd.Flags().String("out", "", "write the PNG to this file")
	serialBreakCmd.Flags().Uint64("ms", 0, "break length in milliseconds (default 250)")
	serialLinesCmd.Flags().String("dtr", "", "on or off")
	serialLinesCmd.Flags().String("rts", "", "on or off")

	rootCmd.AddCommand(
		liveCmd("open <session>", "Open a saved session in the running instance", exactArgs(1),
			func(_ *cobra.Command, a []string) (control.AgentOp, error) {
				return control.AgentOp{Op: "open", Session: parseRef(a[0])}, nil
			}),
		group("tabs", "Open tabs of the running instance",
			liveCmd("list", "List open tabs", noArgs, fixed("list_tabs"))),
		sendCmd, readTabCmd, waitCmd, screenshotCmd,
		group("files", "File operations on a tab (local paths are inside the agent workspace)",
			liveCmd("ls <tab> [path]", "List a remote directory (default .)", rangeArgs(1, 2),
				func(_ *cobra.Command, a []string) (control.AgentOp, error) {
					path := "."
					if len(a) == 2 {
						path = a[1]
					}
					return tabOp("files_ls", a[0], func(op *control.AgentOp) { op.Path = path })
				}),
			liveCmd("get <tab> <remote> <local>", "Download a file into the agent workspace", exactArgs(3),
				func(_ *cobra.Command, a []string) (control.AgentOp, error) {
					return tabOp("files_get", a[0], func(op *control.AgentOp) { op.Remote, op.Local = a[1], a[2] })
				}),
			liveCmd("put <tab> <local> <remote>", "Upload an agent-workspace file", exactArgs(3),
				func(_ *cobra.Command, a []string) (control.AgentOp, error) {
					return tabOp("files_put", a[0], func(op *control.AgentOp) { op.Local, op.Remote = a[1], a[2] })
				})),
		group("tunnel", "Start or stop a saved tunnel in the running instance",
			liveCmd("start <name>", "Start a saved tunnel", exactArgs(1), named("tunnel_start")),
			liveCmd("stop <name>", "Stop a saved tunnel", exactArgs(1), named("tunnel_stop"))),
		liveCmd("close <tab>", "Close a tab", exactArgs(1),
			func(_ *cobra.Command, a []string) (control.AgentOp, error) { return tabOp("close", a[0], nil) }),
		group("serial", "Serial ports and line control in the running instance",
			liveCmd("ports", "List local serial ports", noArgs, fixed("serial_list_ports")),
			serialBreakCmd, serialLinesCmd),
	)
}

// liveCmd sends the op build returns and prints the result.
func liveCmd(use, short string, args cobra.PositionalArgs, build func(*cobra.Command, []string) (control.AgentOp, error)) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: args, RunE: func(c *cobra.Command, a []string) error {
		op, err := build(c, a)
		if err != nil {
			return err
		}
		v, err := liveCall(op)
		if err != nil {
			return err
		}
		printResult(c.OutOrStdout(), v, flags.json)
		return nil
	}}
}

func fixed(op string) func(*cobra.Command, []string) (control.AgentOp, error) {
	return func(*cobra.Command, []string) (control.AgentOp, error) { return control.AgentOp{Op: op}, nil }
}

func named(op string) func(*cobra.Command, []string) (control.AgentOp, error) {
	return func(_ *cobra.Command, a []string) (control.AgentOp, error) {
		return control.AgentOp{Op: op, Name: a[0]}, nil
	}
}

// tabOp builds an op on a tab id argument (a usage error if it is not a number), then lets set fill it.
func tabOp(op, tab string, set func(*control.AgentOp)) (control.AgentOp, error) {
	id, err := strconv.ParseUint(tab, 10, 64)
	if err != nil {
		return control.AgentOp{}, usage("invalid tab id %q", tab)
	}
	o := control.AgentOp{Op: op, Tab: &id}
	if set != nil {
		set(&o)
	}
	return o, nil
}

// parseRef: a number selects by id, anything else by exact name (Rust parse_ref).
func parseRef(s string) *control.SessionRef {
	if id, err := strconv.ParseUint(s, 10, 64); err == nil {
		n := int64(id)
		return &control.SessionRef{ID: &n}
	}
	return &control.SessionRef{Name: &s}
}

// liveCall sends one request and maps the outcome to the CLI exit codes (Rust client::call).
func liveCall(op control.AgentOp) (json.RawMessage, error) {
	resp, err := control.Call(control.Request{ID: 1, Client: cliClient, Entry: control.EntryCliLive, AgentOp: op})
	if err != nil {
		if control.IsNotRunning(err) || !control.Live() {
			return nil, exitErr{5, "rustXterm is not running, or agent control is off"}
		}
		return nil, fail("control channel: %v", err)
	}
	if resp.Status != "ok" {
		return nil, exitErr{exitCodeOf(resp.Code), resp.Message}
	}
	return resp.Result, nil
}

var sendCmd = liveCmd("send <tab> <text>", "Send text to a tab", exactArgs(2),
	func(c *cobra.Command, a []string) (control.AgentOp, error) {
		text := a[1]
		if enter, _ := c.Flags().GetBool("enter"); enter {
			text += "\n"
		}
		return tabOp("send", a[0], func(op *control.AgentOp) { op.Data = text })
	})

var readTabCmd = liveCmd("read <tab>", "Read a tab's terminal text", exactArgs(1),
	func(c *cobra.Command, a []string) (control.AgentOp, error) {
		return tabOp("read", a[0], func(op *control.AgentOp) {
			if c.Flags().Changed("lines") {
				n, _ := c.Flags().GetUint32("lines")
				op.Lines = &n
			}
		})
	})

var waitCmd = liveCmd("wait <tab> <pattern>", "Wait until a tab's output matches a pattern", exactArgs(2),
	func(c *cobra.Command, a []string) (control.AgentOp, error) {
		s, _ := c.Flags().GetString("timeout")
		ms, err := parseDurationMs(s)
		if err != nil {
			return control.AgentOp{}, err
		}
		return tabOp("wait", a[0], func(op *control.AgentOp) { op.Pattern, op.TimeoutMs = a[1], &ms })
	})

var screenshotCmd = &cobra.Command{
	Use:   "screenshot <tab>",
	Short: "Capture a tab as PNG",
	Args:  exactArgs(1),
	RunE: func(c *cobra.Command, a []string) error {
		op, err := tabOp("screenshot", a[0], nil)
		if err != nil {
			return err
		}
		v, err := liveCall(op)
		if err != nil {
			return err
		}
		out, _ := c.Flags().GetString("out")
		if out == "" {
			printResult(c.OutOrStdout(), v, flags.json)
			return nil
		}
		var shot struct {
			PNG string `json:"png"`
		}
		_ = json.Unmarshal(v, &shot)
		png, err := base64.StdEncoding.DecodeString(shot.PNG)
		if err != nil {
			return fail("invalid screenshot data: %v", err)
		}
		if err := os.WriteFile(out, png, 0o666); err != nil {
			return fail("io error: %v", err)
		}
		printResult(c.OutOrStdout(), map[string]any{"path": out}, flags.json)
		return nil
	},
}

var serialBreakCmd = liveCmd("break <tab>", "Send a break on a serial tab", exactArgs(1),
	func(c *cobra.Command, a []string) (control.AgentOp, error) {
		return tabOp("serial_send_break", a[0], func(op *control.AgentOp) {
			if c.Flags().Changed("ms") {
				ms, _ := c.Flags().GetUint64("ms")
				op.Ms = &ms
			}
		})
	})

var serialLinesCmd = liveCmd("lines <tab>", "Set DTR and/or RTS on a serial tab", exactArgs(1),
	func(c *cobra.Command, a []string) (control.AgentOp, error) {
		var lines [2]*bool
		for i, name := range []string{"dtr", "rts"} {
			if !c.Flags().Changed(name) {
				continue
			}
			s, _ := c.Flags().GetString(name)
			b, err := parseOnOff(s)
			if err != nil {
				return control.AgentOp{}, err
			}
			lines[i] = &b
		}
		return tabOp("serial_set_lines", a[0], func(op *control.AgentOp) { op.Dtr, op.Rts = lines[0], lines[1] })
	})

func parseOnOff(s string) (bool, error) {
	switch s {
	case "on", "1", "true":
		return true, nil
	case "off", "0", "false":
		return false, nil
	}
	return false, usage("expected on or off, got `%s`", s)
}

// parseDurationMs reads 10s, 500ms, 2m or a plain number of seconds, in milliseconds (Rust
// parse_duration_ms); anything else is a usage error.
func parseDurationMs(s string) (uint64, error) {
	num, scale := s, uint64(1000)
	switch {
	case strings.HasSuffix(s, "ms"):
		num, scale = strings.TrimSuffix(s, "ms"), 1
	case strings.HasSuffix(s, "s"):
		num = strings.TrimSuffix(s, "s")
	case strings.HasSuffix(s, "m"):
		num, scale = strings.TrimSuffix(s, "m"), 60_000
	}
	n, err := strconv.ParseUint(strings.TrimSpace(num), 10, 64)
	if err != nil {
		return 0, usage("invalid duration '%s'", s)
	}
	if n > ^uint64(0)/scale { // saturate like Rust saturating_mul
		return ^uint64(0), nil
	}
	return n * scale, nil
}

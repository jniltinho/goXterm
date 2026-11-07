// Package cmd is the command line of the single rustxterm-go binary: with no subcommand it opens the desktop app;
// the subcommands are the CLI and the MCP server (mcp). Protocols, the store and MCP are filled in by the later
// phases of migrate-gui-to-go-wails; this is the scaffold.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"rustxterm-go/internal/gui"
)

// version is set by the build (-ldflags "-X rustxterm-go/cmd.version=…").
var version = "dev"

var flags struct {
	json    bool
	safeGPU bool
}

var rootCmd = &cobra.Command{
	Use:           "rustxterm-go",
	Short:         "rustXterm: desktop app, command line and MCP server",
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		return gui.Run(version, flags.safeGPU)
	},
}

// exitErr carries a Rust-compatible exit code (lib.rs exit_code): 1 Failed, 2 Usage, 3 Locked,
// 4 Denied, 5 NotRunning, 6 AgentControlOff.
type exitErr struct {
	code int
	msg  string
}

func (e exitErr) Error() string { return e.msg }

// fail / usage / locked build the common coded errors.
func fail(format string, a ...any) error   { return exitErr{1, fmt.Sprintf(format, a...)} }
func usage(format string, a ...any) error  { return exitErr{2, fmt.Sprintf(format, a...)} }
func locked(format string, a ...any) error { return exitErr{3, fmt.Sprintf(format, a...)} }

// Execute runs the command line, mapping errors to the Rust CLI's exit codes. A --json run prints the
// error as {"error":{"code","message"}} like the Rust agent.
func Execute() {
	err := rootCmd.ExecuteContext(context.Background())
	if err == nil {
		return
	}
	code := 1
	if e, ok := err.(exitErr); ok {
		code = e.code
	}
	if flags.json {
		b, _ := json.Marshal(map[string]any{"error": map[string]any{"code": code, "message": err.Error()}})
		fmt.Fprintln(os.Stdout, string(b))
	} else {
		fmt.Fprintln(os.Stderr, "rustxterm:", err.Error())
	}
	os.Exit(code)
}

func init() {
	p := rootCmd.PersistentFlags()
	p.BoolVar(&flags.json, "json", false, "print JSON instead of tables")
	p.BoolVar(&flags.safeGPU, "safe-gpu", false, "start the webview with GPU acceleration off (Linux, Windows)")
}

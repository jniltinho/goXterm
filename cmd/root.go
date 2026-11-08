// Package cmd is the command line of the single rustxterm-go binary: with no subcommand it opens the desktop app;
// the subcommands are the CLI and the MCP server (mcp). Protocols, the store and MCP are filled in by the later
// phases of migrate-gui-to-go-wails; this is the scaffold.
package cmd

import (
	"context"
	"fmt"
	"io"
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
	Args:          noArgs, // an unknown subcommand is a usage error (2), as with clap
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
func denied(format string, a ...any) error { return exitErr{4, fmt.Sprintf(format, a...)} }

// Execute runs the command line and exits with the Rust CLI's exit code.
func Execute() {
	if code := report(rootCmd.ExecuteContext(context.Background()), os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

// report prints err like the Rust agent (cli.rs run) and returns its exit code: with --json
// {"error":{"code":"<wire code>","message":…}} on stdout, otherwise "rustxterm: <message>" on stderr.
func report(err error, stdout, stderr io.Writer) int {
	if err == nil {
		return 0
	}
	code := 1
	if e, ok := err.(exitErr); ok {
		code = e.code
	}
	if flags.json {
		fmt.Fprintln(stdout, encodeJSON(map[string]any{"error": map[string]any{"code": codeNames[code], "message": err.Error()}}, false))
	} else {
		fmt.Fprintln(stderr, "rustxterm:", err.Error())
	}
	return code
}

func init() {
	p := rootCmd.PersistentFlags()
	p.BoolVar(&flags.json, "json", false, "print JSON instead of tables")
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usage("%v", err) })
	p.BoolVar(&flags.safeGPU, "safe-gpu", false, "start the webview with GPU acceleration off (Linux, Windows)")
}

// Package cmd is the command line of the single rustxterm-go binary: with no subcommand it opens the desktop app;
// the subcommands are the CLI and the MCP server (mcp). Protocols, the store and MCP are filled in by the later
// phases of migrate-gui-to-go-wails; this is the scaffold.
package cmd

import (
	"context"
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

// Execute runs the command line.
func Execute() {
	if err := rootCmd.ExecuteContext(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func init() {
	p := rootCmd.PersistentFlags()
	p.BoolVar(&flags.json, "json", false, "print JSON instead of tables")
	p.BoolVar(&flags.safeGPU, "safe-gpu", false, "start the webview with GPU acceleration off (Linux, Windows)")
}

package cmd

import (
	"errors"

	"github.com/spf13/cobra"
)

// shortcut: skeleton only — the stdio MCP server is wired in phase 3 (task 3.2) with the Go MCP SDK,
// exposing the same permission matrix as the Rust agent-control capability.
func init() { rootCmd.AddCommand(mcpCmd) }

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run the stdio MCP server",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		return errors.New("not implemented yet (migrate-gui-to-go-wails task 3.2)")
	},
}

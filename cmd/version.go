package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() { rootCmd.AddCommand(versionCmd) }

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version and exit",
	Args:  cobra.NoArgs,
	RunE: func(*cobra.Command, []string) error {
		fmt.Println(version)
		return nil
	},
}

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	foldersCmd.AddCommand(foldersListCmd)
	rootCmd.AddCommand(foldersCmd)
}

var foldersCmd = &cobra.Command{
	Use:   "folders",
	Short: "Offline store reads (list)",
}

var foldersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List session-tree folders",
	Args:  noArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		list, err := st.Folders()
		if err != nil {
			return fail("%v", err)
		}
		if flags.json {
			return emitJSON(list)
		}
		for _, f := range list {
			fmt.Printf("%-4v %-24v parent=%v\n", f["id"], f["name"], str(f["parent"]))
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "%d folder(s)\n", len(list))
		return nil
	},
}

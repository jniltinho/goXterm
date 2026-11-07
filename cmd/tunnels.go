package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func init() {
	tunnelsCmd.AddCommand(tunnelsListCmd)
	rootCmd.AddCommand(tunnelsCmd)
}

var tunnelsCmd = &cobra.Command{
	Use:   "tunnels",
	Short: "Offline store reads (list)",
}

var tunnelsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved tunnels",
	Args:  noArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		list, err := st.Tunnels()
		if err != nil {
			return fail("%v", err)
		}
		if flags.json {
			return emitJSON(list)
		}
		for _, t := range list {
			fmt.Printf("%-20v %-8v :%v → %v:%v  via %v\n",
				t["name"], t["kind"], t["bind_port"], str(t["dest_host"]), t["dest_port"], t["via"])
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "%d tunnel(s)\n", len(list))
		return nil
	},
}

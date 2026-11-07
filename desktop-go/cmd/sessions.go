package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"rustxterm-go/internal/store"
)

func init() {
	sessionsCmd.AddCommand(sessionsListCmd)
	rootCmd.AddCommand(sessionsCmd)
}

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Offline store reads (list, show)",
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved sessions (credentials shown only as saved/absent)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		list, err := st.Sessions()
		if err != nil {
			return err
		}
		if flags.json {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			return enc.Encode(list)
		}
		for _, s := range list {
			fmt.Printf("%-4v %-24v %v://%v@%v:%v\n",
				s["id"], s["name"], s["protocol"], str(s["user"]), s["host"], s["port"])
		}
		fmt.Fprintf(os.Stderr, "%d session(s)\n", len(list))
		return nil
	},
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// openStore unlocks the store the way the Rust CLI does: through the keyring wrap if there is one,
// otherwise with the master password typed on a terminal (or RXT_MASTER_PASSWORD for automation).
func openStore() (*store.Store, error) {
	dir := store.DataDir()
	if st, ok, _ := store.OpenWithKeyring(dir, store.SystemKeyring()); ok {
		return st, nil
	}
	pw, err := masterPassword()
	if err != nil {
		return nil, err
	}
	return store.Open(dir, pw)
}

// masterPassword reads the master password. For an interactive terminal it prompts; for automation
// it honors RXT_MASTER_PASSWORD. It never echoes or logs the value.
// shortcut: keyring unlock is wired in task 2.2; until then reads use the master password.
func masterPassword() (string, error) {
	if v, ok := os.LookupEnv("RXT_MASTER_PASSWORD"); ok {
		return v, nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return "", errors.New("type the master password on a terminal, or set RXT_MASTER_PASSWORD")
	}
	fmt.Fprint(os.Stderr, "Master password: ")
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	return string(b), err
}

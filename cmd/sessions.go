package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"rustxterm-go/internal/store"
)

func init() {
	sessionsCmd.AddCommand(sessionsListCmd, sessionsShowCmd)
	rootCmd.AddCommand(sessionsCmd)
}

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Offline store reads (list, show)",
}

var sessionsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List saved sessions (credentials shown only as saved/absent)",
	Args:  noArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		list, err := st.Sessions()
		if err != nil {
			return fail("%v", err)
		}
		if flags.json {
			return emitJSON(list)
		}
		for _, s := range list {
			fmt.Printf("%-4v %-24v %v://%v@%v:%v\n",
				s["id"], s["name"], s["protocol"], str(s["user"]), s["host"], s["port"])
		}
		fmt.Fprintf(os.Stderr, "%d session(s)\n", len(list))
		return nil
	},
}

var sessionsShowCmd = &cobra.Command{
	Use:   "show <id|name>",
	Short: "Show one saved session (no credentials)",
	Args:  exactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		list, err := st.Sessions()
		if err != nil {
			return fail("%v", err)
		}
		s := resolveSession(list, args[0])
		if s == nil {
			return fail("no session matches %q", args[0])
		}
		if flags.json {
			return emitJSON(s)
		}
		for _, k := range []string{"id", "name", "protocol", "host", "port", "user", "folder", "password"} {
			fmt.Printf("%-10s %v\n", k, s[k])
		}
		return nil
	},
}

// resolveSession matches by numeric id, else by exact name (case-insensitive); returns nil if none.
func resolveSession(list []map[string]any, ref string) map[string]any {
	if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
		for _, s := range list {
			if asInt64(s["id"]) == id {
				return s
			}
		}
	}
	for _, s := range list {
		if strings.EqualFold(str(s["name"]), ref) {
			return s
		}
	}
	return nil
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// openStore unlocks the store the way the Rust CLI does: through the keyring wrap if there is one,
// otherwise with the master password typed on a terminal (or RXT_MASTER_PASSWORD for automation).
// Every unlock failure maps to the Locked exit code (3), as the Rust CLI reports a locked store.
func openStore() (*store.Store, error) {
	dir := store.DataDir()
	if st, ok, _ := store.OpenWithKeyring(dir, store.SystemKeyring()); ok {
		return st, nil
	}
	pw, err := masterPassword()
	if err != nil {
		return nil, locked("%v", err)
	}
	st, err := store.Open(dir, pw)
	if err != nil {
		return nil, locked("%v", err)
	}
	return st, nil
}

// emitJSON prints v as indented JSON to stdout.
func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// noArgs / exactArgs are cobra validators that fail with the Usage exit code (2), matching the Rust CLI.
func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usage("unexpected argument %q", args[0])
	}
	return nil
}

func exactArgs(n int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) != n {
			return usage("expected %d argument(s), got %d", n, len(args))
		}
		return nil
	}
}

// asInt64 coerces a JSON number (float64) or int to int64.
func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
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

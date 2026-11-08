package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"rustxterm-go/internal/mobaini"
	"rustxterm-go/internal/store"
)

// Offline commands (Rust crates/agent cli.rs + engine offline.rs): reads open the store through the
// keyring, else the master password; changes need the master password typed on an interactive terminal
// (never the keyring, a pipe or the environment) and are audited as cli-offline owner operations.

// cliClient is the client name the CLI declares (Rust CLI_CLIENT).
const cliClient = "rustxterm-cli"

func init() {
	sessionsAddCmd.Flags().String("name", "", "session name")
	sessionsAddCmd.Flags().String("folder", "", "folder path, /-separated")
	f := sessionsEditCmd.Flags()
	f.String("name", "", "new name")
	f.String("host", "", "new host")
	f.Uint16("port", 0, "new port")
	f.String("user", "", "new user (empty clears it)")
	f.String("folder", "", "new folder path, /-separated")
	exportCmd.Flags().String("out", "", "write to this file instead of stdout")

	rootCmd.AddCommand(
		group("sessions", "Saved sessions (offline store)",
			readCmd("list", "List saved sessions (credentials shown only as saved/absent)", noArgs,
				func(st *store.Store, _ []string) (any, error) { return nonNil(st.Sessions()) }),
			readCmd("show <session>", "Show one saved session (no credentials)", exactArgs(1), showSession),
			sessionsAddCmd, sessionsEditCmd, sessionsRemoveCmd),
		group("folders", "Session folders (offline store)",
			readCmd("list", "List session folders", noArgs,
				func(st *store.Store, _ []string) (any, error) { return nonNil(st.Folders()) })),
		group("tunnels", "Saved tunnels (offline store)",
			readCmd("list", "List saved tunnels", noArgs,
				func(st *store.Store, _ []string) (any, error) { return nonNil(st.Tunnels()) })),
		exportCmd,
		group("import", "Import sessions (preview, then confirm on the terminal)", importIniCmd),
	)
}

// group is a parent command; as with clap, running it without a known subcommand is a usage error.
func group(use, short string, subs ...*cobra.Command) *cobra.Command {
	c := &cobra.Command{Use: use, Short: short, RunE: func(c *cobra.Command, args []string) error {
		if len(args) > 0 {
			return usage("unrecognized subcommand %q", args[0])
		}
		return usage("%s requires a subcommand", c.CommandPath())
	}}
	c.AddCommand(subs...)
	return c
}

// readCmd is an offline read: open the store, run, print the result.
func readCmd(use, short string, args cobra.PositionalArgs, run func(*store.Store, []string) (any, error)) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: args, RunE: func(c *cobra.Command, a []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		v, err := run(st, a)
		if err != nil {
			return err
		}
		printResult(c.OutOrStdout(), v, flags.json)
		return nil
	}}
}

// change is the outcome of an offline store change, for the audit record and the output.
type change struct {
	session int64
	result  any
}

// changeCmd is an offline owner change: terminal-only unlock, run, audit, print.
func changeCmd(use, short, op string, args cobra.PositionalArgs, target func([]string) string,
	run func(*cobra.Command, *store.Store, []string) (change, error)) *cobra.Command {
	return &cobra.Command{Use: use, Short: short, Args: args, RunE: func(c *cobra.Command, a []string) error {
		st, err := terminalStore()
		if err != nil {
			return err
		}
		defer st.Close()
		ch, err := run(c, st, a)
		audit(c, st, op, ch.session, target(a), err)
		if err != nil {
			return err
		}
		printResult(c.OutOrStdout(), ch.result, flags.json)
		return nil
	}}
}

// audit records an allowed cli-offline operation and its outcome (Rust engine offline apply).
// shortcut: offline reads are not audited (each record re-encrypts the whole store); audit them if the
// store gets incremental writes.
func audit(c *cobra.Command, st *store.Store, op string, session int64, target string, opErr error) {
	result := "success"
	if opErr != nil {
		result = opErr.Error()
	}
	rec := store.AuditRecord{Client: cliClient, EntryPoint: "cli-offline", Command: op, Session: session,
		Target: target, Decision: "allowed", Result: result}
	if err := st.AppendAudit(rec); err != nil {
		fmt.Fprintln(c.ErrOrStderr(), "rustxterm: cannot write agent audit record:", err)
	}
}

func nonNil(l []map[string]any, err error) (any, error) {
	if l == nil {
		l = []map[string]any{}
	}
	return l, err
}

func showSession(st *store.Store, args []string) (any, error) {
	list, err := st.Sessions()
	if err != nil {
		return nil, err
	}
	s, err := findSession(list, args[0])
	if err != nil {
		return nil, err
	}
	p, err := st.Policy(asInt64(s["id"]))
	if err != nil {
		return nil, err
	}
	s["agent_policy"], s["content_read"] = p.Policy, p.ContentRead
	return s, nil
}

var sessionsAddCmd = changeCmd("add <url>", "Add a session from a URL such as ssh://user@host:22 (master password on a terminal)",
	"add_session", exactArgs(1), func(a []string) string { return redactURL(a[0]) },
	func(c *cobra.Command, st *store.Store, a []string) (change, error) {
		s, err := parseSessionURL(a[0])
		if err != nil {
			return change{}, err
		}
		if u, _ := s["user"].(string); strings.Contains(u, ":") {
			return change{}, usage("credentials are not accepted in session URLs")
		}
		return saveSession(c, st, s)
	})

var sessionsEditCmd = changeCmd("edit <session>", "Edit non-secret fields (master password on a terminal)",
	"edit_session", exactArgs(1), describeRef,
	func(c *cobra.Command, st *store.Store, a []string) (change, error) {
		s, err := storedSession(st, a[0])
		if err != nil {
			return change{}, err
		}
		return saveSession(c, st, s)
	})

var sessionsRemoveCmd = changeCmd("remove <session>", "Remove a session (master password on a terminal)",
	"remove_session", exactArgs(1), describeRef,
	func(_ *cobra.Command, st *store.Store, a []string) (change, error) {
		s, err := storedSession(st, a[0])
		if err != nil {
			return change{}, err
		}
		id := asInt64(s["id"])
		if err := st.DeleteSession(id); err != nil {
			return change{}, err
		}
		return change{id, map[string]any{"removed": id}}, nil
	})

// storedSession is a session's stored document (without the saved/absent password marker).
func storedSession(st *store.Store, ref string) (map[string]any, error) {
	list, err := st.Sessions()
	if err != nil {
		return nil, err
	}
	s, err := findSession(list, ref)
	if err == nil {
		delete(s, "password")
	}
	return s, err
}

// saveSession applies the edit flags present on c (Rust patch_session) and saves s.
func saveSession(c *cobra.Command, st *store.Store, s map[string]any) (change, error) {
	f := c.Flags()
	set := func(name string) (string, bool) {
		if fl := f.Lookup(name); fl != nil && fl.Changed {
			return fl.Value.String(), true
		}
		return "", false
	}
	if v, ok := set("name"); ok {
		s["name"] = v
	}
	if v, ok := set("host"); ok {
		if strings.TrimSpace(v) == "" || strings.ContainsFunc(v, func(r rune) bool { return unicode.IsSpace(r) || r == '/' }) {
			return change{}, usage("invalid host %q", v)
		}
		s["host"] = v
	}
	if _, ok := set("port"); ok {
		p, _ := f.GetUint16("port")
		s["port"] = p
	}
	if v, ok := set("user"); ok {
		if v == "" {
			delete(s, "user")
		} else {
			s["user"] = v
		}
	}
	if v, ok := set("folder"); ok {
		id, err := st.FolderByPath(v)
		if err != nil {
			return change{}, err
		}
		if id == nil {
			s["folder"] = nil
		} else {
			s["folder"] = *id
		}
	}
	name, _ := s["name"].(string)
	if strings.TrimSpace(name) == "" {
		return change{}, usage("session name is empty")
	}
	id, err := st.SaveSession(encodeJSON(s, false))
	if err != nil {
		return change{}, err
	}
	return change{id, map[string]any{"id": id, "name": name}}, nil
}

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export sessions, folders and tunnels as TOML (no credentials)",
	Args:  noArgs,
	RunE: func(c *cobra.Command, _ []string) error {
		st, err := openStore()
		if err != nil {
			return err
		}
		defer st.Close()
		out, _ := c.Flags().GetString("out")
		if flags.json && out == "" { // the export document as JSON, as the Rust CLI prints it
			doc, err := exportDoc(st)
			if err != nil {
				return err
			}
			printResult(c.OutOrStdout(), doc, true)
			return nil
		}
		text, err := st.ExportTOML()
		if err != nil {
			return err
		}
		if out == "" {
			printResult(c.OutOrStdout(), text, false)
			return nil
		}
		if err := os.WriteFile(out, []byte(text), 0o666); err != nil {
			return fail("io error: %v", err)
		}
		printResult(c.OutOrStdout(), map[string]any{"path": out}, flags.json)
		return nil
	},
}

// exportDoc is the export as the Rust Export struct serializes to JSON.
func exportDoc(st *store.Store) (map[string]any, error) {
	folders, err := nonNil(st.Folders())
	if err != nil {
		return nil, err
	}
	sessions, err := st.Sessions()
	if err != nil {
		return nil, err
	}
	for _, s := range sessions {
		delete(s, "password")
	}
	all, _ := nonNil(sessions, nil)
	tunnels, err := nonNil(st.Tunnels())
	if err != nil {
		return nil, err
	}
	return map[string]any{"format": "rustxterm-export", "version": 1, "folders": folders, "sessions": all, "tunnels": tunnels}, nil
}

var importIniCmd = &cobra.Command{
	Use:   "mobaxterm-ini <file>",
	Short: "Import bookmarks from a MobaXterm.ini file",
	Args:  exactArgs(1),
	RunE: func(c *cobra.Command, a []string) error {
		if !isTerminal() {
			return denied("import requires an interactive terminal")
		}
		file := a[0]
		report, err := mobaini.ImportFile(file)
		if err != nil {
			return fail("io error: %v", err)
		}
		w := c.ErrOrStderr()
		fmt.Fprintf(w, "%s: %d session(s) in %d folder(s)\n", file, len(report.Sessions), len(report.Folders))
		for _, it := range report.Sessions {
			s := it.Session
			fmt.Fprintf(w, "  %s/%v  %v://%v:%v\n", it.Folder, s["name"], s["protocol"], s["host"], s["port"])
		}
		for _, k := range report.Skipped {
			fmt.Fprintf(w, "  skipped line %d %q: %s\n", k.Line, k.Name, k.Reason)
		}
		fmt.Fprintf(w, "Import %d session(s)? [y/N] ", len(report.Sessions))
		var answer string
		_, _ = fmt.Fscanln(c.InOrStdin(), &answer)
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			return fail("import cancelled")
		}
		st, err := terminalStore()
		if err != nil {
			return err
		}
		defer st.Close()
		items := make([]store.ImportItem, 0, len(report.Sessions))
		for _, it := range report.Sessions {
			items = append(items, store.ImportItem{Folder: it.Folder, Session: it.Session})
		}
		n, err := st.ImportSessions(items)
		audit(c, st, "import_mobaxterm_ini", 0, file, err)
		if err != nil {
			return err
		}
		skipped := report.Skipped
		if skipped == nil {
			skipped = []mobaini.Entry{}
		}
		printResult(c.OutOrStdout(), map[string]any{"imported": n, "skipped": skipped}, flags.json)
		return nil
	},
}

// findSession selects by numeric id, else by exact name; an ambiguous or unknown name is a usage
// error (Rust engine find_session).
func findSession(list []map[string]any, ref string) (map[string]any, error) {
	if id, err := strconv.ParseUint(ref, 10, 64); err == nil {
		for _, s := range list {
			if asInt64(s["id"]) == int64(id) {
				return s, nil
			}
		}
		return nil, usage("not found: session %d", id)
	}
	var found []map[string]any
	for _, s := range list {
		if str(s["name"]) == ref {
			found = append(found, s)
		}
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return nil, usage("no session named %q", ref)
	}
	return nil, usage("several sessions are named %q; use the id", ref)
}

// resolveSession is findSession without the error (used by the MCP offline fallback).
func resolveSession(list []map[string]any, ref string) map[string]any {
	s, _ := findSession(list, ref)
	return s
}

// describeRef is the audit target of a session selector (Rust control describe).
func describeRef(a []string) string {
	if _, err := strconv.ParseUint(a[0], 10, 64); err == nil {
		return "session " + a[0]
	}
	return a[0]
}

// redactURL drops a ":password" from a URL's user info (Rust control redact_url).
func redactURL(url string) string {
	start := 0
	if i := strings.Index(url, "://"); i >= 0 {
		start = i + 3
	}
	at := strings.LastIndex(url[start:], "@")
	if at < 0 {
		return url
	}
	at += start
	if colon := strings.Index(url[start:at], ":"); colon >= 0 {
		return url[:start+colon] + url[at:]
	}
	return url
}

func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// openStore unlocks the store for a read the way the Rust CLI does: through the keyring wrap if there
// is one, otherwise with the master password typed on a terminal (or RXT_MASTER_PASSWORD for automation).
func openStore() (*store.Store, error) {
	dir := store.DataDir()
	if !store.Exists(dir) {
		return nil, fail("no database yet: create it in the rustXterm window first")
	}
	if st, ok, _ := store.OpenWithKeyring(dir, store.SystemKeyring()); ok {
		return st, nil
	}
	pw, ok := os.LookupEnv("RXT_MASTER_PASSWORD")
	if !ok {
		if !isTerminal() {
			return nil, locked("the database is locked: type the master password on a terminal, or enable the keyring")
		}
		var err error
		if pw, err = promptPassword(); err != nil {
			return nil, err
		}
	}
	return unlock(dir, pw)
}

// terminalStore unlocks the store for a change: only with the master password typed on a terminal.
func terminalStore() (*store.Store, error) {
	if !isTerminal() {
		return nil, denied("offline changes require the master password typed on an interactive terminal")
	}
	dir := store.DataDir()
	if !store.Exists(dir) {
		return nil, fail("no database yet: create it in the rustXterm window first")
	}
	pw, err := promptPassword()
	if err != nil {
		return nil, err
	}
	return unlock(dir, pw)
}

func unlock(dir, pw string) (*store.Store, error) {
	st, err := store.Open(dir, pw)
	if err != nil {
		return nil, locked("%v", err)
	}
	return st, nil
}

// isTerminal / promptPassword are variables so tests can stand in for an interactive terminal.
var (
	isTerminal     = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	promptPassword = readPassword
)

// readPassword reads the master password without echo; Ctrl+C restores the terminal before exiting.
func readPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	state, err := term.GetState(fd)
	if err != nil {
		return "", fail("cannot read the master password: %v", err)
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	defer signal.Stop(sig)
	go func() {
		if _, ok := <-sig; ok {
			_ = term.Restore(fd, state)
			fmt.Fprintln(os.Stderr, "\nrustxterm: master password prompt interrupted")
			os.Exit(1)
		}
	}()
	fmt.Fprint(os.Stderr, "Master password: ")
	b, err := term.ReadPassword(fd)
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return "", fail("cannot read the master password: %v", err)
	}
	return string(b), nil
}

// noArgs / exactArgs / rangeArgs are cobra validators that fail with the Usage exit code (2), as clap.
func noArgs(_ *cobra.Command, args []string) error {
	if len(args) > 0 {
		return usage("unexpected argument %q", args[0])
	}
	return nil
}

func exactArgs(n int) cobra.PositionalArgs { return rangeArgs(n, n) }

func rangeArgs(lo, hi int) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < lo || len(args) > hi {
			if lo == hi {
				return usage("expected %d argument(s), got %d", lo, len(args))
			}
			return usage("expected %d to %d argument(s), got %d", lo, hi, len(args))
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

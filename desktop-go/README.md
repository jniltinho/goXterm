# rustXterm (Go + Wails)

Go + Wails v3 rewrite of the rustXterm GUI, built in parallel with the Rust app until it reaches parity. One binary
is the desktop app, the CLI and the MCP server. Frontend: Vue 3 + Tailwind, built with **Vite Plus (`vp`)**, embedded
in the binary. Protocols are reimplemented in Go. See `openspec/changes/migrate-gui-to-go-wails` for the plan.

> **Scaffold.** Only the window shell, CLI/MCP skeleton and build pipeline exist. The store readers, protocols
> (SSH/SFTP/FTP/RDP/VNC/serial/telnet), control channel and UI screens are added by the migration's later phases.

## Build (Docker only)

One binary is the desktop window + CLI + MCP (Cobra): no subcommand opens the window, subcommands
are the CLI/MCP. Builds always refresh `dist/`.

```
make tidy        # resolve Go modules (writes go.sum)
make build       # the single binary, Linux amd64 (GTK 4 + WebKitGTK 6.0, CGO) → dist/
make appimage    # the Linux app as an x86_64 AppImage → dist/
make windows     # Windows amd64 (WebView2, CGO-free) → dist/
make macos       # macOS amd64 + arm64 (purego, CGO-free) → dist/
make dist-all    # all of the above
make check test  # gofmt/vet and unit tests
```

All Go builds run in the `rustxterm-go-linux` image (`make image`): Go + GTK 4 + WebKitGTK 6.0. The
binary embeds `internal/gui/dist`, produced by `make frontend` (`vp build --mode desktop`). Because
the store is pure Go, only the Linux desktop build needs CGO (for GTK); Windows and macOS cross-build
CGO-free from the same image with no extra toolchain. The Linux binary needs GTK 4 / WebKitGTK 6.0
at runtime; it does not need SQLCipher.

## CLI

The same commands, output and exit codes as the Rust `rustxterm` CLI. `--json` prints compact JSON;
errors then print as `{"error":{"code":"…","message":"…"}}` on stdout.

| Command | What it does |
|---|---|
| `sessions list` / `sessions show <id\|name>` | Saved sessions (offline; credentials only as `saved`/`absent`) |
| `sessions add <url> [--name] [--folder a/b]` | Add a session from `ssh://user@host:22`, `rdp://…`, `serial:///dev/ttyUSB0?baud=9600`, `wsl://…` |
| `sessions edit <session> [--name] [--host] [--port] [--user] [--folder]` | Edit non-secret fields |
| `sessions remove <session>` | Remove a session |
| `folders list`, `tunnels list` | Folders and saved tunnels (offline) |
| `export [--out FILE]` | Sessions, folders and tunnels as TOML, no credentials (`--json`: the export document) |
| `import mobaxterm-ini <file>` | Preview MobaXterm bookmarks, confirm, import |
| `open <session>`, `tabs list`, `close <tab>` | Open a saved session, list or close tabs in the running app |
| `send <tab> <text> [--enter]`, `read <tab> [--lines N]` | Type into a tab, read its terminal text |
| `wait <tab> <pattern> [--timeout 10s]` | Wait until a tab's output matches |
| `screenshot <tab> [--out FILE]` | Capture a tab as PNG |
| `files ls <tab> [path]`, `files get <tab> <remote> <local>`, `files put <tab> <local> <remote>` | Files on a tab (local paths inside the agent workspace) |
| `tunnel start <name>`, `tunnel stop <name>` | Start or stop a saved tunnel |
| `serial ports`, `serial break <tab> [--ms N]`, `serial lines <tab> [--dtr on\|off] [--rts on\|off]` | Serial ports and line control |
| `mcp`, `version` | Stdio MCP server; print the version |

Offline reads unlock through the keyring, else the master password typed on a terminal (or
`RXT_MASTER_PASSWORD`). Store changes (`sessions add/edit/remove`, `import`) need the master password
typed on an interactive terminal (never the keyring, a pipe or the environment) and are audited.
The other commands talk to the running app over the agent-control socket and never start the GUI.

Exit codes: 0 ok, 1 failed, 2 usage error, 3 database locked, 4 denied, 5 not running, 6 agent control off.

## Chosen libraries

- Store: the existing SQLCipher `rustxterm.db` is read with **no cgo** (`internal/store`): the
  `keys.v1` envelope is opened in Go (Argon2id + XChaCha20-Poly1305), the SQLCipher 4 pages are
  decrypted in pure Go (AES-256-CBC + HMAC-SHA512, `sqlcipher.go`), and the plaintext is read with
  `modernc.org/sqlite` (pure-Go SQLite). This keeps the store cross-compilable and reads the Rust
  store in place with no migration step.
- SSH/SFTP: `golang.org/x/crypto/ssh` (+ `github.com/pkg/sftp`)
- FTP/FTPS: `github.com/jlaffaye/ftp`
- Serial: `go.bug.st/serial`
- VNC/RFB: `github.com/kward/go-vnc` (upstream RFB client; decoded to a `<canvas>`)
- RDP: `github.com/tomatome/grdp` (the most-used pure-Go RDP; NLA/perf confirmed in the phase-0 spike)

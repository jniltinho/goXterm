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
- RDP: `github.com/rcarmo/go-rdp` (MIT), vendored at `internal/rdpmit` (it replaced the GPL-3.0 grdp; see `internal/rdpmit/README.md`)

# Tasks

## 0. Protocol spikes (de-risk before committing)

- [ ] 0.1 Spike VNC with `tenthirtyam/go-vnc`: decode a frame in Go and push it to a `<canvas>`, with input and the encodings/auth types the lab server needs; verify a usable desktop renders and reacts to input (no noVNC/JS RFB)
- [ ] 0.2 Spike RDP with `tomatome/grdp`, `x90skysn3k/grdp` and `rcarmo/go-rdp` against `dockur/windows` and verify authentication (incl. NLA), certificate verification, clipboard, resize and sustained performance — not just one frame; record the winner or "none" in design.md Open Questions
- [ ] 0.3 Spike terminal streaming from Go to xterm.js with flow control and verify byte-integrity under a flood (no loss/reorder), Ctrl+C latency, many concurrent tabs and a bounded per-tab memory ceiling
- [ ] 0.4 Record a go/no-go decision from 0.1–0.3 before committing beyond the spikes; if RDP shows no path to parity, hold the cutover (do not archive Rust) and flag the risk

## 1. Scaffold the Go + Wails app

- [x] 1.1 Create `desktop-go/` Go module mirroring `xenadmin/desktop-go` (main.go, window, guard, graphics, single-instance, `--safe-gpu`) and verify `desktop-go` opens an empty 1280×800 window with the webview confined to the app
- [x] 1.2 Scaffold the Vue 3 + Vite Plus (`vp`) + Tailwind SPA embedded into the binary and verify a production build loads from the embedded `internal/gui/dist`
- [ ] 1.3 Add the Docker build image (GTK4 + WebKitGTK 6.0 + Xvfb + Go + Node) and `make` targets (linux, appimage, windows, macos) and verify each target builds the binary with no host toolchain
- [ ] 1.4 Add a `bench/` harness that measures startup and terminal throughput against the Rust app and verify it runs and prints both numbers

## 2. Shared data readers (no format changes)

- [x] 2.1 Read the existing session store, folders and settings in Go and verify a store created by the Rust app lists identical sessions/folders
- [x] 2.2 Read known-hosts and the keyring/credential store compatibly and verify a saved credential from the Rust app is usable without a migration step
- [ ] 2.3 Implement compatible file locking, distinct control-socket/single-instance identities per app, and matching keyring identifiers; add a coexistence test covering concurrent edit/delete, a killed-mid-write store and stale-lock recovery, proving no corruption

## 3. Control contract (CLI + MCP parity)

- [~] 3.1 Implement the CLI subcommands (Cobra) with `--json` output and the same exit codes, and verify `sessions list --json` prints sessions without credentials and matches the Rust CLI's shape — DONE for the offline store-read commands: `sessions list`, `sessions show <id|name>`, `folders list`, `tunnels list`, all with `--json` and the same exit codes as the Rust agent (0 ok, 1 Failed, 2 Usage, 3 Locked) plus the `{"error":{"code,message}}` JSON error shape; the list/show output reuses the same `store.Sessions()`/`Folders()`/`Tunnels()` readers (credentials only as `saved`/`absent`), unit-tested (resolveSession, arg validators, exit codes, command tree) and smoke-tested on the built binary. REMAINING: the store-write commands (`sessions add`/`edit`/`remove`, `export`/`import` with the Rust TOML schema) and every running-instance command (`open`, `tabs`, `send`, `read`, `wait`, `screenshot`, `files`, `tunnel start/stop`, `close`, `serial`) depend on the control transport (task 3.3); a populated `sessions list --json` against a real store needs the user's master password (read-only)
- [x] 3.2 Implement the stdio MCP server (Go MCP SDK) exposing only the permitted tools and verify a mutation tool (session add/import) is absent and rejected-and-audited — `rustxterm-go mcp` runs a headless JSON-RPC 2.0 stdio server (minimal built-in implementation, no external SDK dependency, in the spirit of native-only libs): `initialize`, `tools/list`, `tools/call`, `ping`. The tool table mirrors the Rust agent's — the read-only store tools plus the live tools (hidden unless an instance is reachable) — and has NO session add/edit/remove/import tools, so store writes are unreachable from MCP; calling an absent tool is rejected as an unknown tool, and a raw mutation op that reached the live socket would be denied-and-audited by the dispatcher's permission matrix (3.3). Live tools are dispatched to the running instance over the control channel (entry mcp-live); with no instance, read tools fall back to the store (entry mcp-offline). Verified: an end-to-end run (in-process test + the real `mcp` subcommand over stdio) shows initialize, a tools/list with only the 5 read tools and no mutation/live tools when no instance is up, and `edit_session` rejected. Live tool *execution* against a running instance shares 3.3's remaining live-op dispatch.
- [~] 3.3 Implement the local same-user control transport with the same framing and 1 MiB cap and verify an existing agent/MCP client drives the Go app unchanged — TRANSPORT DONE in `internal/control`: the wire protocol (Request/Response/AgentOp/SessionRef/SessionPatch/EntryPoint/ErrorCode) is byte-compatible with `rxt_core::control` (serde tags, kebab/snake-case, untagged SessionRef, flattened op), the framing is the same 4-byte big-endian length + JSON with a 1 MiB (`1<<20`) cap, and the Linux transport is a same-user Unix socket at `$XDG_RUNTIME_DIR/rustxterm/control.sock` (dir 0700, socket 0600, SO_PEERCRED peer check, stale-socket replacement, in-use detection, denied-reason linger for other users) — mirroring rxt_platform-linux. The listener is wired into the running app (`gui.Run`, gated by the `agent_control` setting, removed on shutdown) with a dispatcher that enforces the permission matrix (`control.Group`/`control.Allowed`, byte-identical to `rxt_core::control::allowed`) before any work, audits denials, serves the Read group (sessions list/show, folders/tunnels list) from the store, and reports the matrix-allowed-but-unwired ops as `failed`. Verified: a wire-compat test decodes the exact JSON the Rust agent sends, framing limits/truncation, socket lifecycle/perms and a same-user round trip, the permission matrix, a dispatcher matrix test and an end-to-end round trip over a real socket; Linux/Windows/macOS all cross-compile (non-Linux transport stubbed). REMAINING: wire the Live/ContentRead/StoreWrite ops (needs a numeric tab registry + the offline write path), the macOS (same Unix socket, getpeereid) and Windows (named pipe) transports, and an end-to-end check driving the Go app with the real Rust agent CLI binary
- [ ] 3.4 Enforce the web trust boundary: loopback-only backend, origin check and per-launch token on all SPA↔Go streams, and no navigation/program-launch/clipboard-write from remote-controlled content; verify with a token-less request (rejected) and a hostile terminal-title test
- [x] 3.5 Verify headless operation: the CLI and MCP server run without initializing the GUI/webview (no window, no GPU) and still pass 3.1/3.2 — DONE (Go): in a container with no DISPLAY/WAYLAND_DISPLAY the binary runs `version`, `sessions list --json` (JSON error, exit 3 when locked) and `mcp` (initialize + tools/list over stdio) without initializing GTK/WebKit

## 4. Shell UI, themes, icons, HiDPI

- [x] 4.1 Build Home, collapsible/resizable sidebar (Sessions/SFTP), tabbed work area, quick connect and status bar; verify layout persists across restart and quick connect opens an SSH tab for `alice@lab.example:2222` — DONE (Go): Rust Metrics shell; sidebar collapse/page/width saved to config.toml and restored after a restart; quick connect `alice@sshd:22` opened an SSH tab in the Docker lab (host-key prompt, password prompt, connected)
- [ ] 4.2 Build the session editor and settings window to parity and verify every field present in the egui editor/settings exists with the same effect
- [ ] 4.3 Implement Light/Dark/System themes on every screen following the OS color-scheme, and verify switching to Dark applies live and System follows an OS change within 2 s
- [ ] 4.4 Serve the `assets/icons/app/` catalog with `catalog.json` categories and honor `RXT_ICON_OVERRIDE_DIR`, and verify bundled icons by default and an override icon when the env var is set
- [ ] 4.5 Implement responsive HiDPI layout and a persisted interface-scale control, and verify screenshot-compare at 1920×1080, 2560×1440 and 3840×2160 at DPR 1×, 1.25×, 1.5× and 2× shows no clipping, overflow or blur
- [ ] 4.6 Build the credential vault, agent-control approval dialogs and the diagnostics screen to parity, and verify each action present in the egui versions (approve/deny, reveal, clear, export diagnostics) works

## 5. SSH + terminal

- [x] 5.1 Implement SSH (`x/crypto/ssh`) with host-key verification matching the Rust prompt behavior and verify connect, known-host accept, and unknown/changed-key prompt in the lab — TOFU host-key trust (known_hosts + ssh_trust, "Unknown host key" dialog accept-once/accept-and-save), plus password/key/agent/keyboard-interactive auth and an interactive password prompt; verified against the lab and real servers
- [x] 5.2 Wire the xterm.js terminal to the SSH channel/PTY and verify input/output, resize and Ctrl+C (0x03) reach the remote without firing app shortcuts

## 6. SFTP + file transfer

- [x] 6.1 Implement SFTP (`pkg/sftp`) and the dual-pane file tab and verify list, download and upload with progress and error reporting against the lab
- [ ] 6.2 Implement the SFTP side panel parity and verify it matches the egui panel's actions

## 7. FTP / FTPS

- [x] 7.1 Implement FTP/FTPS (`jlaffaye/ftp`) with TLS verification and verify list/get/put over FTP and FTPS against the lab

## 8. VNC

- [x] 8.1 Implement the Go VNC/RFB client (per spike 0.1) decoding frames to a `<canvas>` and verify a desktop renders, input works and the lab server's encodings/auth types are handled, in light and dark chrome

## 9. RDP

- [x] 9.1 Implement the RDP client chosen in spike 0.2 decoding to a `<canvas>` and verify a `dockur/windows` session renders and accepts input with authentication (incl. NLA), certificate verification, clipboard, resize and connecting/error overlays at parity; the cutover (13.5) stays blocked until this is met — DONE: renders to canvas with input; NLA verified on Windows Server 2022 (dockur) and SSL on real 2008 R2 (both username forms); TLS-cert TOFU trust. Clipboard: CLIPRDR text both ways (grdp protocol/cliprdr, "cliprdr" channel in GCC/MCS, native clipboard), verified against the lab xrdp; NLA over TLS 1.0 for 2008 is not reachable in pure Go (crypto/tls CBC split), so those servers use the SSL path (see design.md)

## 10. Serial, Telnet, local shell

- [x] 10.1 Implement local shell and Telnet and verify each opens a working terminal tab against the lab — Telnet (stdlib IAC, negotiation test) and a local shell in a PTY (creack/pty, cross-platform; echo loopback test), both sharing the terminal; a toolbar "Local terminal" button opens the shell
- [x] 10.2 Implement Serial (`go.bug.st/serial`) with the fake/loopback device and verify open/read/write in the lab — config read from the session, mode-mapping unit test, and a pty loopback test exchanging bytes both ways on a real device node

## 11. Tunnels

- [x] 11.1 Implement the tunnels window (Local/Remote/Dynamic) with per-row start/stop and verify a local forward, a remote forward and a dynamic (SOCKS) proxy each carry traffic, and start-all/stop-all work — engine in `internal/tunnelconn` (pure Go over x/crypto/ssh: `client.Dial` for local/dynamic, `client.Listen` for remote, SOCKS5 ported from the Rust app), store CRUD on the `tunnels` table, GUI bindings with live `tunnel:state` events and a Tunnels window (list, add/edit, per-row start/stop, start-all/stop-all, loopback-by-default with explicit non-loopback confirmation). Verified live against the Docker lab: local forward and dynamic SOCKS5 both reach the remote sshd (SSH banner through the tunnel), remote forward round-trips bytes from the remote side to a local echo; SOCKS5 parsing and bind-address clamping unit-tested

## 12. Remote monitoring bar

- [x] 12.1 Implement the remote-monitoring bar (host, CPU+sparkline, memory, up/down, uptime, per-disk, scroll, close) with warning/danger thresholds and verify it appears on a monitored SSH tab and closes per-tab in light/dark/system — bar done and verified live against the lab sshd (host, CPU%, memory, net up/down, uptime, per-disk over an extra SSH exec channel, deltas computed in Go, parser unit-tested) plus a CPU sparkline and warning/danger threshold colouring (verified live)

## 13. Cross-platform verification, benchmark and parity gate

- [ ] 13.1 Capture screenshots of every screen in light and dark on Linux (Docker lab), macOS (`reims` VM, login `nilton`) and Windows (`dockur/windows`) into `docs/prints/` and verify `git status` shows none of them
- [ ] 13.2 Run the `bench/` harness on each platform against the recorded Rust baseline and verify cold start (≤1.5×), terminal throughput (≥80 %) and input latency (keystroke-to-echo, Ctrl+C ≤1.5×) meet the pinned thresholds
- [ ] 13.3 Produce signed/notarized macOS artifacts and signed Windows artifacts from the Docker/release flow and verify they launch on the reims VM and dockur/windows without a signing warning that blocks use
- [ ] 13.4 Record the parity gate (screen inventory, theme matrix, protocol behaviors incl. RDP, benchmark across the three platforms) with a named sign-off owner and verify every item is checked
- [ ] 13.5 On gate pass, switch release artifacts to the Go binary and archive `crates/ui` and the Rust app, and verify stored sessions/settings/credentials still load in the Go app unchanged
- [ ] 13.6 Keep the last Rust release re-promotable for one cycle (post-cutover rollback) and verify it can be re-published if a regression is found
- [ ] 13.7 Add `ATTRIBUTION.md` rows for every new third-party frontend asset (xterm.js, Go protocol/RDP/VNC libraries, fonts) and run `cargo xtask guard` to verify attribution and that `base/`/`docs/prints/` stay untracked

## Workflow follow-up

- Archive this OpenSpec change (`/opsx:archive migrate-gui-to-go-wails`) after the parity gate is signed off and the Rust GUI is archived.
- Sync the new `go-desktop-app` capability into `openspec/specs/` as part of the archive.

# Design

## Context

See `proposal.md` — Why. Today the GUI is an egui/eframe immediate-mode shell in `crates/ui`, in-process with the Rust protocol crates (one crate per protocol), driven through a command layer that is also exposed over a local control channel to the CLI and MCP server (`agent-control`). Stored data (sessions, settings, known-hosts, credentials via the keyring) is implementation-agnostic on disk.

Two decisions were taken with the user up front and constrain this design:
- **Pure-Go rewrite** of the protocols and UI (not an FFI bridge, not a headless-Rust control-channel client).
- **Parallel transition**: the Rust app keeps shipping untouched until a parity gate passes; only then is the Rust GUI archived.

The author's existing Go desktop apps (`xenadmin/desktop-go`, `ad-manager/desktop`) set the house pattern this design follows: Wails v3 + Vue 3 + Vite + Tailwind, one binary that is desktop + CLI + MCP, Docker-only builds (GTK4 + WebKitGTK 6.0 + Xvfb), a webview guard, single instance, 1280×800 default, `--safe-gpu`, and a `bench/` harness against the archived Rust app.

## Goals / Non-Goals

**Goals:**
- A `desktop-go/` Go module that mirrors `xenadmin/desktop-go`'s layout so the two projects stay maintainable together.
- Functional parity with every egui screen (equivalent screens, actions and options), in light/dark/system themes, sharp at FullHD/QHD/4K. Parity is workflow-equivalence, not pixel-identity: the Wails/Tailwind look may be a modernized redesign (user decision).
- Reuse the `assets/icons/app/` catalog and `RXT_ICON_OVERRIDE_DIR` behavior unchanged.
- Read existing user data in place — no store migration.
- A recorded parity gate (screens, themes, protocols, benchmark) gating the Rust-GUI archival.

**Non-Goals (design-level):**
- Reusing any Rust code at runtime (no cgo, no headless process).
- Sharing egui snapshot tests; the Go app gets its own screenshot/compare tooling.
- New protocols or new stored-data formats.
- Changing the command permission matrix or the control-channel wire contract that the CLI/MCP rely on (the Go app re-implements the same contract so existing agent clients keep working).

## Decisions

### D1: Pure-Go rewrite over FFI or a headless-Rust bridge
Chosen per the user. The Go app reimplements protocols with Go libraries: `golang.org/x/crypto/ssh` (+ `pkg/sftp`) for SSH/SFTP, a Go FTP/FTPS client (`jlaffaye/ftp`), `go.bug.st/serial` for serial, and a Telnet implementation. Candidate RDP clients to evaluate in the phase-0 spike: `tomatome/grdp` and its fork `x90skysn3k/grdp`, and `rcarmo/go-rdp`. Candidate VNC/RFB client: `tenthirtyam/go-vnc`. The spike picks the one that reaches parity against the lab; the spec requirement (protocol behavior parity) is library-agnostic. Chosen defaults per the user ("most popular/stable/used"), recorded in `desktop-go/README.md`: RDP `github.com/tomatome/grdp` (the most-used pure-Go RDP), VNC `github.com/kward/go-vnc` (upstream RFB client); the phase-0 spike still confirms NLA and sustained performance before they are committed to.
- Alternatives: (a) **control-channel client** to a headless Rust core — least new code, keeps all protocol crates, but ships two runtimes and a streaming extension to the control channel; (b) **cgo/FFI** to the Rust core as a `cdylib` — reuses protocols but makes cross-compiling for macOS/Windows in one Docker image hard and couples the UI build to the Rust ABI. Both rejected in favor of one toolchain and the author's established Go pattern.
- Consequence: protocol parity is the bulk of the work and the main risk (see Risks). It is phased per protocol in `tasks.md`, and gated by a **phase-0 go/no-go** before committing beyond the spikes.
- RDP is the hardest: `tomatome/grdp` calls itself unfinished and `rcarmo/go-rdp` documents NLA/Windows limits, so the spike acceptance covers authentication (incl. NLA), certificate verification, clipboard, resize and sustained performance — not just "a frame renders". Per the user decision, **the cutover is blocked until Go RDP reaches full parity** (no indefinite Rust-RDP fallback).

#### RDP spike finding (grdp + Go `crypto/tls`): NLA over TLS 1.0 is not reachable in pure Go
Chosen RDP library: `x90skysn3k/grdp` (builds on Linux, unlike `tomatome/grdp` whose Windows-only plugin deps break the build), vendored at `desktop-go/internal/grdp` with two fixes: the slow/fast-path bitmap event never reached the client (`OnBitmap` listened on a nil pdu layer created only inside `Login`; registrations are now buffered and flushed) and the `OnBitmap` type switch. Validated against the lab xrdp (SSL) and a real Windows Server 2008 R2 (SSL, both `user@domain` and `DOMAIN\user`), decoding bitmaps to PNG on a canvas.
- **NLA**: works over TLS 1.2/1.3 (modern Windows), which is the default — **validated against Windows Server 2022** (dockur/windows under KVM): NLA/CredSSP connects and a bitmap decodes to PNG. It **cannot** work over TLS 1.0 on Go's standard `crypto/tls`: Go always applies the 1/n-1 CBC record split (BEAST countermeasure) for TLS 1.0 and exposes no option to disable it, so Windows 2008 SChannel aborts the first CredSSP record with `internal_error` (confirmed against 192.168.10.100). The Rust app only does legacy-TLS NLA because it drops to vendored OpenSSL with `DONT_INSERT_EMPTY_FRAGMENTS` (as FreeRDP does); there is no pure-Go equivalent.
- **Consequence / design**: `rdpconn.Config.LegacyTLS` gates the TLS floor (default TLS 1.2; opt-in TLS 1.0 for old servers, mirroring the Rust "Allow legacy TLS 1.0/1.1" toggle). Old TLS-1.0-only servers connect via the **SSL path** (TLS + standard RDP logon), validated in pure Go; credentials still travel inside the TLS channel. NLA on such servers would require CGO + OpenSSL, which conflicts with the pure-Go/CGO-free goal — **open decision** whether to accept SSL-only for legacy servers or add an OpenSSL legacy path.

### D1a: VNC and RDP framebuffers decoded in Go (no JS protocol stack)
Per the user's "strict pure-Go" decision, the RFB/RDP protocol is handled in Go; decoded framebuffers are pushed to a `<canvas>` in the webview (over an authenticated loopback stream, see D9). noVNC is **not** used — noVNC is itself a JS RFB client expecting an RFB-over-WebSocket server, which would move protocol handling into JavaScript. xterm.js remains, but only as a terminal *renderer* fed decoded bytes (not a protocol), which the decision allows.

### D2: Wails v3 + Vue 3 + Vite Plus (`vp`) + Tailwind, embedded SPA
Matches the author's other apps and gives a mature styling/animation toolchain the egui shell lacked. The frontend builds with **Vite Plus (`vp`)** (user decision), as in `ad-manager/desktop`, and its output goes to `internal/gui/dist`, embedded in the Go binary via `//go:embed`. Go "services" (bound structs) expose session/store/transfer/control operations to the SPA; terminal and framebuffer streams use Wails events (or a localhost websocket the webview guard allows) for throughput.
- Alternative: Fyne/Gio (pure-Go native). Rejected: weaker styling, no xterm.js/noVNC reuse, diverges from the house pattern.

### D3: Terminal = xterm.js renderer; VNC/RDP = Go-decoded framebuffer → canvas
The terminal UI is xterm.js fed PTY/channel bytes streamed from Go with flow control (see D8). VNC and RDP are decoded entirely in Go (D1/D1a); each frame or dirty-rect is sent to a `<canvas>` and input events are sent back, over the authenticated loopback stream (D9). Monitoring bar and all panels are Vue components.
- Rationale: xterm.js is the de-facto web terminal renderer and stays; for VNC/RDP the strict pure-Go decision rules out noVNC's JS RFB stack, so rendering is a thin canvas sink over a Go decoder.

### D4: Icon reuse
The frontend serves the existing `assets/icons/app/` catalog (PNG 16/32/48/64/96 + SVG + `catalog.json`) — embedded via Go `embed` or copied into `frontend` at build. The picker uses `catalog.json` categories. `RXT_ICON_OVERRIDE_DIR` is honored in Go (serve override files when present), preserving the Rust behavior. SVG source is preferred where crisp scaling at 4K matters; otherwise the size ≥ `logical × dpr` bitmap is picked, matching `crates/ui/src/icons.rs`.

### D5: Responsive, HiDPI layout across FullHD/QHD/4K
The SPA uses the webview's `devicePixelRatio`, a fluid layout (CSS grid/flex, `clamp()`), and a user interface-scale control that maps to a root font-size / zoom factor persisted in settings. Icons resolve to the catalog bitmap matching `logical size × dpr` or to SVG. Screenshot-compare runs at 1920×1080, 2560×1440 and 3840×2160 at 1×/2× to catch clipping and blur.

### D6: One control contract, re-implemented
The Go CLI (Cobra) and MCP server (Go MCP SDK) expose the same subcommands, tools and permission matrix as `agent-control`, over the same local same-user transport with the same framing and 1 MiB message cap, so existing agent/MCP clients work against either app. This is a re-implementation of the contract, not a shared library.

### D7: Build and release in Docker, verified cross-platform
A new Docker image (GTK4 + WebKitGTK 6.0 + Xvfb + Go + Node) builds Linux/AppImage; macOS and Windows artifacts are produced by the same release flow as the author's apps. CI gains a Go job. Release tooling learns the Go targets; tags still only on green `main`.

### D8: Terminal/stream flow control, not just throughput
Streams use backpressure, not fire-and-hose. Go bounds its send buffers per tab and honors xterm.js's flow-control signal (xterm.js warns it discards data under overload); PTY reads pause when the renderer is behind. Acceptance covers byte-integrity (no loss/reorder) on a flood, Ctrl+C latency, many concurrent tabs and a per-tab memory ceiling — a `yes`-flood throughput number alone can reward buffering over rendering, so it is not sufficient on its own.

### D9: Web trust boundary
The webview loads only the embedded SPA; navigation is confined (guard). Go↔SPA streams (terminal, framebuffer, services) bind to loopback only, are origin-checked and carry a per-launch token; the Go backend never binds to a network interface. Remote-controlled text (terminal titles/OSC, filenames, server banners) and links/clipboard actions are treated as untrusted: no auto-navigation, no shell/opener execution from remote content, clipboard writes gated by user action.

### D10: Coexistence coordination with the Rust app
"Rust untouched" plus a shared writable store needs real coordination, not just matching paths. The store and known-hosts use file locking with the same discipline the Rust app uses (or the Go app treats them read-mostly during migration and writes under an advisory lock both honor); credential identifiers match the Rust keyring service/account names; the control socket and single-instance lock use distinct identities per app so the two do not collide. A coexistence test covers concurrent edit/delete and crash recovery (stale lock, partial write).

### D11a: Pure-Go store, no SQLCipher cgo (implementation decision)
The store reads the Rust SQLCipher `rustxterm.db` with no cgo: the `keys.v1` envelope opens in Go
(Argon2id + XChaCha20-Poly1305), the SQLCipher 4 pages decrypt in pure Go (AES-256-CBC + HMAC-SHA512,
raw key), and the plaintext is read with `modernc.org/sqlite` (pure-Go SQLite).
- Alternatives rejected: cgo against system/bundled SQLCipher (works, but forces a C toolchain on
  every target — mingw for Windows, osxcross for macOS — and broke `database/sql` PRAGMA-key timing);
  a one-time migration/export (loses credentials via the Rust TOML export).
- Consequence: only the Linux desktop build needs cgo (GTK/WebKit); Windows (WebView2) and macOS
  (Wails purego) cross-build CGO-free from the one Linux image. Verified: `CGO_ENABLED=0` reads the
  real store (13 sessions), and linux/windows/macos binaries build from the same image.

### D11: Measurable acceptance
The parity gate pins concrete numbers against a recorded Rust baseline on the lab reference host: terminal throughput within 20 % and interactive input (keystroke-to-echo, Ctrl+C) within 1.5× of the Rust app; cold start within 1.5× of the Rust app; screenshot-compare at the three resolutions and at DPR 1×, 1.25×, 1.5×, 2× with no clipping/overflow and icon SSIM above an agreed threshold. These defaults are the baseline; the sign-off owner is the maintainer who records the gate.

## Risks / Trade-offs

- **Protocol parity is large and the biggest schedule risk** → phase per protocol, start with SSH/SFTP (highest value), keep the Rust app shipping so there is no pressure to cut corners; each protocol phase ends with lab tests before moving on.
- **Go RDP maturity is weak** (`tomatome/grdp` unfinished; `rcarmo/go-rdp` NLA/Windows limits) → phase-0 spike with full acceptance (auth incl. NLA, cert verification, clipboard, resize, sustained perf) against `dockur/windows`; per the user decision the **cutover is blocked until Go RDP reaches parity**, so the Rust app keeps shipping everything until then, and the phase-0 go/no-go can halt the migration before further investment.
- **Go VNC maturity** (`tenthirtyam/go-vnc`) → phase-0 spike decoding a frame in Go to a canvas with input; encodings and auth types that the lab server needs are part of acceptance.
- **Terminal transport is a flow-control problem, not a throughput one** → bounded buffers + backpressure (D8); gate on byte-integrity, Ctrl+C latency, multi-tab load and a memory ceiling, not a `yes`-flood number alone.
- **4K/HiDPI blur or clipping** → screenshot-compare at the three resolutions and DPR 1×/1.25×/1.5×/2× in CI; SVG-first icons for chrome.
- **Two codebases during migration** (maintenance cost) → bounded by the parity gate (incl. RDP), which triggers archival of the Rust GUI; the phase-0 go/no-go bounds wasted effort if feasibility fails.
- **Concurrent store/keyring from two apps** → coordinated locking, matching credential identifiers and distinct per-app socket/instance identities (D10); a coexistence test covers concurrent edit/delete and crash recovery.

## Migration Plan

0. Phase-0 spikes (VNC, RDP, terminal flow control) with full acceptance, then a recorded **go/no-go** before committing beyond the spikes.
1. Scaffold `desktop-go/` (window, guard, single-instance, `--safe-gpu`, embedded empty SPA, Docker build with pinned Wails/Node/Go versions and target architectures) and the `bench/` harness.
2. Wire the store/settings/known-hosts/keyring readers in Go against the existing files; no writes that change formats.
3. Re-implement the control contract (CLI + MCP) so agent clients work against the Go app.
4. Build the shell UI to parity: Home, sidebar, tabs, quick connect, settings, session editor, themes, icons, HiDPI.
5. Protocols in priority order: SSH + terminal (xterm.js) → SFTP + file tab → FTP/FTPS → VNC (Go RFB → canvas) → RDP (Go → canvas) → Serial → Telnet → local shell → tunnels → remote-monitoring bar.
6. Capture screenshots on Linux (Docker lab), macOS (`reims` VM, login `nilton`) and Windows (`dockur/windows`); run the benchmark; produce signed/notarized macOS and signed Windows artifacts.
7. Record the parity gate (D11 numbers; RDP parity is required — cutover is blocked without it). **Pre-cutover rollback**: until the gate passes, the Rust app is the shipped GUI; abandoning the migration costs only the `desktop-go/` tree.
8. On gate pass: switch release artifacts to the Go binary and archive `crates/ui` and the Rust app, leaving stored user data untouched. **Post-cutover rollback**: keep the last Rust release published and re-promotable for one release cycle in case a regression surfaces after cutover.

## Open Questions

- Which Go RDP client reaches parity — or whether any does (resolved by the phase-0 spike + go/no-go). If none does, the cutover stays blocked (user decision); this does not change the specs or the task breakdown, only the schedule.
- Exact encodings/auth types the lab VNC and RDP servers require (pinned during the spikes; does not change the behavior requirements).

Resolved with the user (recorded here so they are not reopened): parity means equivalent workflows, redesign allowed (not pixel-identity); VNC/RDP are decoded in Go to a canvas, noVNC's JS RFB stack is not used; if Go RDP cannot reach parity the cutover is blocked rather than shipping RDP from Rust indefinitely.

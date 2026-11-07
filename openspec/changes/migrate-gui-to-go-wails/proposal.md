# Proposal

## Why

The current GUI is a Rust/egui immediate-mode shell (`crates/ui`). egui gives us one native binary but costs us on the UI/UX side: every widget, dialog, layout and theme is hand-drawn and hand-measured, styling and animation are limited, and reaching a polished desktop look takes large custom effort per screen. The author's other desktop products (`jniltinho/xenadmin/desktop-go`, `jniltinho/ad-manager/desktop`) have converged on a Go + Wails v3 stack (Vue 3 + Vite + Tailwind web frontend in a single Go binary that is also the CLI and MCP server) that reaches a richer, faster-to-iterate UI with far less bespoke drawing code. This change brings rustXterm onto the same stack so the GUI/UI/UX gains a mature web toolchain while keeping the single-binary, CLI-and-MCP, Docker-only-build, theme-and-icon discipline the project already requires.

## What Changes

- **New Go + Wails v3 application** under `desktop-go/` (mirroring `xenadmin/desktop-go`): one Go binary that is the desktop window, the CLI and the MCP server, with an embedded Vue 3 + Vite + Tailwind SPA frontend.
- **Full reimplementation in Go** (user decision): the protocols currently in the Rust crates (SSH, SFTP, FTP/FTPS, RDP, VNC, Serial, Telnet, plus local shell, tunnels, remote monitoring, agent control) are rewritten in Go. The Rust protocol crates are **not** bridged via FFI or a control channel; they are retired once parity is reached.
- **Visual and functional parity** with every current egui screen: Home, sidebar (Sessions/SFTP), tabbed work area, quick connect, session editor, settings window, SFTP/dual-pane file tab, tunnels window, remote-monitoring bar, terminal area (xterm.js renderer), RDP/VNC framebuffer decoded in Go and drawn to a `<canvas>`, agent-control and vault/credential dialogs, diagnostics. Light, dark and system themes on every screen.
- **Same icon set**: the bundled catalog in `assets/icons/app/` (16/32/48/64/96 px PNGs + SVG source, `catalog.json`) is reused by the Go frontend, with the `RXT_ICON_OVERRIDE_DIR` override behavior preserved.
- **Responsive, HiDPI-correct layout across FullHD, QHD and 4K**: the frontend scales cleanly at 1920×1080, 2560×1440 and 3840×2160 and at fractional device pixel ratios (1×, 1.25×, 1.5×, 2×), with no blurry icons, clipped panels or horizontal overflow; interface scale is user-selectable and the icon catalog's larger bitmaps / SVG are used so chrome, terminal and dialogs stay sharp at every resolution.
- **Parallel transition** (user decision): the existing Rust app (`apps/rustxterm`, `crates/ui`, protocol crates) stays shipping and untouched while the Go app is built up to parity behind its own build/target. The Go app is benchmarked against the Rust app (as in `ad-manager/desktop/bench`). Once parity is verified across the test matrix, the Rust app and `crates/ui` are archived — **BREAKING** at the delivery level (the shipped GUI becomes the Go one), with no change to stored sessions, settings, known-hosts or credentials, which the Go app reads in place.
- **Docker-only builds** for the Go app (GTK4 + WebKitGTK 6.0 + Xvfb image, matching the author's other apps and this project's "builds only in Docker" rule), with `make` targets for Linux/AppImage, Windows and macOS, a webview guard, single-instance, 1280×800 default window and `--safe-gpu`.
- **Cross-platform verification**: macOS via the `reims` VM (login `nilton`), Windows via `dockur/windows`, Linux via the Docker lab; screenshots of every screen in light/dark written to `docs/prints/` (git-ignored, as today).

### Non-goals

- Changing stored data formats (session store, settings, known-hosts, credentials) — the Go app reads the existing files.
- FFI/cgo bridge to the Rust core or a headless Rust process (explicitly rejected in favor of a pure-Go rewrite).
- Removing the Rust app in this change — it is archived only after parity is signed off (tracked here, executed as the final task).
- New protocols beyond what the Rust app already ships.

## Capabilities

### New Capabilities

- `go-desktop-app`: the Go + Wails v3 application and its migration contract — the single Go binary (desktop + CLI + MCP), embedded Vue/Vite/Tailwind frontend, reuse of the `assets/icons/app/` catalog and `RXT_ICON_OVERRIDE_DIR` override, screen-by-screen visual and functional parity with the egui shell in light/dark/system themes, pure-Go protocol reimplementation, parallel coexistence with the Rust app, the parity gate and benchmark, the Docker-only build and crash-safe graphics start, the cross-platform test matrix (reims macOS, dockur Windows, Docker-lab Linux), and the criteria for archiving the Rust app.

### Modified Capabilities

<!-- None. The existing capability specs (desktop-shell, terminal-emulation, session-management, file-transfer, remote-desktop, remote-monitoring, ssh-sessions, agent-control, reproducible-distribution, …) describe implementation-agnostic application behavior ("The application SHALL …"). The Go app must satisfy those same behaviors, so their requirements do not change; parity with them is a requirement of the new `go-desktop-app` capability instead. -->

## Impact

- **New code**: `desktop-go/` (Go module: `main.go`, window/guard/graphics, CLI via Cobra, MCP via the Go SDK, protocol packages under `internal/`, embedded `frontend/` SPA), its Docker build image, `make` targets, and a `bench/` harness against the Rust app.
- **Reused assets**: `assets/icons/app/` catalog and `catalog.json` embedded into the frontend; `ATTRIBUTION.md` gains rows for any new third-party frontend asset (xterm.js, Go protocol/RDP/VNC libraries, fonts) per the project rule; `docs/prints/` gains Go-app screenshots (git-ignored).
- **Unchanged during migration**: `apps/rustxterm`, `crates/ui`, and all Rust protocol crates keep building and shipping until the parity gate passes.
- **Retired at parity** (final task, after sign-off): the Rust GUI (`crates/ui`) and app are archived; the shipped product becomes the Go binary. Stored user data is read unchanged.
- **Build/CI**: a new Docker image and CI job for the Go app; release tooling learns to build/publish the Go binary and AppImage/macOS/Windows artifacts.
- **Dependencies**: Go toolchain, Wails v3, Vue 3 + Vite + Tailwind, xterm.js (terminal renderer), and Go protocol libraries — `golang.org/x/crypto/ssh` (+ `pkg/sftp`), an FTP/FTPS client, an RDP client (`tomatome/grdp` / `x90skysn3k/grdp` / `rcarmo/go-rdp`, chosen by the phase-0 spike), a VNC/RFB client (`tenthirtyam/go-vnc`), serial — all decoding in Go and pulled only inside the Docker build, no host toolchains.

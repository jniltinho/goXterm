# Spec Delta

## Purpose

Defines the Go + Wails desktop application that replaces the egui GUI: one Go binary that is the desktop window, CLI and MCP server, with a web frontend reaching visual and functional parity with the Rust shell across themes and screen resolutions, built and verified in parallel with the Rust app until a parity gate allows the Rust GUI to be archived.

## ADDED Requirements

### Requirement: Single binary for desktop, CLI and MCP
The Go application SHALL be one binary that runs as the desktop GUI by default and exposes the same CLI subcommands and stdio MCP server as the Rust `agent-control` capability, behind the same authorization, approval and audit boundary, with stable `--json` output.

#### Scenario: Three entry points, one binary
- **WHEN** the binary is run with no arguments, with `sessions list --json`, and with `mcp`
- **THEN** the first opens the desktop window, the second prints sessions as JSON without credentials, and the third runs the stdio MCP server exposing only the permitted tools

#### Scenario: Same permission matrix
- **WHEN** an MCP client requests a session add or import tool
- **THEN** no such tool exists and the request is rejected and audited, matching the Rust app's command permission matrix

### Requirement: Native window with confined web UI
The GUI SHALL render as a native desktop window hosting an embedded web UI whose navigation is confined to the application; external links or in-page navigation attempts SHALL NOT replace the app content. The default window SHALL be 1280×720 or larger and a single instance SHALL be enforced.

#### Scenario: Navigation stays inside the app
- **WHEN** the UI triggers navigation to an external URL
- **THEN** the app content is not replaced and the link opens in the user's default browser or is blocked

#### Scenario: Single instance
- **WHEN** the application is already running and the user launches it again
- **THEN** the existing window is focused and no second instance starts

### Requirement: Web trust boundary
The backend SHALL bind only to loopback and never to a network interface. Streams between the backend and the web UI (terminal, framebuffer, services) SHALL be origin-checked and authenticated with a per-launch token. Remote-controlled content (terminal titles/OSC sequences, filenames, server banners, links) SHALL be treated as untrusted: it SHALL NOT trigger navigation, open external programs, or write the clipboard without an explicit user action.

#### Scenario: No network exposure
- **WHEN** the app is running
- **THEN** its backend and stream endpoints listen only on loopback and reject requests without the per-launch token or with a mismatched origin

#### Scenario: Hostile terminal title
- **WHEN** a connected session sets a terminal title or OSC sequence containing a URL or control request
- **THEN** no navigation, program launch or clipboard write happens without the user acting

### Requirement: Screen parity with the egui shell
The GUI SHALL provide every screen the egui shell provides with equivalent actions: Home, collapsible/resizable sidebar with Sessions and SFTP, tabbed work area, quick connect, session editor, settings window, SFTP and dual-pane file transfer, tunnels window, remote-monitoring bar, terminal, RDP/VNC framebuffer, agent-control dialogs, credential vault and diagnostics. Parity is workflow-equivalence (same screens, actions and options), not pixel-identity; the visual design MAY be modernized.

#### Scenario: Screen inventory
- **WHEN** parity screenshots are captured for the Go app and the Rust app
- **THEN** every Rust screen and dialog has a corresponding Go screen offering the same user actions

#### Scenario: Quick connect parity
- **WHEN** the user types `alice@lab.example:2222` and presses Enter in quick connect
- **THEN** an SSH tab opens to lab.example port 2222 as alice, as in the Rust app

### Requirement: Light, dark and system themes on every screen
The GUI SHALL offer Light, Dark and System themes on every screen and dialog. System SHALL follow the desktop color-scheme preference and update live within 2 seconds when it changes.

#### Scenario: Switch theme
- **WHEN** the user selects Dark in Settings
- **THEN** all panels, dialogs and the default terminal scheme switch to dark immediately without restart

#### Scenario: Follow system
- **WHEN** the theme is System and the desktop switches from light to dark
- **THEN** the application switches to dark within 2 seconds, and uses Light with no error when no appearance source is reachable

### Requirement: Reused icon catalog with override
The GUI SHALL use the bundled icon catalog in `assets/icons/app/` for session types, toolbar and tabs, and SHALL load replacement icons from the directory named by `RXT_ICON_OVERRIDE_DIR` when it is set.

#### Scenario: Bundled icons by default
- **WHEN** `RXT_ICON_OVERRIDE_DIR` is unset
- **THEN** the bundled catalog icons are used throughout the UI

#### Scenario: Icon override
- **WHEN** `RXT_ICON_OVERRIDE_DIR` points to a directory with an icon named like a built-in one
- **THEN** that icon is used instead of the bundled one

### Requirement: Responsive HiDPI layout across FullHD, QHD and 4K
The GUI SHALL render correctly at 1920×1080, 2560×1440 and 3840×2160 and at device pixel ratios 1×, 1.25×, 1.5× and 2×, with no clipped panels, no horizontal page overflow and no blurry icons or text. Interface scale SHALL be user-selectable and persist across restarts.

#### Scenario: 4K sharpness
- **WHEN** the window runs at 3840×2160 at 2× device pixel ratio
- **THEN** icons use the catalog's larger bitmaps or vector source, chrome and dialogs stay sharp, and no content is clipped or overflows

#### Scenario: Scale persists
- **WHEN** the user sets interface scale to 1.5×, closes the app and reopens it
- **THEN** the chosen scale is restored

### Requirement: Protocol behavior parity in Go
The Go application SHALL implement SSH, SFTP, FTP/FTPS, RDP, VNC, Serial, Telnet, local shell, tunnels and remote monitoring in Go, with the externally observable behavior required by the existing protocol capability specs, including host-key verification and session/credential handling.

#### Scenario: SSH session
- **WHEN** the user opens an SSH session to a lab server with a known host key
- **THEN** the terminal connects, input and output flow, and an unknown or changed host key triggers the same verification prompt behavior the Rust app defines

#### Scenario: File transfer
- **WHEN** the user lists, downloads and uploads files over SFTP and over FTPS
- **THEN** transfers succeed with progress and error reporting equivalent to the Rust app

### Requirement: Reads existing user data unchanged
The Go application SHALL read the existing session store, settings, known-hosts and stored credentials in place without converting or rewriting their formats, so a user can run either app against the same data.

#### Scenario: Shared store
- **WHEN** a user with sessions and credentials created by the Rust app opens the Go app
- **THEN** the same sessions, folders, settings and saved credentials are available with no migration step

### Requirement: Parallel coexistence with the Rust app
The Go application SHALL run alongside the Rust app during migration without either app corrupting the other's use of the shared store, known-hosts, keyring or control channel. Shared files SHALL be accessed under compatible locking, the two apps SHALL use distinct control-socket and single-instance identities, and credential identifiers SHALL match so each app reads the other's saved credentials.

#### Scenario: Both installed
- **WHEN** both apps are installed and the Rust app is running
- **THEN** launching the Go app does not break the Rust app's store access, uses a separate control socket and instance lock, and neither app loses or corrupts stored data

#### Scenario: Concurrent edit and crash recovery
- **WHEN** one app edits or deletes a session while the other reads it, or an app is killed mid-write
- **THEN** the store stays consistent (no partial record is read as valid) and a stale lock left by the killed app is recovered on next launch

### Requirement: Parity gate before archiving the Rust GUI
The Rust GUI (`crates/ui`) and Rust app SHALL be archived only after a recorded parity gate confirms the Go app matches the Rust app on the screen inventory, the theme matrix, the protocol behaviors (RDP parity included) and a benchmark against a recorded Rust baseline with pinned thresholds, across the Linux, macOS and Windows test matrix.

#### Scenario: Gate not met
- **WHEN** any parity-gate item is unverified, including RDP not reaching parity
- **THEN** the Rust GUI is not archived and remains the shipped GUI (the cutover is blocked)

#### Scenario: Gate met
- **WHEN** every parity-gate item is verified and recorded
- **THEN** the Go binary becomes the shipped GUI and the Rust GUI is archived, with stored user data unchanged

### Requirement: Measurable parity thresholds
The parity benchmark SHALL use pinned, recorded numbers against a Rust baseline on a reference host, covering terminal throughput, interactive input latency (keystroke-to-echo and Ctrl+C), cold start, and screenshot-compare across the three resolutions and device pixel ratios, with a named sign-off owner.

#### Scenario: Thresholds recorded
- **WHEN** the gate is evaluated
- **THEN** each metric has a target value and a measured value, and the gate passes only when every measured value meets its target

### Requirement: Cross-platform verification and screenshots
Verification SHALL cover Linux (Docker lab), macOS (the `reims` VM) and Windows (`dockur/windows`), and SHALL capture screenshots of every screen in light and dark themes into `docs/prints/`, which SHALL stay git-ignored.

#### Scenario: Capture on three platforms
- **WHEN** the screenshot task runs on each platform
- **THEN** PNGs for each screen in both themes appear under `docs/prints/` and `git status` shows none of them

### Requirement: Docker-only build and crash-safe graphics start
The Go application SHALL build only inside Docker (no host toolchains) for Linux/AppImage, Windows and macOS, and a graphics driver crash during GPU start SHALL NOT make every later launch fail: the next launch SHALL fall back to a safer renderer and `--safe-gpu` SHALL force software rendering for one launch.

#### Scenario: Build in Docker
- **WHEN** a developer runs the build targets
- **THEN** the binary and installers are produced from the Docker image with no toolchain installed on the host

#### Scenario: Driver crash on start
- **WHEN** a launch dies inside the GPU driver before the window painted
- **THEN** the next launch starts on a safer renderer, says so, and does not loop on the failing backend

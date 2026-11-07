# Go app parity gate (migrate-gui-to-go-wails 13.4)

Record of the parity gate between the Go + Wails app (`desktop-go/`) and the Rust app it replaces.
Every row names its evidence. The gate passes when the sign-off below is filled in; then 13.5 (switch
the release artifacts to Go and archive the Rust app) may start.

- Date: 2026-11-07. Branch `migrate-gui-to-go-wails` (PR #1).
- **Sign-off: Nilton OS (maintainer), 2026-11-07** — approved and merged PR #1 ("vou aprovar o mr", "aprovei"), with this record and its known gaps.

## Screen inventory and themes

`make prints` (desktop-go/build/qa/prints.sh) captures 30 screens per skin on Linux (Xvfb) into
`docs/prints/go-linux/<skin>/` (local-only, git-ignored).

| Screens | Light | Dark |
|---|---|---|
| Unlock, Home with recent sessions, tree session menu, tree folder menu | ✓ | ✓ |
| Session editor: new (type chooser), SSH Advanced / Terminal / Network / Bookmark / Agent, Telnet, SFTP, FTP, FTPS, RDP, VNC, Serial | ✓ | ✓ |
| Settings: General, Terminal, SSH, Display, Shortcuts, Security, Agent control, Diagnostics | ✓ | ✓ |
| Tunnels | ✓ | ✓ |
| Live: SSH terminal + SFTP browser + monitor bar, RDP, VNC, FTP file tab | ✓ | ✓ |

Skins: Light, Dark, Windows XP, macOS, macOS Dark, Mac OS Retro (+ System). Every skin defines the same
tokens (`internal/archtest/skins_test.go`); XP, macOS and Retro were checked on the Settings and
session editor screens during QA. Dialog tabs share one component (`TabBar.vue`) and every `<select>`
is the themed `Dropdown.vue` (no native GTK popup).

## Protocols and features

| Area | Go evidence | Status |
|---|---|---|
| SSH terminal (password, keyboard-interactive, agent, keys, host-key TOFU, Save password) | `sshconn` TestLabEcho; lab QA (trust dialog, saved password reused) | ✓ |
| SFTP browser (Rust columns, owner/group, sort, file-type icons, ASCII mode, row menu, follow folder) | `sftpconn` TestLabSFTP; lab QA | ✓ |
| FTP / FTPS explicit and implicit (certificate trust, password prompt) | `ftpconn` TestLabFTPSExplicit/Implicit; `tlstrust` tests; lab QA | ✓ |
| RDP (pure Go grdp: NLA, TLS, legacy SSL/standard only when allowed, certificate trust, clipboard, release key) | `rdpconn` TestLabRDP; lab xrdp; Windows Server 2022 (NLA) and 2008 R2 (SSL) | ✓ |
| VNC (password prompt, view-only, SSH gateway, hidden-tab pause, hostile sizes, clipboard) | `vncconn` TestLabVNC + fake-server tests; lab QA through the lab sshd | ✓ |
| Telnet | `telnetconn` TestTelnetNegotiation | ✓ |
| Serial | `serialconn` TestModeMapping, TestSerialLoopback | ✓ |
| Local terminal (PTY / ConPTY, login shell, home folder) | `localconn` TestLocalShellEcho, TestPasswdShell; macOS zsh; Windows cmd.exe | ✓ |
| WSL (Windows) | `wslconn` (Rust cases ported); Windows 11 WSL 1 Alpine: list, `--cd`, startup commands | ✓ |
| SSH tunnels (local, remote, dynamic) | `tunnelconn` TestLabLocalForward / TestLabRemoteForward / TestLabDynamicSocks | ✓ |
| Remote monitoring bar, MultiExec, input order | lab QA; `multiexec` tests; ordered input queue | ✓ |
| Store (SQLCipher 4, keys.v1, keyring, credentials, agent policy) | `store` tests (official sqlcipher reads a Go store); Keychain (macOS), Credential Manager (Windows) | ✓ |
| Import MobaXterm.ini, TOML export/import | `mobaini` tests (Rust fixtures); TOML cross-checked with the Rust crate both ways | ✓ |
| Agent control (CLI / MCP, per-session policy) | `control` tests; headless gate 3.5 | ✓ |

All `TestLab*` above passed together against the Docker lab on 2026-11-07
(`RXT_SSH_LAB=1`; `TestLabKeyAuth` needs a lab key and was skipped).

## Platforms

| Platform | What ran |
|---|---|
| Linux (Ubuntu 24.04, Docker lab) | every screen in Light/Dark, every protocol above, benchmark |
| macOS 15.8.1 (reims VM, universal .app) | start, store creation, Keychain unlock, local zsh, SSH + SFTP browser + monitor bar |
| Windows 11 (dockur) | WebView2 start, store + Credential Manager unlock, ConPTY cmd.exe, WSL session |

## Benchmark (13.2)

`make bench`, both apps in one clean container, Rust baseline = release v0.8.0:

| Measure | Go | Rust | Go/Rust | Threshold |
|---|---|---|---|---|
| Cold start | 264 ms | 185 ms | 1.42× | ≤ 1.5× |
| Terminal throughput | 4.76 M lines/s | 3.57 M lines/s | 1.33× | ≥ 0.80× |
| Keystroke to process | 2.29 ms | 1.58 ms | 1.44× | ≤ 1.5× |
| Ctrl+C to SIGINT | 11.7 ms | 9.1 ms | 1.29× | ≤ 1.5× |

## Known differences and gaps (accepted or open at sign-off)

- Unsigned releases (13.3 dropped by the owner): macOS 15 denies Local Network access to the unsigned
  app, so hosts on the Mac's own subnet need the app signed or are unreachable; routed hosts work.
- The screenshot sweep runs on Linux only; macOS and Windows were checked interactively. The design's
  DPR matrix (1×, 1.25×, 1.5×, 2×) and icon SSIM compare were not run.
- The benchmark harness needs X11 (Linux); it was not run on macOS or Windows.
- RDP resize scales the image (the spec's fallback) rather than sending display-control resizes.

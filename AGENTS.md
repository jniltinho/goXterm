# Project guidelines

- rustXterm: terminal and remote session manager for Linux, macOS and Windows (SSH, SFTP, FTP/FTPS, RDP, VNC,
  Telnet, Serial, WSL, local shells, SSH tunnels, remote monitoring, agent control via CLI and MCP), written in
  **Go + Wails v3** with a Vue 3 + Tailwind frontend (Vite Plus, `vp`). One binary is the desktop app, the CLI and
  the MCP server. The former Rust/egui app is archived, read-only, in `archive/rust/`.
- Builds, lints and tests run only in Docker (`make` targets, image from `build/linux.Dockerfile`); do not install
  Go, Node or GTK on the desktop.
- `base/` and `docs/prints/` are local-only, git-ignored scratch/reference folders. Never add them to Git, Docker
  build contexts or artifacts. Run `make guard` before every push (`git config core.hooksPath .githooks` runs it).
- Every committed icon, image, font and test fixture (`testdata/`, `fixtures/`) needs a row in `ATTRIBUTION.md`
  (checked by `make guard`). Vendored code keeps its license next to it and needs a row too; no copyleft.
- Architecture: the core is GUI-free packages under `internal/` (one per protocol: `sshconn`, `sftpconn`,
  `ftpconn`, `rdpconn`, `vncconn`, `telnetconn`, `serialconn`, `localconn`, `wslconn`, `tunnelconn`, plus
  `store`, `secret`, `tlstrust`, `control`, `mobaini`, `fsentry`); only `internal/gui` imports Wails. Layering and
  skin token parity are enforced by `make layers` (`internal/archtest`).
- Protocols use Go libraries; RDP is pure Go. Keep the Rust app's stored formats (store, settings, known hosts,
  keyring entries) unchanged so existing data keeps loading.
- Light, dark and system themes (plus the extra skins in `frontend/src/themes/`) are required for every screen;
  `make prints` writes screenshots of every screen to `docs/prints/go-linux/`.
- Specs live in `openspec/specs/`; active changes in `openspec/changes/` (archive completed ones with `/opsx:archive`).
- Tests use the Docker lab (`lab/`, `make lab-test`) or small VMs with fictional credentials; never real servers.
- Code first in large batches, then one verification batch per milestone; commit and push each verified milestone
  with task ids in the message.
- Parallel agents own disjoint packages and work in their own git worktree.
- Write code, comments, CLI messages, docs and OpenSpec artifacts in English.
- Releases: tag `vX.Y.Z` only on a commit whose `ci` run on `main` is green (`release-go.yml` enforces it and only
  builds/publishes); Go versions continue the Rust numbering (v0.9.0 onward). Follow the `create-release` skill.

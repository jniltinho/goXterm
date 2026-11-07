# desktop-go — agent guidelines

- Go + Wails v3 rewrite of the rustXterm GUI. Mirrors the layout of `jniltinho/xenadmin/desktop-go` and
  `jniltinho/ad-manager/desktop`. One binary = desktop + CLI + MCP.
- Build **only in Docker** (`make` targets); never install Go/Node/GTK on the host.
- Frontend: Vue 3 + Tailwind via **Vite Plus (`vp`)**, output to `internal/gui/dist` (embedded).
- `-tags nogui` builds the CLI/MCP without the window (no CGO/GTK). The desktop build is CGO (GTK 4 + WebKitGTK 6.0).
- Reuse the Rust app's icon catalog (`../assets/icons/app/`) and honor `RXT_ICON_OVERRIDE_DIR`.
- Read the existing rustXterm store/settings/known-hosts/keyring in place; never change their formats.
- Plan and phases: `../openspec/changes/migrate-gui-to-go-wails`.

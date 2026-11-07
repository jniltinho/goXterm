# rustXterm — marcos de avanço da migração Go + Wails

Marcos (milestones) da migração da GUI desktop de Rust+egui para **Go + Wails v3**
(Vue 3 + Vite + xterm.js), com paridade MobaXterm/rustXterm, libs nativas de Go e
core modular (um pacote por protocolo). Cada marco é verificado (testes / lab Docker
/ screenshot) e está no branch `migrate-gui-to-go-wails` (PR #1). O trabalho restante
é rastreado em `openspec/changes/migrate-gui-to-go-wails/tasks.md`.

## Concluídos

| Marco | Entrega | Tasks | Verificação |
|------|---------|-------|-------------|
| **M1 — Scaffold & store** | App Wails + SPA embutida; store pure-Go (SQLCipher 4 decrypt **e** encrypt, envelope Argon2id) lendo o store do Rust sem migração | 1.1, 1.2, 2.1, 2.2 | testes de round-trip; lê store real |
| **M2 — SSH + terminal** | xterm.js sobre `x/crypto/ssh`; TOFU de host-key; auth por chave/agente (chave configurada antes do agente — sem pedir senha à toa); keepalive; banner de sessão igual ao Rust; aviso de logout com Return/R/S (salvar saída); zoom e busca no terminal | 5.1, 5.2 | `TestLabKeyAuth`, servidor real, testes do buffer/banner |
| **M3 — Transferência** | SFTP (`pkg/sftp`) + FTP/FTPS (`jlaffaye/ftp`), painel duplo | 6.1, 7.1 | lab |
| **M4 — VNC** | Cliente RFB em Go, canvas, clipboard bidirecional | 8.1 | lab |
| **M5 — RDP** | grdp vendorizado; NLA (Windows moderno) + SSL (2008 R2), TOFU de certificado; renderização corrigida (sem flip extra, ordem de bytes do RLE 16bpp, surface commands desligados → bitmap legado, sem drawing orders → sem restos na tela; desktop do tamanho da aba, escala ao redimensionar); tela "Session stopped" com Return/Ctrl+D/R — clipboard pendente | 9.1 (~) | dockur/windows, captura real do 2008 comparada pixel a pixel, Windows moderno confirmado pelo usuário |
| **M6 — Serial/Telnet/Local** | `go.bug.st/serial`, Telnet stdlib, shell local em PTY (`creack/pty`), cross-platform | 10.1, 10.2 | loopback, negociação |
| **M7 — Monitoramento** | Barra de monitoramento remoto (CPU sparkline + limiares) | 12.1 | lab |
| **M8 — Túneis SSH** | Local/Remote/Dynamic (SOCKS5) + janela de túneis | 11.1 | tráfego real no lab |
| **M9 — CLI (leitura)** | `sessions`/`folders`/`tunnels` com `--json` e exit codes do Rust | 3.1 (~) | unit + smoke |
| **M10 — Transporte de controle** | Socket Unix same-UID, framing wire-compatível (cap 1 MiB) + dispatcher com matriz de permissões | 3.3 (~) | wire-compat, round-trip |
| **M11 — Servidor MCP** | MCP stdio só com as ferramentas permitidas; mutação ausente/rejeitada-e-auditada | 3.2 | e2e stdio |
| **M12 — Paridade GUI** | Unlock, home, editor 8 protocolos (+Browse da chave), Settings 8 abas (skin/toolbar/tabs), barra de menus, toolbar com captions, árvore colapsável, traço da sessão encerrada | fase 4 | screenshots |
| **M13 — Builds multiplataforma** | Binário único (cobra: desktop+CLI+MCP) + AppImage; Linux/Windows/macOS em `dist/` | 1.3 (~) | builds verdes |
| **M14 — SFTP lateral + abas** | Painel "SFTP browser" na conexão da aba SSH (toolbar, caminho, lista, download/upload por diálogo nativo, nova pasta/renomear/apagar); *Follow terminal folder* (OSC 7 ou cwd do shell após Return); faixa vertical Sessions/SFTP (abre SFTP ao conectar, volta a Sessions ao desconectar); abas estilo Rust (🏠, `+`, borda azul, ponto de estado); barra de monitoramento com ícones; zoom Ctrl+roda; RDP sem as linhas verticais (padding do bitmap recortado) | 6.2 | lab Docker (Xvfb), testes |
| **M15 — Skins** | Light / Dark / System (tokens de cor em `theme.css`, `prefers-color-scheme`); selects com texto alinhado; Rendering backend com os valores do Rust (`cpu` liga o modo GPU seguro) | fase 4 | screenshots light/dark |

## Restante antes do cutover

- Clipboard RDP (CLIPRDR); ops *live*/write do plano de controle + transportes macOS/Windows;
  fronteira de confiança web (3.4); gate headless (3.5); gate de
  paridade multiplataforma (13.x). A GUI Rust permanece até o gate passar.

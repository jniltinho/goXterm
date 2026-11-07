// The window's API: rustxterm-go/internal/gui.App, bound by Wails v3.
import { Call } from "@wailsio/runtime";

const svc = "rustxterm-go/internal/gui.App.";
// a failed call rejects with the Go error's text alone (no Wails "RuntimeError:" prefix)
const call = <T>(method: string, ...args: unknown[]) =>
  (Call.ByName(svc + method, ...args) as Promise<T>).catch((e: unknown) => {
    throw e instanceof Error ? e.message : e;
  });

export interface Session {
  id: number;
  name: string;
  protocol: string;
  host: string;
  port: number;
  user: string;
  folder: number | null;
  icon: string;
  password: string;
}

export interface Folder {
  id: number;
  name: string;
  parent: number | null;
  icon: string;
}

export const Sessions = () => call<Session[]>("Sessions");
export const Folders = () => call<Folder[]>("Folders");

// Global settings (config.toml). GetSettings returns the full object; edit a copy and send it all
// back so unedited fields round-trip.
export interface TerminalPrefs {
  fontFamily: string; fontSize: number; cursorShape: string; cursorBlink: boolean;
  scheme: string; scrollbackLines: number; logRaw: boolean;
}
export interface Settings {
  theme: string; logLevel: string; agentControl: boolean; copyOnSelect: boolean; rightClickPaste: boolean;
  pasteGuard: boolean; confirmClose: boolean; sidebarOpen: boolean; sidebarWidth: number; sidebarPage: string;
  uiScale: string; remoteMonitoring: boolean; remoteMonitoringInterval: number; notifications: boolean;
  gpuBackend: string; firstRunThemeDone: boolean;
  toolbarCaptions: boolean; showTabNumbers: boolean; showCloseButtons: boolean;
  terminal: TerminalPrefs;
}
// Native open-file dialog to pick an SSH private key; returns "" when cancelled.
export const PickKeyFile = () => call<string>("PickKeyFile");
// System clipboard (native, no WebView permission prompt): terminal Copy/Paste.
export const ClipboardText = () => call<string>("ClipboardText");
export const SetClipboardText = (text: string) => call<void>("SetClipboardText", text);
// Quit the application (Terminal → Exit).
export const Quit = () => call<void>("Quit");
export const GetSettings = () => call<Settings>("GetSettings");
export const SaveSettings = (s: Settings) => call<void>("SaveSettings", s);
export interface Diag {
  version: string; dataDir: string; locked: boolean;
  sessions: number; folders: number; sshTrust: number; certTrust: number;
}
export const Diagnostics = () => call<Diag>("Diagnostics");

// Store unlock — Locked() is true until the master password is entered (or the keyring/env unlocks it).
export const Locked = () => call<boolean>("Locked");
export const Unlock = (password: string) => call<void>("Unlock", password);
// First start: no store yet → "Create master password" (at least 8 characters).
export const FirstRun = () => call<boolean>("FirstRun");
export const CreateStore = (password: string, keyring: boolean) => call<void>("CreateStore", password, keyring);

// Session editing — SaveSession takes the session JSON document (id 0 = new) and returns its id.
export const SaveSession = (js: string) => call<number>("SaveSession", js);
export const DeleteSession = (id: number) => call<void>("DeleteSession", id);
export const SessionJSON = (id: number) => call<string>("SessionJSON", id);
export const DuplicateSession = (id: number) => call<number>("DuplicateSession", id);
export const RenameSession = (id: number, name: string) => call<void>("RenameSession", id, name);
export const SaveFolder = (id: number, name: string, parent: number | null) => call<number>("SaveFolder", id, name, parent);
export const DeleteFolder = (id: number) => call<void>("DeleteFolder", id);

// SSH host-key trust (TOFU): the backend emits "ssh:trust" for an unknown key; answer once/save/reject.
export const SshTrustRespond = (reqId: string, decision: string) => call<void>("SshTrustRespond", reqId, decision);
// Interactive SSH password: the backend emits "ssh:password" when it needs one; answer or cancel.
export const SshPasswordRespond = (reqId: string, password: string, ok: boolean) =>
  call<void>("SshPasswordRespond", reqId, password, ok);

// Terminal (SSH) — output streams as Wails events "term:out:<tabId>" (base64) and "term:close:<tabId>".
export const Connect = (id: number) => call<string>("Connect", id);
export const QuickConnect = (host: string, port: number, user: string, password: string) =>
  call<string>("QuickConnect", host, port, user, password);
export const Send = (tabId: string, data: string) => call<void>("Send", tabId, data);
// Session banner (info box drawn when a terminal tab connects) and the output that arrived before the
// tab was listening. Call right after subscribing to term:out/term:close.
export interface BannerLine { kind: "head" | "check" | "muted"; text: string; target?: string; on?: boolean; note?: string }
export interface Banner { title: string; hint: string; lines: BannerLine[] }
export interface Attach { banner: Banner | null; early: string; closed: boolean; closeMsg: string }
export const TermAttach = (tabId: string) => call<Attach>("TermAttach", tabId);
// Save a terminal's text through a native save dialog; resolves to the path ("" when cancelled).
export const SaveTerminalOutput = (suggested: string, text: string) => call<string>("SaveTerminalOutput", suggested, text);
// Remote monitoring (SSH only): metrics stream as "monitor:<tabId>" events once started.
export const MonitorStart = (tabId: string) => call<void>("MonitorStart", tabId);
export const MonitorStop = (tabId: string) => call<void>("MonitorStop", tabId);
export interface Metrics {
  host: string; os: string; cpu: number; memUsed: number; memTotal: number;
  netUp: number; netDown: number; uptime: number;
  disks: { mount: string; usedPct: number; usedKiB: number; sizeKiB: number }[] | null;
}
export const Resize = (tabId: string, cols: number, rows: number) => call<void>("Resize", tabId, cols, rows);
export const Disconnect = (tabId: string) => call<void>("Disconnect", tabId);
// MultiExec: with it on, input typed in a selected terminal goes to every selected terminal.
export const SetMultiExec = (enabled: boolean) => call<void>("SetMultiExec", enabled);
export const SelectMultiExec = (tabId: string, selected: boolean) => call<void>("SelectMultiExec", tabId, selected);
export const HasDemo = () => call<boolean>("HasDemo");
export const ConnectDemo = () => call<string>("ConnectDemo");

// Telnet and Serial are terminals too: same term:out/term:close events and Send/Resize/Disconnect.
export const TelnetConnect = (id: number) => call<string>("TelnetConnect", id);
export const TelnetQuickConnect = (host: string, port: number) => call<string>("TelnetQuickConnect", host, port);
export const HasTelnetDemo = () => call<boolean>("HasTelnetDemo");
export const ConnectTelnetDemo = () => call<string>("ConnectTelnetDemo");
export const LocalShell = () => call<string>("LocalShell");
export const SerialConnect = (id: number) => call<string>("SerialConnect", id);
export const SerialOpen = (device: string, baud: number) => call<string>("SerialOpen", device, baud);
export const SerialPorts = () => call<string[]>("SerialPorts");
export const HasSerialDemo = () => call<boolean>("HasSerialDemo");
export const ConnectSerialDemo = () => call<string>("ConnectSerialDemo");

// File transfer — a dual pane of the local filesystem and a remote SFTP or FTP/FTPS session.
export interface Entry { name: string; dir: boolean; size: number; mode: string; modTime: number }
export interface RemoteSession { tabId: string; home: string }
// open a remote session (SFTP over a saved SSH session, or FTP/FTPS):
export const SftpConnect = (id: number) => call<RemoteSession>("SftpConnect", id);
export const FtpConnect = (id: number, mode: string) => call<RemoteSession>("FtpConnect", id, mode);
export const HasSftpDemo = () => call<boolean>("HasSftpDemo");
export const ConnectSftpDemo = () => call<RemoteSession>("ConnectSftpDemo");
export const HasFtpDemo = () => call<boolean>("HasFtpDemo");
export const ConnectFtpDemo = () => call<RemoteSession>("ConnectFtpDemo");
// operate on an open remote session (SFTP or FTP, same calls):
export const RemoteList = (tabId: string, dir: string) => call<Entry[]>("RemoteList", tabId, dir);
export const RemoteDownload = (tabId: string, remote: string, localDir: string) =>
  call<number>("RemoteDownload", tabId, remote, localDir);
export const RemoteUpload = (tabId: string, local: string, remoteDir: string) =>
  call<number>("RemoteUpload", tabId, local, remoteDir);
export const RemoteDisconnect = (tabId: string) => call<void>("RemoteDisconnect", tabId);
// Side SFTP browser of an SSH terminal tab (same connection; ids are the terminal's backend tabId),
// "Follow terminal folder" and the editing operations of a remote file session.
export const TermFiles = (termId: string) => call<RemoteSession>("TermFiles", termId);
export const TermCwd = (termId: string) => call<string>("TermCwd", termId);
export const RemoteMkdir = (tabId: string, dir: string) => call<void>("RemoteMkdir", tabId, dir);
export const RemoteRemove = (tabId: string, path: string) => call<void>("RemoteRemove", tabId, path);
export const RemoteRename = (tabId: string, from: string, to: string) => call<void>("RemoteRename", tabId, from, to);
export const RemoteDownloadAs = (tabId: string, remote: string) => call<string>("RemoteDownloadAs", tabId, remote);
export const RemoteUploadPick = (tabId: string, remoteDir: string) => call<string>("RemoteUploadPick", tabId, remoteDir);
export const LocalHome = () => call<string>("LocalHome");
export const LocalList = (dir: string) => call<Entry[]>("LocalList", dir);

// VNC — the framebuffer is decoded in Go and streamed as per-rectangle PNGs over "vnc:frame:<tab>".
export interface VncView { tabId: string; width: number; height: number; name: string }
export const VncConnect = (id: number) => call<VncView>("VncConnect", id);
export const HasVncDemo = () => call<boolean>("HasVncDemo");
export const ConnectVncDemo = () => call<VncView>("ConnectVncDemo");
export const VncStart = (tabId: string) => call<void>("VncStart", tabId);
export const VncPointer = (tabId: string, buttonMask: number, x: number, y: number) =>
  call<void>("VncPointer", tabId, buttonMask, x, y);
export const VncKey = (tabId: string, down: boolean, keysym: number) => call<void>("VncKey", tabId, down, keysym);
export const VncDisconnect = (tabId: string) => call<void>("VncDisconnect", tabId);
// Clipboard: server text arrives as "vnc:clipboard:<tab>" events; send local text with VncSendClipboard.
export const VncSendClipboard = (tabId: string, text: string) => call<void>("VncSendClipboard", tabId, text);

// SSH tunnels (port forwarding) — Local/Remote/Dynamic(SOCKS5) over a saved SSH gateway session.
// Live state streams as "tunnel:state" events ({via, name, state, error}); loopback unless confirmed.
export interface Tunnel {
  name: string; kind: string; via: number;
  bind_addr: string; bind_port: number; dest_host: string; dest_port: number;
  exposure_confirmed: boolean; autostart: boolean; running?: boolean;
}
export const Tunnels = () => call<Tunnel[]>("Tunnels");
export const TunnelSave = (js: string) => call<void>("TunnelSave", js);
export const TunnelDelete = (via: number, name: string) => call<void>("TunnelDelete", via, name);
export const TunnelStart = (js: string) => call<void>("TunnelStart", js);
export const TunnelStop = (via: number, name: string) => call<void>("TunnelStop", via, name);
export const TunnelStartAll = () => call<void>("TunnelStartAll");
export const TunnelStopAll = () => call<void>("TunnelStopAll");

// RDP — the framebuffer is decoded in Go (vendored grdp) and streamed as per-rectangle PNGs over "rdp:frame:<tab>".
export interface RdpView { tabId: string; width: number; height: number }
// tabW/tabH: the tab's size in CSS px — the remote desktop is created at that size ("fit to tab")
// unless the session has a fixed resolution.
export const RdpConnect = (id: number, tabW: number, tabH: number) => call<RdpView>("RdpConnect", id, tabW, tabH);
export const HasRdpDemo = () => call<boolean>("HasRdpDemo");
export const ConnectRdpDemo = (tabW: number, tabH: number) => call<RdpView>("ConnectRdpDemo", tabW, tabH);
export const RdpStart = (tabId: string) => call<void>("RdpStart", tabId);
export const RdpMouse = (tabId: string, kind: string, button: number, x: number, y: number) =>
  call<void>("RdpMouse", tabId, kind, button, x, y);
export const RdpKey = (tabId: string, down: boolean, scancode: number) => call<void>("RdpKey", tabId, down, scancode);
export const RdpDisconnect = (tabId: string) => call<void>("RdpDisconnect", tabId);
// Clipboard (text): remote copies go straight to the system clipboard; call RdpSyncClipboard when the
// view gets focus to offer the local clipboard to the remote desktop.
export const RdpSyncClipboard = (tabId: string) => call<void>("RdpSyncClipboard", tabId);

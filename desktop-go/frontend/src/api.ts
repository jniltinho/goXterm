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
  gpuBackend: string; firstRunThemeDone: boolean; terminal: TerminalPrefs;
}
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
export const HasDemo = () => call<boolean>("HasDemo");
export const ConnectDemo = () => call<string>("ConnectDemo");

// Telnet and Serial are terminals too: same term:out/term:close events and Send/Resize/Disconnect.
export const TelnetConnect = (id: number) => call<string>("TelnetConnect", id);
export const TelnetQuickConnect = (host: string, port: number) => call<string>("TelnetQuickConnect", host, port);
export const HasTelnetDemo = () => call<boolean>("HasTelnetDemo");
export const ConnectTelnetDemo = () => call<string>("ConnectTelnetDemo");
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

// RDP — the framebuffer is decoded in Go (vendored grdp) and streamed as per-rectangle PNGs over "rdp:frame:<tab>".
export interface RdpView { tabId: string; width: number; height: number }
export const RdpConnect = (id: number) => call<RdpView>("RdpConnect", id);
export const HasRdpDemo = () => call<boolean>("HasRdpDemo");
export const ConnectRdpDemo = () => call<RdpView>("ConnectRdpDemo");
export const RdpStart = (tabId: string) => call<void>("RdpStart", tabId);
export const RdpMouse = (tabId: string, kind: string, button: number, x: number, y: number) =>
  call<void>("RdpMouse", tabId, kind, button, x, y);
export const RdpKey = (tabId: string, down: boolean, scancode: number) => call<void>("RdpKey", tabId, down, scancode);
export const RdpDisconnect = (tabId: string) => call<void>("RdpDisconnect", tabId);

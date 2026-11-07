<script setup lang="ts">
import { ref, computed, watch, onMounted, shallowRef } from "vue";
import {
  Sessions, Folders, Locked, Unlock, FirstRun, CreateStore, Connect, ConnectDemo, HasDemo, LocalShell, QuickConnect,
  TelnetConnect, TelnetQuickConnect, ConnectTelnetDemo, HasTelnetDemo,
  SerialConnect, ConnectSerialDemo, HasSerialDemo,
  SftpConnect, FtpConnect, ConnectSftpDemo, HasSftpDemo, ConnectFtpDemo, HasFtpDemo,
  VncConnect, ConnectVncDemo, HasVncDemo,
  RdpConnect, ConnectRdpDemo, HasRdpDemo,
  type Session, type Folder, type RemoteSession, type VncView, type RdpView,
} from "./api";
import { SetMultiExec, SelectMultiExec, WslConnect, SetFolderIcon, MoveSession } from "./api";
import FolderDialog from "./FolderDialog.vue";
import { commandFor, shortcutMap, DEFAULT_SHORTCUTS } from "./commands";
import ImportDialog from "./ImportDialog.vue";
import { ImportMobaPick, ImportMobaApply, ImportTOMLPick, ExportTOMLPick, ExportTOMLWrite, type ImportPreview, type ExportPlan } from "./api";
import { SaveSettings } from "./api";
import { DeleteSession, DuplicateSession, SaveFolder, DeleteFolder, SshTrustRespond, SshPasswordRespond, GetSettings, Quit } from "./api";

// Toolbar/tab appearance flags from Settings → Display (config.toml, no unlock needed).
const ui = ref({ toolbarCaptions: true, showTabNumbers: true, showCloseButtons: true });
// Rust Metrics: sidebar 240 px (Settings value), icon strip 34 px, 16 px tree indent.
const STRIP_W = 34, INDENT = 16;
const sideW = ref(240);
const rootOpen = ref(true); // restored from localStorage in onMounted
const status = ref("Ready");
// Behaviour switches from Settings (Rust defaults until loaded).
const cfg = ref({ sftpAutoSwitch: true, remoteMonitoring: true, confirmClose: true });
// Focus a field when its dialog appears (the autofocus attribute is ignored for inserted elements).
const vFocus = { mounted: (el: HTMLElement) => setTimeout(() => el.focus()) };
async function loadUI() {
  try {
    const s = await GetSettings();
    ui.value = { toolbarCaptions: s.toolbarCaptions, showTabNumbers: s.showTabNumbers, showCloseButtons: s.showCloseButtons };
    sideW.value = Math.min(600, Math.max(160, s.sidebarWidth || 240));
    shortcutMap.value = { ...DEFAULT_SHORTCUTS, ...s.shortcuts };
    cfg.value = { sftpAutoSwitch: s.sftpAutoSwitch, remoteMonitoring: s.remoteMonitoring, confirmClose: s.confirmClose };
    if (!layoutLoaded) { // the saved layout, once (Rust: sidebar_open, sidebar_page, sidebar_width)
      sidebarCollapsed.value = s.sidebarOpen === false;
      sidePage.value = s.sidebarPage === "sftp" ? "sftp" : "sessions";
      layoutLoaded = true;
    }
    applySkin(s.theme); // Skin: themes/<skin>.css picks the palette
    window.dispatchEvent(new CustomEvent("rxt-settings", { detail: s })); // open terminals follow
  } catch { /* defaults */ }
}

// Header menu bar (MobaXterm parity): one open menu at a time; a click elsewhere closes it.
const menu = ref("");
const sidebarCollapsed = ref(false);
function toggleMenu(name: string) { menu.value = menu.value === name ? "" : name; }
function runMenu(fn: () => void) { menu.value = ""; fn(); }
function aboutApp() { void showInfo("About rustXterm", "rustXterm — native terminal & remote session manager (Go + Wails)."); }

// Global keyboard shortcuts. Chord combos (Ctrl+Shift+…, Ctrl+Tab) work anywhere; single keys
// (Enter/F2/Delete) only when focus is not in a text field or the terminal, so terminal input is never
// hijacked (Ctrl+C etc. reach the remote).
function inEditable() {
  const el = document.activeElement as HTMLElement | null;
  return !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.isContentEditable);
}
function cycleTab(dir: number) {
  if (!tabs.value.length) return;
  const i = tabs.value.findIndex((t) => t.id === active.value);
  active.value = tabs.value[(i + dir + tabs.value.length) % tabs.value.length].id;
}
// Application commands bound in Settings › Shortcuts (Rust command ids; terminal.* run in the terminal).
const quickInput = ref<HTMLInputElement | null>(null);
const APP_COMMANDS: Record<string, () => void> = {
  "session.new": () => newSession(),
  "tab.new_local": () => openLocal(),
  "session.quick_connect": () => { quickInput.value?.focus(); quickInput.value?.select(); },
  "tab.close": () => { if (active.value) void requestClose(active.value); },
  "tab.next": () => cycleTab(1),
  "tab.prev": () => cycleTab(-1),
  "tab.duplicate": () => { const t = tabs.value.find((x) => x.id === active.value); if (t) duplicateTab(t); },
  "app.settings": () => (settingsOpen.value = true),
  "app.toggle_sidebar": () => (sidebarCollapsed.value = !sidebarCollapsed.value),
  "multiexec.toggle": () => toggleMultiExec(),
  "tunnels.open": () => (tunnelsOpen.value = true),
  "app.help": () => aboutApp(),
  "app.about": () => aboutApp(),
  "app.exit": () => void requestExit(),
  "session.import": () => void importSessions(),
};
function onKey(e: KeyboardEvent) {
  const cmd = commandFor(e);
  if (cmd && APP_COMMANDS[cmd]) { e.preventDefault(); APP_COMMANDS[cmd](); return; }
  if (inEditable()) return;
  if (!selected.value) return;
  if (e.key === "Enter") { e.preventDefault(); openSession(selected.value); }
  else if (e.key === "F2") { e.preventDefault(); editSession(selected.value); }
  else if (e.key === "Delete") { e.preventDefault(); delSession(selected.value); }
}
import { Events } from "@wailsio/runtime";
import TerminalView from "./Terminal.vue";
import FileBrowser from "./FileBrowser.vue";
import VncCanvas from "./VncView.vue";
import RdpCanvas from "./RdpView.vue";
import SessionEditor from "./SessionEditor.vue";
import { applySkin } from "./themes";
import SettingsDialog from "./SettingsDialog.vue";
import SftpPanel from "./SftpPanel.vue";
import AskDialog from "./AskDialog.vue";
import { askConfirm, showInfo } from "./ask";
import TunnelsDialog from "./TunnelsDialog.vue";

const sessions = ref<Session[]>([]);
const folders = ref<Folder[]>([]);
const loading = ref(true);
const error = ref("");
const selected = ref<Session | null>(null);
const locked = ref(false);
const showUnlock = ref(true); // the unlock dialog is dismissable (Cancel); the sidebar reopens it
const master = ref("");
const unlockError = ref("");
const unlocking = ref(false);

// Recent sessions (per-viewer, localStorage): the ids most recently opened, newest first.
const recentIds = ref<number[]>(loadRecents());
function loadRecents(): number[] {
  try { return JSON.parse(localStorage.getItem("rxt-recent") || "[]"); } catch { return []; }
}
function pushRecent(id: number) {
  const r = [id, ...recentIds.value.filter((x) => x !== id)].slice(0, 9);
  recentIds.value = r;
  try { localStorage.setItem("rxt-recent", JSON.stringify(r)); } catch { /* private mode */ }
}
const recentSessions = computed(() =>
  recentIds.value.map((id) => sessions.value.find((s) => s.id === id)).filter((s): s is Session => !!s));

type Tab =
  | { id: string; title: string; kind: "ssh"; icon: string; connect: () => Promise<string>; ssh: boolean }
  | { id: string; title: string; kind: "files"; icon: string; connect: () => Promise<RemoteSession> }
  | { id: string; title: string; kind: "vnc"; icon: string; connect: () => Promise<VncView> }
  | { id: string; title: string; kind: "rdp"; icon: string; connect: (w: number, h: number) => Promise<RdpView> };
const tabs = shallowRef<Tab[]>([]);
const active = ref("");
let seq = 0;

async function loadData() {
  loading.value = true;
  error.value = "";
  try {
    [folders.value, sessions.value] = await Promise.all([Folders(), Sessions()]);
  } catch (e) {
    error.value = String(e);
  } finally {
    loading.value = false;
  }
}

async function openDemos() {
  try { if (await HasDemo()) openTerm("lab: ssh demo", "ssh", () => ConnectDemo(), true); } catch { /* none */ }
  try { if (await HasTelnetDemo()) openTerm("lab: telnet demo", "terminal", () => ConnectTelnetDemo()); } catch { /* none */ }
  try { if (await HasSerialDemo()) openTerm("lab: serial demo", "plug", () => ConnectSerialDemo()); } catch { /* none */ }
  try { if (await HasSftpDemo()) openFiles("lab: sftp demo", "sftp", () => ConnectSftpDemo()); } catch { /* none */ }
  try { if (await HasFtpDemo()) openFiles("lab: ftps demo", "ftps", () => ConnectFtpDemo()); } catch { /* none */ }
  try { if (await HasVncDemo()) openVnc("lab: vnc demo", () => ConnectVncDemo()); } catch { /* none */ }
  try { if (await HasRdpDemo()) openRdp("lab: rdp demo", (w, h) => ConnectRdpDemo(w, h)); } catch { /* none */ }
}

onMounted(async () => {
  Events.On("ssh:trust", (ev: { data: unknown }) => { trustQ.value = [...trustQ.value, ev.data as TrustReq]; });
  Events.On("ssh:password", (ev: { data: unknown }) => { passValue.value = ""; passQ.value = [...passQ.value, ev.data as PassReq]; });
  rootOpen.value = loadJSON("rxt-root-open", true);
  watch(rootOpen, (v) => saveJSON("rxt-root-open", v));
  await loadUI();
  window.addEventListener("click", (e) => { if (!(e.target as HTMLElement).closest(".menubar")) menu.value = ""; });
  window.addEventListener("keydown", onKey);
  try { locked.value = await Locked(); } catch { locked.value = true; }
  try { firstRun.value = locked.value && (await FirstRun()); } catch { /* older backend */ }
  await openDemos(); // lab targets from env vars: they need no saved sessions, so not gated on unlock
  if (locked.value) { loading.value = false; return; }
  await loadData();
});

// First start (no store yet): "Create master password", as the Rust store_ui (MIN_MASTER 8).
const firstRun = ref(false);
const confirmPw = ref("");
const useKeyring = ref(false); // Rust default: off
const MIN_MASTER = 8;
function strength(p: string) {
  const classes = [/[a-z]/, /[A-Z]/, /\d/, /[^\p{L}\p{N}]/u].filter((r) => r.test(p)).length;
  const n = [...p].length;
  if (n < MIN_MASTER) return "Too short";
  if (n >= 16 || (n >= 12 && classes >= 3)) return "Strong";
  return classes >= 2 ? "Fair" : "Weak";
}
const createProblem = computed(() =>
  [...master.value].length < MIN_MASTER ? `Use at least ${MIN_MASTER} characters.`
    : master.value !== confirmPw.value ? "The passwords do not match." : "");

async function doUnlock() {
  if (!master.value || unlocking.value) return;
  if (firstRun.value && createProblem.value) return;
  unlockError.value = "";
  unlocking.value = true;
  try {
    if (firstRun.value) {
      await CreateStore(master.value, useKeyring.value);
      firstRun.value = false;
      confirmPw.value = "";
    } else await Unlock(master.value);
    locked.value = false;
    master.value = "";
    await loadData();
  } catch (e) {
    unlockError.value = String(e);
  } finally {
    unlocking.value = false;
  }
}

// openTerm opens a terminal tab (SSH, Telnet or Serial — all stream term:out/term:close). monitor
// enables the remote-monitoring bar (SSH only).
// Per-tab connection state (reported by each view) and the session URL, for the tab's status dot and
// the status bar ("ssh://root@host:22 — Connected", Rust parity).
type TabState = "connecting" | "connected" | "stopped";
const tabState = ref<Record<string, TabState>>({});
const tabUrl = ref<Record<string, string>>({});
const STATE_LABEL: Record<TabState, string> = { connecting: "Connecting", connected: "Connected", stopped: "Disconnected" };
function sessionUrl(s: Session) {
  return `${s.protocol}://${s.user ? s.user + "@" : ""}${s.host}${s.port ? ":" + s.port : ""}`;
}
function newTab(url: string) {
  const id = "tab" + ++seq;
  tabState.value[id] = "connecting";
  if (url) tabUrl.value[id] = url;
  return id;
}
// ssh: a real SSH shell — it gets remote monitoring (on by default) and the side SFTP browser.
function openTerm(title: string, icon: string, connect: () => Promise<string>, ssh = false, url = "") {
  const id = newTab(url);
  if (ssh) tabMon.value[id] = cfg.value.remoteMonitoring; // Settings › SSH › Remote monitoring
  tabs.value = [...tabs.value, { id, title, kind: "ssh", icon, connect, ssh }];
  active.value = id;
}
// Toolbar (Rust app.rs TOOLBAR order); Exit is pinned right in the template.
const TOOLBAR: { icon: string; label: string; run: () => void; on?: () => boolean; disabled?: boolean; hint?: string }[] = [
  { icon: "session-new", label: "Session", hint: "New session (Ctrl+Shift+N)", run: () => newSession() },
  { icon: "sessions", label: "Sessions", run: () => (sidebarCollapsed.value = !sidebarCollapsed.value), on: () => !sidebarCollapsed.value },
  { icon: "tunneling", label: "Tunneling", run: () => (tunnelsOpen.value = true) },
  { icon: "multiexec", label: "MultiExec", hint: "MultiExec: type in several terminals at once", run: () => toggleMultiExec(), on: () => multiexec.value },
  { icon: "settings", label: "Settings", hint: "Settings (Ctrl+,)", run: () => (settingsOpen.value = true) },
  { icon: "help", label: "Help", run: () => aboutApp() },
];

// Quick connect: [ssh://|telnet://][user@]host[:port] opens a tab (the password is asked when needed).
const quick = ref("");
const quickErr = ref("");
function quickConnect() {
  const m = /^(?:(ssh|telnet):\/\/)?(?:([^@\s]+)@)?([^\s:@/]+)(?::(\d{1,5}))?$/.exec(quick.value.trim());
  if (!m) { quickErr.value = "Expected [ssh://|telnet://][user@]host[:port]"; return; }
  const [, proto = "ssh", user = "", host, p] = m;
  const port = p ? Number(p) : proto === "telnet" ? 23 : 22;
  const url = `${proto}://${user ? user + "@" : ""}${host}:${port}`;
  if (proto === "telnet") openTerm(host, "terminal", () => TelnetQuickConnect(host, port), false, url);
  else openTerm(user ? `${user}@${host}` : host, "ssh", () => QuickConnect(host, port, user, ""), true, url);
  quick.value = quickErr.value = "";
}

// MultiExec (Rust parity): the toggle shows a checkbox before each terminal tab; input typed in a
// checked terminal is sent to all checked terminals.
const multiexec = ref(false);
const tabMulti = ref<Record<string, boolean>>({});
function toggleMultiExec() {
  multiexec.value = !multiexec.value;
  void SetMultiExec(multiexec.value);
}
function selectMulti(id: string, on: boolean) {
  tabMulti.value[id] = on;
  const b = tabBackend.value[id];
  if (b) void SelectMultiExec(b, on);
}

// Duplicate tab: the same connection again, in a new tab.
function duplicateTab(t: Tab) {
  if (t.kind === "ssh") openTerm(t.title, t.icon, t.connect, t.ssh, tabUrl.value[t.id] ?? "");
}

const openLocal = () => openTerm("Local terminal", "terminal", () => LocalShell(), false, "local://");

// Side SFTP browser of the active SSH tab: its backend tabId, the folder OSC 7 reported, and a tick per
// Return (the panel then asks the shell's cwd when the shell does not send OSC 7).
const tabMon = ref<Record<string, boolean>>({});
const tabBackend = ref<Record<string, string>>({});
const tabCwd = ref<Record<string, string>>({});
const enterTick = ref<Record<string, number>>({});
const followFolder = ref(true);
const sidePage = ref<"sessions" | "sftp">("sessions");

// Layout persistence: the sidebar state goes back to config.toml (debounced), so it survives a restart.
let layoutLoaded = false;
let layoutTimer: ReturnType<typeof setTimeout> | undefined;
watch([sidebarCollapsed, sidePage, sideW], () => {
  if (!layoutLoaded) return;
  clearTimeout(layoutTimer);
  layoutTimer = setTimeout(async () => {
    try {
      const s = await GetSettings();
      s.sidebarOpen = !sidebarCollapsed.value;
      s.sidebarPage = sidePage.value;
      s.sidebarWidth = sideW.value;
      await SaveSettings(s);
    } catch { /* settings unavailable: keep the layout for this run */ }
  }, 500);
});

// Sidebar resize (Rust: 160 px .. half the window), by dragging its right edge.
function startResize(e: MouseEvent) {
  const x0 = e.clientX, w0 = sideW.value;
  const move = (m: MouseEvent) => {
    sideW.value = Math.round(Math.min(Math.max(160, window.innerWidth / 2), Math.max(160, w0 + m.clientX - x0)));
  };
  const up = () => { window.removeEventListener("mousemove", move); window.removeEventListener("mouseup", up); };
  window.addEventListener("mousemove", move);
  window.addEventListener("mouseup", up);
}
const activeSsh = computed(() => {
  const t = tabs.value.find((x) => x.id === active.value);
  return t?.kind === "ssh" && t.ssh ? t.id : "";
});
// The live SSH connection the SFTP browser shows; when there is none (disconnected, tab closed, a
// non-SSH tab or Home active) the sidebar goes back to Sessions.
const sftpTerm = computed(() =>
  activeSsh.value && tabState.value[activeSsh.value] !== "stopped" ? tabBackend.value[activeSsh.value] ?? "" : "");
watch(sftpTerm, (id) => { if (!id) sidePage.value = "sessions"; });
function onBackend(id: string, backend: string) {
  tabBackend.value[id] = backend;
  if (tabMulti.value[id]) void SelectMultiExec(backend, true); // a reconnect has a new backend id
  delete tabCwd.value[id];
  if (id === activeSsh.value && cfg.value.sftpAutoSwitch) sidePage.value = "sftp"; // Settings › SSH: SSH tabs only
}
function openFiles(title: string, icon: string, connect: () => Promise<RemoteSession>, url = "") {
  const id = newTab(url);
  tabs.value = [...tabs.value, { id, title, kind: "files", icon, connect }];
  active.value = id;
}
function openVnc(title: string, connect: () => Promise<VncView>, url = "") {
  const id = newTab(url);
  tabs.value = [...tabs.value, { id, title, kind: "vnc", icon: "vnc", connect }];
  active.value = id;
}
// Session editor (create/edit) and the tree context menu.
const editorOpen = ref(false);
const settingsOpen = ref(false);
const tunnelsOpen = ref(false);
const editorSessionId = ref<number | null>(null);
const ctx = ref<{ x: number; y: number; session?: Session; folder?: Folder; root?: boolean } | null>(null);

// SSH host-key trust prompt (TOFU): the backend emits "ssh:trust" for an unknown key and blocks.
// Prompts queue so concurrent connections (e.g. the SSH terminal and its SFTP pane) each get one.
type TrustReq = { reqId: string; host: string; port: number; algorithm: string; fingerprint: string; kind?: "tls"; changed?: boolean };
const trustQ = ref<TrustReq[]>([]);
const trustReq = computed(() => trustQ.value[0] ?? null);
function respondTrust(decision: "once" | "save" | "reject") {
  const r = trustQ.value[0];
  if (!r) return;
  void SshTrustRespond(r.reqId, decision);
  trustQ.value = trustQ.value.slice(1);
}

// Interactive SSH password prompt (sessions with no saved credential); also queued.
type PassReq = { reqId: string; host: string; user: string; prompt?: string; canSave?: boolean };
const passQ = ref<PassReq[]>([]);
const passReq = computed(() => passQ.value[0] ?? null);
const passValue = ref("");
const passSave = ref(false); // Rust "Save password": stored in the encrypted store after the login works
function respondPassword(ok: boolean) {
  const r = passQ.value[0];
  if (!r) return;
  void SshPasswordRespond(r.reqId, ok ? passValue.value : "", ok, ok && !!r.canSave && passSave.value);
  passQ.value = passQ.value.slice(1);
  passValue.value = "";
  passSave.value = false;
}

// New session (in a folder when started from the tree).
const editorFolder = ref<number | null>(null);
function newSession(folder: number | null = null) { editorSessionId.value = null; editorFolder.value = folder; editorOpen.value = true; }
// Import sessions…: MobaXterm.ini → preview dialog → import (Rust StoreDialog::Import).
const importPreview = ref<ImportPreview | null>(null);
const importing = ref(false);
async function importSessions() {
  if (locked.value) { unlockError.value = ""; showUnlock.value = true; return; }
  try { importPreview.value = await ImportMobaPick(); } catch (e) { void showInfo("Import MobaXterm.ini", `Cannot import: ${e}`); }
}
// rustXterm TOML (Rust Action::ImportToml / ExportToml + StoreDialog::Export confirmation).
const exportPlan = ref<ExportPlan | null>(null);
async function importToml() {
  try { const msg = await ImportTOMLPick(); if (msg) { status.value = msg; await loadData(); } }
  catch (e) { void showInfo("Notice", String(e)); }
}
async function exportToml() {
  try { exportPlan.value = await ExportTOMLPick(); } catch (e) { void showInfo("Notice", String(e)); }
}
async function confirmExport() {
  const p = exportPlan.value;
  exportPlan.value = null;
  if (!p) return;
  try { await ExportTOMLWrite(p.path); status.value = `Exported ${p.sessions} session(s) to ${p.path}.`; }
  catch (e) { status.value = `Could not export to ${p.path}: ${e}`; }
}
async function confirmImport() {
  const p = importPreview.value;
  if (!p || importing.value) return;
  importing.value = true;
  try {
    const n = await ImportMobaApply(p.path);
    importPreview.value = null;
    status.value = `Imported ${n} session(s).`;
    await loadData();
  } catch (e) {
    void showInfo("Import MobaXterm.ini", `Cannot import: ${e}`);
  } finally {
    importing.value = false;
  }
}
function editSession(s: Session) { editorSessionId.value = s.id; editorOpen.value = true; }
// Rust: OK on a new session saves and connects it; an edited session is only saved.
async function onEditorSaved(id: number) {
  const isNew = editorSessionId.value === null;
  editorOpen.value = false;
  await loadData();
  const s = isNew ? sessions.value.find((x) => x.id === id) : undefined;
  if (s) openSession(s);
}
async function delSession(s: Session) {
  if (!(await askConfirm("Delete session", `Delete the saved session "${s.name}"? Open tabs stay open.`, "Delete"))) return;
  try { await DeleteSession(s.id); if (selected.value?.id === s.id) selected.value = null; await loadData(); }
  catch (e) { error.value = String(e); }
}
async function dupSession(s: Session) {
  try { await DuplicateSession(s.id); await loadData(); } catch (e) { error.value = String(e); }
}
// New / Edit folder dialog (Rust Modal::FolderName: name + icon grid).
const folderDlg = ref<{ title: string; id: number; parent: number | null; name: string; icon: string } | null>(null);
function newFolder(parent: number | null) { folderDlg.value = { title: "New folder", id: 0, parent, name: "", icon: "folder" }; }
function editFolder(f: Folder) { folderDlg.value = { title: "Edit folder", id: f.id, parent: f.parent, name: f.name, icon: f.icon || "folder" }; }
async function saveFolderDlg(name: string, icon: string) {
  const d = folderDlg.value;
  folderDlg.value = null;
  if (!d) return;
  try {
    const id = await SaveFolder(d.id, name, d.parent);
    await SetFolderIcon(id, icon);
    await loadData();
  } catch (e) { error.value = String(e); }
}
async function setFolderColor(f: Folder, icon: string) {
  try { await SetFolderIcon(f.id, icon); await loadData(); } catch (e) { error.value = String(e); }
}
async function moveToRoot(s: Session) {
  try { await MoveSession(s.id, null); await loadData(); } catch (e) { error.value = String(e); }
}
// Rust icons::FOLDER_COLORS for "Change color" (label = the colour, "folder" = Yellow).
const FOLDER_COLORS = ["folder", "folder-blue", "folder-green", "folder-gray", "folder-red", "folder-purple", "folder-silver",
  "folder-steel", "folder-orange", "folder-pink", "folder-teal", "folder-cyan", "folder-brown", "folder-lime", "folder-navy", "folder-black"];
const colorLabel = (n: string) => (n === "folder" ? "Yellow" : n[7].toUpperCase() + n.slice(8));
const colorsOpen = ref(false);
async function delFolder(f: Folder) {
  if (!(await askConfirm("Delete folder", "Delete this folder? Its sessions move to the parent folder.", "Delete"))) return;
  try { await DeleteFolder(f.id); await loadData(); } catch (e) { error.value = String(e); }
}
const ctxPos = (e: MouseEvent) => ({ x: Math.min(e.clientX, window.innerWidth - 230), y: Math.min(e.clientY, window.innerHeight - 270) });
function openCtx(e: MouseEvent, s: Session) { selected.value = s; ctx.value = { ...ctxPos(e), session: s }; }
function openFolderCtx(e: MouseEvent, f: Folder) { ctx.value = { ...ctxPos(e), folder: f }; }
// Right click on the tree's empty area: the root folder menu (Rust: the rows' "rest" area).
function onTreeContext(e: MouseEvent) {
  if ((e.target as HTMLElement).closest(".treerow") || locked.value) return;
  ctx.value = { ...ctxPos(e), root: true };
}
function closeCtx() { ctx.value = null; colorsOpen.value = false; }

// openSession opens a session in the tab its protocol needs (double-click / Enter).
function openSession(s: Session) {
  pushRecent(s.id);
  switch (s.protocol) {
    case "telnet": openTerm(s.name, "terminal", () => TelnetConnect(s.id), false, sessionUrl(s)); break;
    case "serial": openTerm(s.name, "plug", () => SerialConnect(s.id), false, "serial://" + s.host); break;
    case "wsl": openTerm(s.name, "tux", () => WslConnect(s.id), false, `wsl://${s.user ? s.user + "@" : ""}${s.host || "Default"}`); break;
    case "rdp": openRdpFor(s); break;
    case "vnc": openVncFor(s); break;
    case "sftp": openSftpFor(s); break;
    case "ftp": case "ftps": openFtpFor(s); break;
    default: openTerm(s.name, "ssh", () => Connect(s.id), true, sessionUrl(s)); break; // ssh
  }
}
function openSftpFor(s: Session | null) { if (s) openFiles("SFTP: " + s.name, "sftp", () => SftpConnect(s.id), sessionUrl({ ...s, protocol: "sftp" })); }
function openFtpFor(s: Session | null) { if (s) openFiles("FTP: " + s.name, "ftp", () => FtpConnect(s.id, "explicit"), sessionUrl(s)); }
function openVncFor(s: Session | null) { if (s) openVnc("VNC: " + s.name, () => VncConnect(s.id), sessionUrl(s)); }
function openRdp(title: string, connect: (w: number, h: number) => Promise<RdpView>, url = "") {
  const id = newTab(url);
  tabs.value = [...tabs.value, { id, title, kind: "rdp", icon: "rdp", connect }];
  active.value = id;
}
function openRdpFor(s: Session | null) { if (s) openRdp("RDP: " + s.name, (w, h) => RdpConnect(s.id, w, h), sessionUrl(s)); }
// Close from the UI: a still-connected tab asks first (Settings › General › Confirm before closing).
async function requestClose(id: string) {
  const t = tabs.value.find((x) => x.id === id);
  if (t && cfg.value.confirmClose && tabState.value[id] === "connected" &&
      !(await askConfirm("Close tab", `"${t.title}" is still connected. Close it and disconnect?`, "Close"))) return;
  closeTab(id);
}
async function requestExit() {
  if (cfg.value.confirmClose && tabs.value.some((t) => tabState.value[t.id] === "connected") &&
      !(await askConfirm("Exit", "Some tabs are still connected. Exit and disconnect them?", "Exit"))) return;
  void Quit();
}
function closeTab(id: string) {
  for (const m of [tabState, tabUrl, tabMon, tabBackend, tabCwd, enterTick, tabMulti]) delete m.value[id];
  const i = tabs.value.findIndex((t) => t.id === id);
  tabs.value = tabs.value.filter((t) => t.id !== id);
  if (active.value === id) active.value = tabs.value[Math.max(0, i - 1)]?.id ?? "";
}

type Row =
  | { kind: "folder"; folder: Folder; depth: number; open: boolean; hasKids: boolean }
  | { kind: "session"; session: Session; depth: number };

// Collapsed folders (by id); toggled by the tree chevron. A new Set keeps the computed reactive.
// Folder open/closed state survives a restart (Rust keeps it in persisted egui memory).
const collapsed = ref<Set<number>>(new Set(loadJSON<number[]>("rxt-collapsed", [])));
function loadJSON<T>(key: string, def: T): T {
  try { const v = localStorage.getItem(key); return v ? (JSON.parse(v) as T) : def; } catch { return def; }
}
function saveJSON(key: string, v: unknown) { try { localStorage.setItem(key, JSON.stringify(v)); } catch { /* private mode */ } }
watch(collapsed, (s) => saveJSON("rxt-collapsed", [...s]));
function toggleFolder(id: number) {
  const next = new Set(collapsed.value);
  if (!next.delete(id)) next.add(id);
  collapsed.value = next;
}

const rows = computed<Row[]>(() => {
  const out: Row[] = [];
  const byParent = new Map<number | null, Folder[]>();
  for (const f of folders.value) {
    const k = f.parent ?? null;
    (byParent.get(k) ?? byParent.set(k, []).get(k)!).push(f);
  }
  const ses = (fid: number | null) =>
    sessions.value.filter((s) => (s.folder ?? null) === fid).sort((a, b) => a.name.localeCompare(b.name));
  // Rust sidebar folder_contents: each level lists its folders, then its sessions.
  const walk = (parent: number | null, depth: number) => {
    for (const f of (byParent.get(parent) ?? []).sort((a, b) => a.name.localeCompare(b.name))) {
      const open = !collapsed.value.has(f.id);
      out.push({ kind: "folder", folder: f, depth, open, hasKids: true });
      if (open) walk(f.id, depth + 1);
    }
    for (const s of ses(parent)) out.push({ kind: "session", session: s, depth });
  };
  walk(null, 0);
  return out;
});

const protoIcon: Record<string, string> = {
  ssh: "ssh", sftp: "sftp", ftp: "ftp", ftps: "ftps", rdp: "rdp", vnc: "vnc",
  telnet: "terminal", serial: "plug", wsl: "tux", local: "terminal",
};
const icon = (name: string) => `./icons/${name}.png`;
const sessionIcon = (s: Session) => icon(s.icon || protoIcon[s.protocol] || "session");
const folderIcon = (f: Folder) => icon(f.icon || "folder");
const tabIcon = (t: Tab) => icon(t.icon);
</script>

<template>
  <div class="app flex h-full flex-col">
    <div v-if="locked && showUnlock" class="modal-backdrop">
      <div class="dialog">
        <div class="dialog-title">{{ firstRun ? "Create master password" : "Unlock sessions" }}</div>
        <div class="dialog-body">
          <template v-if="firstRun">
            <p class="dialog-sub">Saved sessions, credentials and trust records are stored in an encrypted database.</p>
            <p class="dialog-sub dialog-warn">If you forget the master password, the saved data cannot be recovered.</p>
          </template>
          <p v-else class="dialog-sub">Enter the master password to open saved sessions.</p>
          <label class="dialog-label">Master password</label>
          <input
            type="password"
            class="dialog-input"
            v-model="master"
            @keyup.enter="doUnlock"
            v-focus
          />
          <template v-if="firstRun">
            <div v-if="master" class="dialog-hint">Strength: {{ strength(master) }}</div>
            <label class="dialog-label mt-1">Confirm master password</label>
            <input type="password" class="dialog-input" v-model="confirmPw" @keyup.enter="doUnlock" />
            <label class="dialog-check"><input v-model="useKeyring" type="checkbox" /> Unlock with system keyring</label>
            <div v-if="master && confirmPw && createProblem" class="dialog-err">{{ createProblem }}</div>
          </template>
          <div v-if="unlockError" class="dialog-err">{{ unlockError }}</div>
          <div class="dialog-actions">
            <button class="dbtn primary" :disabled="!master || unlocking || (firstRun && !!createProblem)" @click="doUnlock">
              <img :src="icon('ok')" class="dbtn-ico" /> {{ unlocking ? "Working..." : firstRun ? "Create" : "Unlock" }}
            </button>
            <button class="dbtn" @click="showUnlock = false"><img :src="icon('cancel')" class="dbtn-ico" /> {{ firstRun ? "Later" : "Cancel" }}</button>
          </div>
        </div>
      </div>
    </div>
    <nav class="menubar flex items-stretch">
      <div class="menu">
        <button class="menu-t" :class="menu === 'terminal' ? 'on' : ''" @click.stop="toggleMenu('terminal')">Terminal</button>
        <div v-if="menu === 'terminal'" class="menu-d">
          <button class="menu-i" @click="runMenu(() => newSession())"><img :src="icon('session-new')" class="menu-ico" /> New session</button>
          <button class="menu-i" @click="runMenu(openLocal)"><img :src="icon('terminal')" class="menu-ico" /> Local terminal</button>
          <div class="menu-sep"></div>
          <button class="menu-i" @click="runMenu(requestExit)"><img :src="icon('exit')" class="menu-ico" /> Exit</button>
        </div>
      </div>
      <div class="menu">
        <button class="menu-t" :class="menu === 'sessions' ? 'on' : ''" @click.stop="toggleMenu('sessions')">Sessions</button>
        <div v-if="menu === 'sessions'" class="menu-d">
          <button class="menu-i" @click="runMenu(() => newSession())"><img :src="icon('session-new')" class="menu-ico" /> New session</button>
          <button class="menu-i" @click="runMenu(() => newFolder(null))"><img :src="icon('folder')" class="menu-ico" /> New folder</button>
          <button class="menu-i" @click="runMenu(() => importSessions())"><img :src="icon('folder')" class="menu-ico" /> Import sessions</button>
        </div>
      </div>
      <div class="menu">
        <button class="menu-t" :class="menu === 'view' ? 'on' : ''" @click.stop="toggleMenu('view')">View</button>
        <div v-if="menu === 'view'" class="menu-d">
          <button class="menu-i" @click="runMenu(() => sidebarCollapsed = !sidebarCollapsed)"><img :src="icon('layout-sidebar')" class="menu-ico" /> {{ sidebarCollapsed ? "Show" : "Hide" }} sidebar</button>
        </div>
      </div>
      <div class="menu">
        <button class="menu-t" :class="menu === 'tools' ? 'on' : ''" @click.stop="toggleMenu('tools')">Tools</button>
        <div v-if="menu === 'tools'" class="menu-d">
          <button class="menu-i" @click="runMenu(() => tunnelsOpen = true)"><img :src="icon('tunnel')" class="menu-ico" /> SSH tunnels…</button>
        </div>
      </div>
      <div class="menu">
        <button class="menu-t" :class="menu === 'settings' ? 'on' : ''" @click.stop="toggleMenu('settings')">Settings</button>
        <div v-if="menu === 'settings'" class="menu-d">
          <button class="menu-i" @click="runMenu(() => settingsOpen = true)"><img :src="icon('gear')" class="menu-ico" /> Settings…</button>
        </div>
      </div>
      <div class="menu">
        <button class="menu-t" :class="menu === 'help' ? 'on' : ''" @click.stop="toggleMenu('help')">Help</button>
        <div v-if="menu === 'help'" class="menu-d">
          <button class="menu-i" @click="runMenu(() => aboutApp())"><img :src="icon('help')" class="menu-ico" /> About rustXterm</button>
        </div>
      </div>
    </nav>
    <!-- toolbar (Rust app.rs TOOLBAR): 52 px with captions, 34 px without; Exit pinned right -->
    <header class="toolbar" :class="ui.toolbarCaptions ? 'cap' : ''">
      <button v-for="b in TOOLBAR" :key="b.label" class="tbtn" :class="b.on?.() ? 'on' : ''" :disabled="b.disabled" :title="b.hint ?? b.label" @click="b.run()">
        <img :src="icon(b.icon)" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">{{ b.label }}</span>
      </button>
      <span class="ml-auto"></span>
      <button class="tbtn" title="Exit" @click="requestExit"><img :src="icon('exit')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">Exit</span></button>
    </header>

    <!-- 28 px row: Quick connect over the sidebar column, then the tab strip -->
    <div class="row">
      <div class="qc-col" :style="{ width: (sidebarCollapsed ? sideW - STRIP_W : sideW) + 'px' }">
        <input ref="quickInput" v-model="quick" class="qc" placeholder="Quick connect..." spellcheck="false" aria-label="Quick connect" :class="quickErr ? 'err' : ''" :title="quickErr" @keydown.enter="quickConnect" @keydown.esc="quick = ''; quickErr = ''" />
      </div>
      <div class="tabstrip">
        <button class="tab-home" :class="!active ? 'sel' : ''" title="Home" @click="active = ''"><img :src="icon('house')" /></button>
        <template v-for="(t, i) in tabs" :key="t.id">
        <input v-if="multiexec && t.kind === 'ssh'" type="checkbox" class="me-check" :checked="!!tabMulti[t.id]" :title="'MultiExec ' + t.title" @change="selectMulti(t.id, ($event.target as HTMLInputElement).checked)" />
        <button class="tab" :class="t.id === active ? 'sel' : ''" :title="tabUrl[t.id] || t.title" @click="active = t.id" @mousedown.middle.prevent="requestClose(t.id)">
          <img :src="tabIcon(t)" class="tab-ico" />
          <span class="tab-dot" :class="tabState[t.id] ?? 'connecting'" :title="STATE_LABEL[tabState[t.id] ?? 'connecting']"></span>
          <span class="tab-t"><template v-if="ui.showTabNumbers">{{ i + 1 }}. </template>{{ t.title }}</span>
          <img v-if="multiexec && tabMulti[t.id]" :src="icon('multiexec')" class="tab-ico" title="MultiExec" />
          <span v-if="ui.showCloseButtons" class="tab-x" title="Close" @click.stop="requestClose(t.id)">×</span>
        </button>
        </template>
        <button class="tab-new" title="New local terminal" @click="openLocal"></button>
      </div>
    </div>

    <div class="flex min-h-0 flex-1">
      <!-- sidebar: 34 px icon strip (40 px buttons) + the page; a click on the open page collapses it -->
      <aside class="sidebar" :style="{ width: (sidebarCollapsed ? STRIP_W : sideW) + 'px' }">
        <nav class="strip">
          <button
            v-for="p in (['sessions', 'sftp'] as const)"
            :key="p"
            class="strip-b"
            :class="!sidebarCollapsed && sidePage === p ? 'on' : ''"
            :title="p === 'sessions' ? 'Sessions' : 'SFTP'"
            @click="sidebarCollapsed = !sidebarCollapsed && sidePage === p; sidePage = p"
          >
            <img :src="icon(p === 'sessions' ? 'star' : 'sftp')" class="strip-ico" />
          </button>
          <div class="strip-rest"></div>
        </nav>
        <div v-show="!sidebarCollapsed" class="page">
          <SftpPanel
            v-if="sidePage === 'sftp'"
            v-model:follow="followFolder"
            :term-id="sftpTerm"
            :cwd="tabCwd[activeSsh] ?? ''"
            :enter-tick="enterTick[activeSsh] ?? 0"
            :monitor="!!tabMon[activeSsh]"
            @toggle-monitor="tabMon[activeSsh] = !tabMon[activeSsh]"
          />
          <div v-else class="tree" @contextmenu.prevent="onTreeContext">
            <div class="treerow root" :style="{ paddingLeft: '4px' }" @click="rootOpen = !rootOpen" @contextmenu.prevent="!locked && (ctx = { x: $event.clientX, y: $event.clientY, root: true })">
              <span class="tw"><i class="tri" :class="rootOpen ? 'open' : ''"></i></span>
              <img :src="icon('folder-user')" class="ico" />
              <span class="truncate">User sessions</span>
            </div>
            <template v-if="rootOpen">
              <div v-if="locked" class="locked-note">
                <div>Saved sessions are locked.</div>
                <button class="locked-unlock" @click="unlockError = ''; showUnlock = true">{{ firstRun ? "Create master password…" : "Unlock..." }}</button>
              </div>
              <div v-else-if="loading" class="side-note">Loading…</div>
              <div v-else-if="error" class="side-err">{{ error }}</div>
              <div v-else-if="!rows.length" class="side-note tree-hint">No sessions yet. Create one with New session or import sessions.</div>
              <template v-else>
                <div v-for="(row, i) in rows" :key="i">
                  <div
                    v-if="row.kind === 'folder'"
                    class="treerow"
                    :style="{ paddingLeft: 4 + (row.depth + 1) * INDENT + 'px' }"
                    @click="toggleFolder(row.folder.id)"
                    @contextmenu.prevent="openFolderCtx($event, row.folder)"
                  >
                    <span class="tw"><i class="tri" :class="row.open ? 'open' : ''"></i></span>
                    <img :src="folderIcon(row.folder)" class="ico" />
                    <span class="truncate">{{ row.folder.name }}</span>
                  </div>
                  <div
                    v-else
                    class="treerow session"
                    :class="selected?.id === row.session.id ? 'sel' : ''"
                    :style="{ paddingLeft: 4 + (row.depth + 1) * INDENT + 'px' }"
                    :title="row.session.host ? `${row.session.name} (${row.session.host})` : row.session.name"
                    @click="selected = row.session"
                    @dblclick="openSession(row.session)"
                    @contextmenu.prevent="openCtx($event, row.session)"
                  >
                    <span class="tw"></span>
                    <img :src="sessionIcon(row.session)" class="ico" />
                    <span class="truncate">{{ row.session.name }}</span>
                  </div>
                </div>
              </template>
            </template>
          </div>
        </div>
      </aside>
      <div v-show="!sidebarCollapsed" class="side-grip" title="Drag to resize the sidebar" @mousedown.prevent="startResize"></div>

      <main class="central">
        <div class="relative min-h-0 flex-1">
          <template v-for="t in tabs" :key="t.id">
            <TerminalView
              v-if="t.kind === 'ssh'"
              v-show="t.id === active"
              :connect="t.connect"
              :monitor="!!tabMon[t.id]"
              :title="t.title"
              @close="requestClose(t.id)"
              @monitor-off="tabMon[t.id] = false"
              @duplicate="duplicateTab(t)"
              @state="(st) => (tabState[t.id] = st)"
              @backend="(b) => onBackend(t.id, b)"
              @cwd="(d) => (tabCwd[t.id] = d)"
              @enter="enterTick[t.id] = (enterTick[t.id] ?? 0) + 1"
              class="absolute inset-0"
            />
            <FileBrowser v-else-if="t.kind === 'files'" v-show="t.id === active" :connect="t.connect" @state="(st) => (tabState[t.id] = st)" class="absolute inset-0" />
            <VncCanvas v-else-if="t.kind === 'vnc'" v-show="t.id === active" :connect="t.connect" @state="(st) => (tabState[t.id] = st)" class="absolute inset-0" />
            <RdpCanvas v-else v-show="t.id === active" :connect="t.connect" @close="closeTab(t.id)" @state="(st) => (tabState[t.id] = st)" class="absolute inset-0" />
          </template>
          <!-- Home (Rust home.rs): gear top-right, logo 64 + title 32 px at 22 % height, 200x30 buttons, recents -->
          <div v-if="!active" class="home">
            <button class="home-gear" title="Settings" @click="settingsOpen = true"><img :src="icon('settings')" /></button>
            <div class="home-brand"><img :src="icon('logo')" class="home-logo" /><span>rustXterm</span></div>
            <div class="home-actions">
              <button class="home-btn" @click="newSession()"><img :src="icon('session-new')" class="home-btn-ico" /> New session</button>
              <button class="home-btn" @click="importSessions"><img :src="icon('open')" class="home-btn-ico" /> Import sessions</button>
            </div>
            <div v-if="recentSessions.length" class="recent">
              <div class="recent-title">Recent sessions</div>
              <div class="recent-grid">
                <div v-for="s in recentSessions" :key="s.id" class="recent-item" @click="openSession(s)" :title="s.host ? `${s.name} (${s.host})` : s.name">
                  <img :src="sessionIcon(s)" class="recent-ico" /><span class="truncate">{{ s.name }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </main>
    </div>

    <footer class="statusbar">
      <span class="truncate">{{ active && tabUrl[active] ? `${tabUrl[active]} — ${STATE_LABEL[tabState[active] ?? "connecting"]}` : status }}</span>
      <span v-if="multiexec" class="ml-auto shrink-0 me-on">MultiExec ON</span>
      <span class="shrink-0" :class="multiexec ? '' : 'ml-auto'">{{ tabs.length }} tab(s)</span>
    </footer>

    <!-- tree context menu -->
    <div v-if="ctx" class="ctx-mask" @click="closeCtx" @contextmenu.prevent="closeCtx">
      <div class="ctx" :style="{ left: ctx.x + 'px', top: ctx.y + 'px' }" @click.stop>
        <!-- Rust sidebar menus: session_row / folder_menu -->
        <template v-if="ctx.session">
          <button class="ctx-item" @click="openSession(ctx!.session!); closeCtx()"><img :src="icon('play')" class="ctx-ico" /> Connect<span class="ctx-sc">Enter</span></button>
          <button class="ctx-item" @click="editSession(ctx!.session!); closeCtx()"><img :src="icon('edit')" class="ctx-ico" /> Edit session</button>
          <button class="ctx-item" @click="dupSession(ctx!.session!); closeCtx()"><img :src="icon('copy')" class="ctx-ico" /> Duplicate session</button>
          <button class="ctx-item" @click="editSession(ctx!.session!); closeCtx()"><img :src="icon('rename')" class="ctx-ico" /> Rename session<span class="ctx-sc">F2</span></button>
          <button class="ctx-item" @click="delSession(ctx!.session!); closeCtx()"><img :src="icon('delete')" class="ctx-ico" /> Delete session<span class="ctx-sc">Del</span></button>
          <div class="ctx-sep"></div>
          <button class="ctx-item" @click="newSession(ctx!.session!.folder); closeCtx()"><img :src="icon('session-new')" class="ctx-ico" /> New session here</button>
          <button class="ctx-item" @click="newFolder(ctx!.session!.folder); closeCtx()"><img :src="icon('new-folder')" class="ctx-ico" /> New folder here</button>
          <button v-if="ctx.session.folder !== null" class="ctx-item" @click="moveToRoot(ctx!.session!); closeCtx()"><img :src="icon('folder-up')" class="ctx-ico" /> Move to root</button>
        </template>
        <template v-else>
          <button class="ctx-item" @click="newSession(ctx!.folder?.id ?? null); closeCtx()"><img :src="icon('session-new')" class="ctx-ico" /> New session</button>
          <button class="ctx-item" @click="newFolder(ctx!.folder?.id ?? null); closeCtx()"><img :src="icon('new-folder')" class="ctx-ico" /> New folder</button>
          <button class="ctx-item" @click="closeCtx(); importSessions()"><img :src="icon('download')" class="ctx-ico" /> Import sessions…</button>
          <template v-if="ctx.folder">
            <div class="ctx-sep"></div>
            <button class="ctx-item" @click="editFolder(ctx!.folder!); closeCtx()"><img :src="icon('edit')" class="ctx-ico" /> Edit folder…<span class="ctx-sc">F2</span></button>
            <div class="ctx-sub" @mouseenter="colorsOpen = true" @mouseleave="colorsOpen = false">
              <button class="ctx-item" :class="colorsOpen ? 'on' : ''" @click="colorsOpen = !colorsOpen"><span class="ctx-ico"></span> Change color<span class="ctx-arrow"></span></button>
              <div v-if="colorsOpen" class="ctx ctx-colors">
                <button v-for="c in FOLDER_COLORS" :key="c" class="ctx-item" @click="setFolderColor(ctx!.folder!, c); closeCtx()"><img :src="icon(c)" class="ctx-ico" /> {{ colorLabel(c) }}</button>
              </div>
            </div>
            <button class="ctx-item" @click="delFolder(ctx!.folder!); closeCtx()"><img :src="icon('delete')" class="ctx-ico" /> Delete folder</button>
          </template>
        </template>
      </div>
    </div>

    <!-- Export sessions (Rust StoreDialog::Export) -->
    <div v-if="exportPlan" class="modal-backdrop" style="z-index: 88">
      <div class="dialog">
        <div class="dialog-title">Export sessions</div>
        <div class="dialog-body">
          <p class="dialog-sub">Write {{ exportPlan.sessions }} session(s), {{ exportPlan.folders }} folder(s) and {{ exportPlan.tunnels }} tunnel(s) to {{ exportPlan.path }}?</p>
          <p class="dialog-sub">Passwords, passphrases and trust records are not exported.</p>
          <div class="dialog-actions">
            <button class="dbtn primary" @click="confirmExport"><img :src="icon('ok')" class="dbtn-ico" /> Export</button>
            <button class="dbtn" @click="exportPlan = null"><img :src="icon('cancel')" class="dbtn-ico" /> Cancel</button>
          </div>
        </div>
      </div>
    </div>
    <ImportDialog v-if="importPreview" :preview="importPreview" :busy="importing" @ok="confirmImport" @cancel="importPreview = null" />
    <FolderDialog v-if="folderDlg" :title="folderDlg.title" :name="folderDlg.name" :icon="folderDlg.icon" @ok="saveFolderDlg" @cancel="folderDlg = null" />

    <!-- session editor -->
    <SessionEditor
      v-if="editorOpen"
      :sessionId="editorSessionId"
      :initial-folder="editorFolder"
      :folders="folders"
      @saved="onEditorSaved"
      @close="editorOpen = false"
    />

    <AskDialog />

    <!-- global settings -->
    <SettingsDialog v-if="settingsOpen" @saved="loadUI()" @close="settingsOpen = false"
      @import="settingsOpen = false; importSessions()" @import-toml="importToml()" @export-toml="exportToml()" @unlock="settingsOpen = false; unlockError = ''; showUnlock = true" />
    <TunnelsDialog v-if="tunnelsOpen" :sessions="sessions" @close="tunnelsOpen = false" />

    <!-- SSH host-key trust (trust-on-first-use) -->
    <div v-if="trustReq" class="modal-backdrop" style="z-index: 65">
      <div class="dialog" style="width: 470px">
        <div class="dialog-title">{{ trustReq.kind === "tls" ? (trustReq.changed ? "Certificate changed" : "Untrusted certificate") : "Unknown host key" }}</div>
        <div class="dialog-body">
          <p v-if="trustReq.kind === 'tls' && trustReq.changed" class="dialog-sub warn-text">The certificate of <b>{{ trustReq.host }}:{{ trustReq.port }}</b> differs from the one saved before. This happens after a certificate renewal, or when someone intercepts the connection.</p>
          <p v-else-if="trustReq.kind === 'tls'" class="dialog-sub">The TLS certificate of <b>{{ trustReq.host }}:{{ trustReq.port }}</b> is not trusted by this system. No credentials are sent until you accept.</p>
          <p v-else class="dialog-sub">The SSH host key of <b>{{ trustReq.host }}:{{ trustReq.port }}</b> is not known. No credentials are sent until you accept.</p>
          <div class="fp-head"><img :src="icon(trustReq.kind === 'tls' ? 'lock' : 'key')" class="dbtn-ico" /> {{ trustReq.kind === "tls" ? `Certificate "${trustReq.algorithm}" SHA-256 fingerprint` : `${trustReq.algorithm} key fingerprint` }}</div>
          <div class="fp">{{ trustReq.fingerprint }}</div>
          <p class="dialog-sub">Accept only if it matches the fingerprint published by the server administrator.</p>
          <div class="dialog-actions">
            <button class="dbtn primary" @click="respondTrust('once')"><img :src="icon('ok')" class="dbtn-ico" /> Accept once</button>
            <button class="dbtn" @click="respondTrust('save')">Accept and save</button>
            <button class="dbtn" @click="respondTrust('reject')"><img :src="icon('cancel')" class="dbtn-ico" /> Cancel</button>
          </div>
        </div>
      </div>
    </div>

    <!-- interactive SSH password (sessions without a saved credential) -->
    <div v-if="passReq" class="modal-backdrop" style="z-index: 66">
      <!-- Enter confirms from the field or the "Save password" box, Escape cancels -->
      <div class="dialog" style="width: 400px" @keydown.enter.prevent="respondPassword(true)" @keydown.esc="respondPassword(false)">
        <div class="dialog-title">Password</div>
        <div class="dialog-body">
          <p class="dialog-sub" v-if="passReq.prompt">{{ passReq.prompt }}.</p>
          <p class="dialog-sub" v-else>Enter the password for <b>{{ (passReq.user || "user") + "@" + passReq.host }}</b>.</p>
          <label class="dialog-label">Password</label>
          <input type="password" class="dialog-input" v-model="passValue" v-focus />
          <label v-if="passReq.canSave" class="dialog-check"><input v-model="passSave" type="checkbox" /> Save password</label>
          <div class="dialog-actions">
            <button class="dbtn primary" @click="respondPassword(true)"><img :src="icon('ok')" class="dbtn-ico" /> OK</button>
            <button class="dbtn" @click="respondPassword(false)"><img :src="icon('cancel')" class="dbtn-ico" /> Cancel</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Shell — Rust Metrics/Tokens: menu 20 px, toolbar 52 (34 compact), row 28, status 18, body 12 px,
   captions 11 px, flat square widgets, 1 px borders. */
.app { background: var(--surface); color: var(--text); font-size: 12px; }
.menubar { height: 20px; background: var(--toolbar); color: var(--text); user-select: none; }
.menu { position: relative; }
.menu-t { height: 20px; padding: 0 6px; background: transparent; border: none; cursor: pointer; color: inherit; font-size: 12px; }
.menu-t:hover, .menu-t.on { background: var(--hover); }
.menu-d { position: absolute; top: 100%; left: 0; z-index: 70; min-width: 190px; background: var(--panel); border: 1px solid var(--border); padding: 2px; display: flex; flex-direction: column; }
.menu-i { display: flex; align-items: center; gap: 8px; height: 22px; padding: 0 8px; background: transparent; border: none; cursor: pointer; text-align: left; color: var(--text); font-size: 12px; }
.menu-i:hover { background: var(--hover); }
.menu-ico { height: 16px; width: 16px; }
.menu-sep { height: 1px; margin: 3px 0; background: var(--border-subtle); }

.toolbar { display: flex; align-items: center; gap: 4px; height: 52px; padding: 0 4px; background: var(--toolbar); border-bottom: 1px solid var(--border-subtle); }
.toolbar:not(.cap) { height: 34px; }
.tbtn { display: flex; flex-direction: column; align-items: center; justify-content: flex-start; min-width: 56px; height: 48px; padding: 4px 6px 2px; background: transparent; border: none; cursor: pointer; color: var(--text); }
.toolbar:not(.cap) .tbtn { min-width: 30px; width: 30px; height: 30px; justify-content: center; padding: 0; }
.tbtn:hover:not(:disabled) { background: var(--hover); }
.tbtn.on { background: var(--pressed); }
.tbtn:disabled { cursor: default; }
.tbtn:disabled .tbico { opacity: .45; }
.tbtn:disabled .tbcap { color: var(--text-disabled); }
.tbico { width: 24px; height: 24px; flex: none; }
.tbcap { margin-top: auto; font-size: 11px; line-height: 14px; white-space: nowrap; }

.row { display: flex; height: 28px; background: var(--toolbar); border-bottom: 1px solid var(--border); position: relative; z-index: 1; }
.qc-col { flex: none; display: flex; align-items: center; padding: 0 2px; }
.qc { width: 100%; height: 26px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.qc:focus { border-color: var(--focus); }
.qc.err { border-color: var(--danger); }
.qc::placeholder { color: var(--text-muted); }
.tabstrip { flex: 1; min-width: 0; display: flex; align-items: flex-end; gap: 2px; padding-left: 2px; overflow: hidden; }
/* Same pattern as TabBar.vue: muted inactive tabs, hover tint, the active tab bold on the work-area colour */
.tab, .tab-home, .tab-new { height: 26px; flex: none; display: flex; align-items: center; background: var(--tab-inactive); border: 1px solid var(--border-subtle); border-bottom-color: var(--border); border-radius: var(--ctl-radius) var(--ctl-radius) 0 0 !important; cursor: pointer; color: var(--text-muted); font-size: 12px; position: relative; transition: background-color 0.12s ease, color 0.12s ease; }
.tab:hover:not(.sel), .tab-home:hover:not(.sel), .tab-new:hover { background: var(--hover); color: var(--text); }
.tab.sel, .tab-home.sel { color: var(--text); font-weight: 600; background: var(--tab-active); border-color: var(--border); border-bottom-color: transparent; height: 27px; margin-bottom: -1px; } /* joins the work area */
@media (prefers-reduced-motion: reduce) { .tab, .tab-home, .tab-new { transition: none; } }
.tab-home { width: 36px; justify-content: center; }
.tab-home img { width: 16px; height: 16px; }
.tab-new { width: 26px; justify-content: center; }
.tab-new::after { content: ""; width: 10px; height: 10px; background: linear-gradient(var(--text), var(--text)) center / 10px 1.5px no-repeat, linear-gradient(var(--text), var(--text)) center / 1.5px 10px no-repeat; }
.tab { min-width: 80px; max-width: 220px; gap: 4px; padding: 0 6px; }
.tab-ico { width: 16px; height: 16px; flex: none; }
.tab-t { min-width: 0; flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; text-align: left; }
.tab-x { width: 16px; height: 16px; flex: none; display: flex; align-items: center; justify-content: center; font-size: 14px; color: var(--text-muted); }
.tab-x:hover { color: #fff; background: var(--danger); }
.me-check { align-self: center; margin: 0 2px; }
.me-on { color: var(--warning); }
.tab-dot { width: 8px; height: 8px; clip-path: circle(50%); flex: none; background: var(--k-d4a72c); }
.tab-dot.connected { background: var(--success); }
.tab-dot.stopped { background: var(--danger); }

.sidebar { flex: none; display: flex; background: var(--surface); border-right: 1px solid var(--border); }
.side-grip { width: 4px; margin-left: -2px; margin-right: -2px; flex: none; cursor: col-resize; z-index: 2; }
.side-grip:hover { background: var(--accent); opacity: .5; }
.strip { width: 34px; flex: none; display: flex; flex-direction: column; background: var(--panel); }
.strip-b { height: 40px; flex: none; display: flex; align-items: center; justify-content: center; background: var(--panel); border: 1px solid var(--border); border-top: none; cursor: pointer; }
.strip-b:first-child { border-top: 1px solid var(--border); }
.strip-b:hover { background: var(--hover); }
.strip-b.on { background: var(--surface); border-right-color: transparent; }
.strip-rest { flex: 1; border-right: 1px solid var(--border); }
.strip-ico { width: 24px; height: 24px; }
.page { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.tree { flex: 1; min-height: 0; overflow-y: auto; padding-top: 2px; }
.treerow { display: flex; align-items: center; gap: 4px; height: 24px; padding-right: 6px; font-size: 12px; cursor: default; user-select: none; white-space: nowrap; }
.treerow:hover { background: var(--hover); }
.treerow .tw { flex: 0 0 12px; display: flex; align-items: center; justify-content: center; }
/* egui's collapsing triangle: filled, text colour, pointing right (closed) or down (open) */
.tri { width: 0; height: 0; border-top: 4.5px solid transparent; border-bottom: 4.5px solid transparent; border-left: 6px solid var(--text); opacity: .75; transition: transform .1s; }
.tri.open { transform: rotate(90deg); }
.treerow .ico { height: 22px; width: 22px; flex: none; }
.treerow.session.sel { background: var(--pressed); }

.central { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--surface); }
.statusbar { border-top: 1px solid var(--border-subtle); height: 18px; display: flex; align-items: center; gap: 12px; padding: 0 6px; font-size: 11px; color: var(--text); background: var(--panel); }

/* Home (Rust home.rs) */
.home { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; background: var(--surface); color: var(--text); }
.home::before { content: ""; flex: 0 0 22%; } /* 22 % of the area's height, as home.rs */
.home-gear { position: absolute; top: 4px; right: 4px; width: 30px; height: 30px; display: flex; align-items: center; justify-content: center; background: transparent; border: none; cursor: pointer; }
.home-gear:hover { background: var(--hover); }
.home-gear img { width: 24px; height: 24px; }
.home-brand { display: flex; align-items: center; gap: 16px; font-size: 32px; line-height: 1; }
.home-logo { height: 64px; width: 64px; }
.home-actions { display: flex; gap: 24px; margin-top: 24px; }
.home-btn { display: flex; align-items: center; justify-content: center; gap: 4px; width: 200px; height: 30px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.home-btn:hover { background: var(--hover); border-color: var(--outline); }
.home-btn-ico { height: 16px; width: 16px; }
.recent { width: 576px; max-width: calc(100% - 32px); margin-top: 24px; background: var(--panel); padding: 12px 20px 10px; }
.recent-title { height: 22px; text-align: center; font-weight: 700; font-size: 12px; }
.recent-grid { display: grid; grid-template-columns: repeat(3, 1fr); }
.recent-item { display: flex; align-items: center; gap: 6px; height: 24px; padding-right: 8px; font-size: 12px; cursor: pointer; min-width: 0; }
.recent-item:hover span { text-decoration: underline; }
.recent-ico { height: 16px; width: 16px; flex: none; }

.side-note { padding: 8px 12px; color: var(--text-muted); }
.tree-hint { padding: 2px 8px 0 20px; }
.side-err { margin: 8px; padding: 8px; border: 1px solid var(--k-f0c38a); background: var(--k-fff4e5); color: var(--k-8a4b00); }
.locked-note { padding: 2px 0 0 20px; display: flex; flex-direction: column; align-items: flex-start; gap: 4px; color: var(--text); }
.locked-unlock { height: 20px; padding: 0 5px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.locked-unlock:hover { background: var(--hover); }

/* dialogs (Rust: flat windows, panel fill, 1 px border, no shadow or dimming) */
.modal-backdrop { position: absolute; inset: 0; z-index: 50; display: flex; align-items: center; justify-content: center; background: transparent; }
.dialog { width: 422px; background: var(--panel); border: 1px solid var(--border); color: var(--text); }
.dialog-title { font-size: 12px; border-bottom: 1px solid var(--border-subtle); padding: 3px 8px; background: var(--title-bg); color: var(--title-fg); font-weight: var(--title-weight); }
.dialog-body { padding: 8px 10px 10px; }
.dialog-sub { font-size: 12px; margin-bottom: 4px; }
.dialog-label { display: block; font-size: 12px; margin-bottom: 2px; }
.dialog-input { width: 100%; height: 20px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.dialog-input:focus { border-color: var(--focus); }
.dialog-err { color: var(--danger); font-size: 12px; margin-top: 6px; }
.dialog-warn { color: var(--warning); }
.warn-text { color: var(--warning); }
.dialog-check { display: flex; align-items: center; gap: 6px; margin-top: 6px; }
.dialog-hint { font-size: 11px; color: var(--text-muted); margin-top: 2px; }
.dialog-actions { display: flex; justify-content: center; gap: 24px; margin-top: 12px; }
.dbtn { display: flex; align-items: center; justify-content: center; gap: 4px; min-width: 80px; height: 30px; padding: 0 8px; font-size: 12px; background: var(--panel); border: 1px solid var(--border); cursor: pointer; color: var(--text); }
.dbtn:hover:not(:disabled) { background: var(--hover); border-color: var(--outline); }
.dbtn.primary { background: var(--panel); color: var(--text); }
.dbtn:disabled { color: var(--text-disabled); cursor: default; }
.dbtn:disabled .dbtn-ico { opacity: .45; }
.dbtn-ico { height: 16px; width: 16px; }
.fp-head { display: flex; align-items: center; gap: 6px; font-size: 11px; font-weight: 700; background: var(--panel); border: 1px solid var(--border-subtle); padding: 4px 8px; margin: 4px 0; }
.fp { font-family: "DejaVu Sans Mono", monospace; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); padding: 8px 10px; word-break: break-all; margin-bottom: 10px; }
/* context menus (Rust popup menu: 16 px icons, 22 px items) */
.ctx-mask { position: fixed; inset: 0; z-index: 70; }
.ctx { position: fixed; min-width: 180px; background: var(--panel); border: 1px solid var(--border); padding: 2px; }
.ctx-item { display: flex; align-items: center; gap: 6px; width: 100%; height: 22px; padding: 0 6px; font-size: 12px; background: transparent; border: none; text-align: left; cursor: pointer; color: var(--text); }
.ctx-item:hover { background: var(--hover); }
.ctx-item.danger:hover { background: var(--hover); }
.ctx-ico { height: 16px; width: 16px; display: inline-flex; align-items: center; justify-content: center; }
.ctx-del { color: var(--danger); font-weight: 700; }
.ctx-sep { height: 1px; background: var(--border-subtle); margin: 3px 0; }
.ctx-item:disabled { color: var(--text-disabled); cursor: default; background: transparent; }
.ctx-item:disabled .ctx-ico { opacity: .45; }
.ctx-sc { margin-left: auto; padding-left: 20px; color: var(--text-muted); }
.ctx-sub { position: relative; }
.ctx-item.on { background: var(--pressed); }
.ctx-arrow { margin-left: auto; width: 0; height: 0; border-top: 4px solid transparent; border-bottom: 4px solid transparent; border-left: 4px solid var(--text); }
.ctx-colors { position: absolute; left: 100%; top: -3px; min-width: 120px; }
.app :deep(*:not(button, input, textarea, .ed, .st, .dialog, .ask, .fd, .im)), .app { border-radius: 0 !important; } /* controls and windows take the skin radius */
</style>

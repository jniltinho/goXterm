<script setup lang="ts">
import { ref, computed, watch, onMounted, shallowRef } from "vue";
import {
  Sessions, Folders, Locked, Unlock, Connect, ConnectDemo, HasDemo, LocalShell, QuickConnect,
  TelnetConnect, TelnetQuickConnect, ConnectTelnetDemo, HasTelnetDemo,
  SerialConnect, ConnectSerialDemo, HasSerialDemo,
  SftpConnect, FtpConnect, ConnectSftpDemo, HasSftpDemo, ConnectFtpDemo, HasFtpDemo,
  VncConnect, ConnectVncDemo, HasVncDemo,
  RdpConnect, ConnectRdpDemo, HasRdpDemo,
  type Session, type Folder, type RemoteSession, type VncView, type RdpView,
} from "./api";
import { SetMultiExec, SelectMultiExec } from "./api";
import { DeleteSession, DuplicateSession, RenameSession, SaveFolder, DeleteFolder, SshTrustRespond, SshPasswordRespond, GetSettings, Quit } from "./api";

// Toolbar/tab appearance flags from Settings → Display (config.toml, no unlock needed).
const ui = ref({ toolbarCaptions: true, showTabNumbers: true, showCloseButtons: true });
// Rust Metrics: sidebar 240 px (Settings value), icon strip 34 px, 16 px tree indent.
const STRIP_W = 34, INDENT = 16;
const sideW = ref(240);
const rootOpen = ref(true);
const status = ref("Ready");
async function loadUI() {
  try {
    const s = await GetSettings();
    ui.value = { toolbarCaptions: s.toolbarCaptions, showTabNumbers: s.showTabNumbers, showCloseButtons: s.showCloseButtons };
    sideW.value = Math.min(600, Math.max(160, s.sidebarWidth || 240));
    document.documentElement.dataset.theme = s.theme || "system"; // Skin: theme.css picks the palette
  } catch { /* defaults */ }
}

// Header menu bar (MobaXterm parity): one open menu at a time; a click elsewhere closes it.
const menu = ref("");
const sidebarCollapsed = ref(false);
function toggleMenu(name: string) { menu.value = menu.value === name ? "" : name; }
function runMenu(fn: () => void) { menu.value = ""; fn(); }
function aboutApp() { alert("rustXterm — native terminal & remote session manager (Go + Wails)."); }

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
function onKey(e: KeyboardEvent) {
  const mod = e.ctrlKey || e.metaKey;
  const k = e.key.toLowerCase();
  if (mod && e.shiftKey && k === "n") { e.preventDefault(); newSession(); return; }
  if (mod && e.key === "Tab") { e.preventDefault(); cycleTab(e.shiftKey ? -1 : 1); return; }
  if (mod && e.shiftKey && k === "w") { e.preventDefault(); if (active.value) closeTab(active.value); return; }
  if (mod && e.key === ",") { e.preventDefault(); settingsOpen.value = true; return; }
  if (inEditable()) return;
  if (!selected.value) return;
  if (e.key === "Enter") { e.preventDefault(); openSession(selected.value); }
  else if (e.key === "F2") { e.preventDefault(); renameSession(selected.value); }
  else if (e.key === "Delete") { e.preventDefault(); delSession(selected.value); }
}
import { Events } from "@wailsio/runtime";
import TerminalView from "./Terminal.vue";
import FileBrowser from "./FileBrowser.vue";
import VncCanvas from "./VncView.vue";
import RdpCanvas from "./RdpView.vue";
import SessionEditor from "./SessionEditor.vue";
import SettingsDialog from "./SettingsDialog.vue";
import SftpPanel from "./SftpPanel.vue";
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
  await loadUI();
  window.addEventListener("click", (e) => { if (!(e.target as HTMLElement).closest(".menubar")) menu.value = ""; });
  window.addEventListener("keydown", onKey);
  try { locked.value = await Locked(); } catch { locked.value = true; }
  await openDemos(); // lab targets from env vars: they need no saved sessions, so not gated on unlock
  if (locked.value) { loading.value = false; return; }
  await loadData();
});

async function doUnlock() {
  if (!master.value || unlocking.value) return;
  unlockError.value = "";
  unlocking.value = true;
  try {
    await Unlock(master.value);
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
  if (ssh) tabMon.value[id] = true;
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
  if (id === active.value) sidePage.value = "sftp"; // MobaXterm opens the SFTP tab on connect
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
type TrustReq = { reqId: string; host: string; port: number; algorithm: string; fingerprint: string };
const trustQ = ref<TrustReq[]>([]);
const trustReq = computed(() => trustQ.value[0] ?? null);
function respondTrust(decision: "once" | "save" | "reject") {
  const r = trustQ.value[0];
  if (!r) return;
  void SshTrustRespond(r.reqId, decision);
  trustQ.value = trustQ.value.slice(1);
}

// Interactive SSH password prompt (sessions with no saved credential); also queued.
type PassReq = { reqId: string; host: string; user: string; prompt?: string };
const passQ = ref<PassReq[]>([]);
const passReq = computed(() => passQ.value[0] ?? null);
const passValue = ref("");
function respondPassword(ok: boolean) {
  const r = passQ.value[0];
  if (!r) return;
  void SshPasswordRespond(r.reqId, ok ? passValue.value : "", ok);
  passQ.value = passQ.value.slice(1);
  passValue.value = "";
}

function newSession() { editorSessionId.value = null; editorOpen.value = true; }
function importSessions() { /* TODO: MobaXterm .mxtsessions / rustXterm import */ }
function editSession(s: Session) { editorSessionId.value = s.id; editorOpen.value = true; }
async function onEditorSaved(_id: number) { editorOpen.value = false; await loadData(); }
async function delSession(s: Session) {
  if (!confirm(`Delete session "${s.name}"? This cannot be undone.`)) return;
  try { await DeleteSession(s.id); if (selected.value?.id === s.id) selected.value = null; await loadData(); }
  catch (e) { error.value = String(e); }
}
async function dupSession(s: Session) {
  try { await DuplicateSession(s.id); await loadData(); } catch (e) { error.value = String(e); }
}
async function renameSession(s: Session) {
  const name = prompt("Rename session:", s.name);
  if (name === null || !name.trim() || name === s.name) return;
  try { await RenameSession(s.id, name.trim()); await loadData(); } catch (e) { error.value = String(e); }
}
async function newFolder(parent: number | null) {
  const name = prompt("New folder name:");
  if (!name || !name.trim()) return;
  try { await SaveFolder(0, name.trim(), parent); await loadData(); } catch (e) { error.value = String(e); }
}
async function renameFolder(f: Folder) {
  const name = prompt("Rename folder:", f.name);
  if (name === null || !name.trim() || name === f.name) return;
  try { await SaveFolder(f.id, name.trim(), f.parent); await loadData(); } catch (e) { error.value = String(e); }
}
async function delFolder(f: Folder) {
  if (!confirm(`Delete folder "${f.name}"? Its sessions and subfolders move to the root.`)) return;
  try { await DeleteFolder(f.id); await loadData(); } catch (e) { error.value = String(e); }
}
function openCtx(e: MouseEvent, s: Session) { selected.value = s; ctx.value = { x: e.clientX, y: e.clientY, session: s }; }
function openFolderCtx(e: MouseEvent, f: Folder) { ctx.value = { x: e.clientX, y: e.clientY, folder: f }; }
function closeCtx() { ctx.value = null; }

// openSession opens a session in the tab its protocol needs (double-click / Enter).
function openSession(s: Session) {
  pushRecent(s.id);
  switch (s.protocol) {
    case "telnet": openTerm(s.name, "terminal", () => TelnetConnect(s.id), false, sessionUrl(s)); break;
    case "serial": openTerm(s.name, "plug", () => SerialConnect(s.id), false, "serial://" + s.host); break;
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
const collapsed = ref<Set<number>>(new Set());
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
  for (const s of ses(null)) out.push({ kind: "session", session: s, depth: 0 });
  const walk = (parent: number | null, depth: number) => {
    for (const f of (byParent.get(parent) ?? []).sort((a, b) => a.name.localeCompare(b.name))) {
      const open = !collapsed.value.has(f.id);
      const hasKids = (byParent.get(f.id)?.length ?? 0) + ses(f.id).length > 0;
      out.push({ kind: "folder", folder: f, depth, open, hasKids });
      if (open) {
        for (const s of ses(f.id)) out.push({ kind: "session", session: s, depth: depth + 1 });
        walk(f.id, depth + 1);
      }
    }
  };
  walk(null, 0);
  return out;
});

const protoIcon: Record<string, string> = {
  ssh: "ssh", sftp: "sftp", ftp: "ftp", ftps: "ftps", rdp: "rdp", vnc: "vnc",
  telnet: "terminal", serial: "plug", wsl: "cmd", local: "terminal",
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
        <div class="dialog-title">Unlock sessions</div>
        <div class="dialog-body">
          <p class="dialog-sub">Enter the master password to open saved sessions.</p>
          <label class="dialog-label">Master password</label>
          <input
            type="password"
            class="dialog-input"
            v-model="master"
            @keyup.enter="doUnlock"
            autofocus
          />
          <div v-if="unlockError" class="dialog-err">{{ unlockError }}</div>
          <div class="dialog-actions">
            <button class="dbtn primary" :disabled="!master || unlocking" @click="doUnlock">
              <img :src="icon('ok')" class="dbtn-ico" /> {{ unlocking ? "Unlocking…" : "Unlock" }}
            </button>
            <button class="dbtn" @click="showUnlock = false"><img :src="icon('cancel')" class="dbtn-ico" /> Cancel</button>
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
          <button class="menu-i" @click="runMenu(() => Quit())"><img :src="icon('exit')" class="menu-ico" /> Exit</button>
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
      <button class="tbtn" title="Exit" @click="Quit()"><img :src="icon('exit')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">Exit</span></button>
    </header>

    <!-- 28 px row: Quick connect over the sidebar column, then the tab strip -->
    <div class="row">
      <div class="qc-col" :style="{ width: (sidebarCollapsed ? sideW - STRIP_W : sideW) + 'px' }">
        <input v-model="quick" class="qc" placeholder="Quick connect..." spellcheck="false" aria-label="Quick connect" :class="quickErr ? 'err' : ''" :title="quickErr" @keydown.enter="quickConnect" @keydown.esc="quick = ''; quickErr = ''" />
      </div>
      <div class="tabstrip">
        <button class="tab-home" :class="!active ? 'sel' : ''" title="Home" @click="active = ''"><img :src="icon('house')" /></button>
        <template v-for="(t, i) in tabs" :key="t.id">
        <input v-if="multiexec && t.kind === 'ssh'" type="checkbox" class="me-check" :checked="!!tabMulti[t.id]" :title="'MultiExec ' + t.title" @change="selectMulti(t.id, ($event.target as HTMLInputElement).checked)" />
        <button class="tab" :class="t.id === active ? 'sel' : ''" :title="tabUrl[t.id] || t.title" @click="active = t.id" @mousedown.middle.prevent="closeTab(t.id)">
          <img :src="tabIcon(t)" class="tab-ico" />
          <span class="tab-dot" :class="tabState[t.id] ?? 'connecting'" :title="STATE_LABEL[tabState[t.id] ?? 'connecting']"></span>
          <span class="tab-t"><template v-if="ui.showTabNumbers">{{ i + 1 }}. </template>{{ t.title }}</span>
          <img v-if="multiexec && tabMulti[t.id]" :src="icon('multiexec')" class="tab-ico" title="MultiExec" />
          <span v-if="ui.showCloseButtons" class="tab-x" title="Close" @click.stop="closeTab(t.id)">×</span>
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
          <div v-else class="tree">
            <div class="treerow root" :style="{ paddingLeft: '4px' }" @click="rootOpen = !rootOpen" @contextmenu.prevent="!locked && (ctx = { x: $event.clientX, y: $event.clientY, root: true })">
              <span class="tw">{{ rootOpen ? "▼" : "▶" }}</span>
              <img :src="icon('folder-user')" class="ico" />
              <span class="truncate">User sessions</span>
            </div>
            <template v-if="rootOpen">
              <div v-if="locked" class="locked-note">
                <div>Saved sessions are locked.</div>
                <button class="locked-unlock" @click="unlockError = ''; showUnlock = true">Unlock...</button>
              </div>
              <div v-else-if="loading" class="side-note">Loading…</div>
              <div v-else-if="error" class="side-err">{{ error }}</div>
              <template v-else>
                <div v-for="(row, i) in rows" :key="i">
                  <div
                    v-if="row.kind === 'folder'"
                    class="treerow"
                    :style="{ paddingLeft: 4 + (row.depth + 1) * INDENT + 'px' }"
                    @click="toggleFolder(row.folder.id)"
                    @contextmenu.prevent="openFolderCtx($event, row.folder)"
                  >
                    <span class="tw">{{ row.hasKids ? (row.open ? "▼" : "▶") : "" }}</span>
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

      <main class="central">
        <div class="relative min-h-0 flex-1">
          <template v-for="t in tabs" :key="t.id">
            <TerminalView
              v-if="t.kind === 'ssh'"
              v-show="t.id === active"
              :connect="t.connect"
              :monitor="!!tabMon[t.id]"
              @close="closeTab(t.id)"
              @duplicate="duplicateTab(t)"
              @state="(st) => (tabState[t.id] = st)"
              @backend="(b) => onBackend(t.id, b)"
              @cwd="(d) => (tabCwd[t.id] = d)"
              @enter="enterTick[t.id] = (enterTick[t.id] ?? 0) + 1"
              class="absolute inset-0 p-1"
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
              <button class="home-btn" @click="newSession"><img :src="icon('session-new')" class="home-btn-ico" /> New session</button>
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
        <template v-if="ctx.session">
          <button class="ctx-item" @click="openSession(ctx!.session!); closeCtx()"><img :src="icon('ssh')" class="ctx-ico" /> Connect</button>
          <button class="ctx-item" @click="editSession(ctx!.session!); closeCtx()"><img :src="icon('gear')" class="ctx-ico" /> Edit session</button>
          <button class="ctx-item" @click="dupSession(ctx!.session!); closeCtx()"><img :src="icon('session')" class="ctx-ico" /> Duplicate session</button>
          <button class="ctx-item" @click="renameSession(ctx!.session!); closeCtx()"><span class="ctx-ico">✎</span> Rename session</button>
          <button class="ctx-item danger" @click="delSession(ctx!.session!); closeCtx()"><span class="ctx-ico ctx-del">✕</span> Delete session</button>
          <div class="ctx-sep"></div>
          <button class="ctx-item" @click="newSession(); closeCtx()"><img :src="icon('session-new')" class="ctx-ico" /> New session</button>
        </template>
        <template v-else-if="ctx.root">
          <button class="ctx-item" @click="newSession(); closeCtx()"><img :src="icon('session-new')" class="ctx-ico" /> New session</button>
          <button class="ctx-item" @click="newFolder(null); closeCtx()"><img :src="icon('folder')" class="ctx-ico" /> New folder</button>
        </template>
        <template v-else-if="ctx.folder">
          <button class="ctx-item" @click="newSession(); closeCtx()"><img :src="icon('session-new')" class="ctx-ico" /> New session here</button>
          <button class="ctx-item" @click="newFolder(ctx!.folder!.id); closeCtx()"><img :src="icon('folder')" class="ctx-ico" /> New folder here</button>
          <div class="ctx-sep"></div>
          <button class="ctx-item" @click="renameFolder(ctx!.folder!); closeCtx()"><span class="ctx-ico">✎</span> Rename folder</button>
          <button class="ctx-item danger" @click="delFolder(ctx!.folder!); closeCtx()"><span class="ctx-ico ctx-del">✕</span> Delete folder</button>
        </template>
      </div>
    </div>

    <!-- session editor -->
    <SessionEditor
      v-if="editorOpen"
      :sessionId="editorSessionId"
      :folders="folders"
      @saved="onEditorSaved"
      @close="editorOpen = false"
    />

    <!-- global settings -->
    <SettingsDialog v-if="settingsOpen" @saved="settingsOpen = false; loadUI()" @close="settingsOpen = false" />
    <TunnelsDialog v-if="tunnelsOpen" :sessions="sessions" @close="tunnelsOpen = false" />

    <!-- SSH host-key trust (trust-on-first-use) -->
    <div v-if="trustReq" class="modal-backdrop" style="z-index: 65">
      <div class="dialog" style="width: 470px">
        <div class="dialog-title">Unknown host key</div>
        <div class="dialog-body">
          <p class="dialog-sub">The SSH host key of <b>{{ trustReq.host }}:{{ trustReq.port }}</b> is not known. No credentials are sent until you accept.</p>
          <div class="fp-head"><img :src="icon('key')" class="dbtn-ico" /> {{ trustReq.algorithm }} key fingerprint</div>
          <div class="fp">{{ trustReq.fingerprint }}</div>
          <p class="dialog-sub">Accept only if it matches the fingerprint published by the server administrator.</p>
          <div class="dialog-actions">
            <button class="dbtn primary" @click="respondTrust('once')">Accept once</button>
            <button class="dbtn" @click="respondTrust('save')">Accept and save</button>
            <button class="dbtn" @click="respondTrust('reject')">Cancel</button>
          </div>
        </div>
      </div>
    </div>

    <!-- interactive SSH password (sessions without a saved credential) -->
    <div v-if="passReq" class="modal-backdrop" style="z-index: 66">
      <div class="dialog" style="width: 400px">
        <div class="dialog-title">Password</div>
        <div class="dialog-body">
          <p class="dialog-sub" v-if="passReq.prompt">{{ passReq.prompt }}.</p>
          <p class="dialog-sub" v-else>Enter the password for <b>{{ (passReq.user || "user") + "@" + passReq.host }}</b>.</p>
          <label class="dialog-label">Password</label>
          <input type="password" class="dialog-input" v-model="passValue" @keyup.enter="respondPassword(true)" autofocus />
          <div class="dialog-actions">
            <button class="dbtn primary" @click="respondPassword(true)">OK</button>
            <button class="dbtn" @click="respondPassword(false)">Cancel</button>
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

.toolbar { display: flex; align-items: center; gap: 4px; height: 52px; padding: 0 4px; background: var(--toolbar); }
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

.row { display: flex; height: 28px; background: var(--toolbar); }
.qc-col { flex: none; display: flex; align-items: center; padding: 0 2px; }
.qc { width: 100%; height: 26px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.qc:focus { border-color: var(--focus); }
.qc.err { border-color: var(--danger); }
.qc::placeholder { color: var(--text-muted); }
.tabstrip { flex: 1; min-width: 0; display: flex; align-items: flex-end; gap: 2px; padding-left: 2px; overflow: hidden; }
.tab, .tab-home, .tab-new { height: 26px; flex: none; display: flex; align-items: center; background: var(--tab-inactive); border: 1px solid var(--border); cursor: pointer; color: var(--text); font-size: 12px; position: relative; }
.tab:hover, .tab-home:hover, .tab-new:hover { background: var(--hover); }
.tab.sel, .tab-home.sel { background: var(--tab-active); border-bottom-color: transparent; }
.tab.sel::before, .tab-home.sel::before { content: ""; position: absolute; left: -1px; right: -1px; top: -1px; height: 2px; background: var(--accent); }
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
.treerow .tw { flex: 0 0 12px; text-align: center; font-size: 8px; color: var(--text-muted); }
.treerow .ico { height: 22px; width: 22px; flex: none; }
.treerow.session.sel { background: var(--pressed); }

.central { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--surface); }
.statusbar { height: 18px; display: flex; align-items: center; gap: 12px; padding: 0 6px; font-size: 11px; color: var(--text); background: var(--panel); }

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
.side-err { margin: 8px; padding: 8px; border: 1px solid var(--k-f0c38a); background: var(--k-fff4e5); color: var(--k-8a4b00); }
.locked-note { padding: 2px 0 0 20px; display: flex; flex-direction: column; align-items: flex-start; gap: 4px; color: var(--text); }
.locked-unlock { height: 20px; padding: 0 5px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.locked-unlock:hover { background: var(--hover); }

/* dialogs (Rust: flat windows, panel fill, 1 px border, no shadow or dimming) */
.modal-backdrop { position: absolute; inset: 0; z-index: 50; display: flex; align-items: center; justify-content: center; background: transparent; }
.dialog { width: 422px; background: var(--panel); border: 1px solid var(--border); color: var(--text); }
.dialog-title { font-size: 12px; color: var(--text); background: var(--panel); border-bottom: 1px solid var(--border-subtle); padding: 3px 8px; }
.dialog-body { padding: 8px 10px 10px; }
.dialog-sub { font-size: 12px; margin-bottom: 4px; }
.dialog-label { display: block; font-size: 12px; margin-bottom: 2px; }
.dialog-input { width: 100%; height: 20px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.dialog-input:focus { border-color: var(--focus); }
.dialog-err { color: var(--danger); font-size: 12px; margin-top: 6px; }
.dialog-actions { display: flex; justify-content: center; gap: 20px; margin-top: 12px; }
.dbtn { display: flex; align-items: center; justify-content: center; gap: 4px; min-width: 74px; height: 26px; padding: 0 8px; font-size: 12px; background: var(--panel); border: 1px solid var(--border); cursor: pointer; color: var(--text); }
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
.app :deep(*), .app { border-radius: 0 !important; }
</style>

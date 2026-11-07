<script setup lang="ts">
import { ref, computed, watch, onMounted, shallowRef } from "vue";
import {
  Sessions, Folders, Locked, Unlock, Connect, ConnectDemo, HasDemo, LocalShell,
  TelnetConnect, ConnectTelnetDemo, HasTelnetDemo,
  SerialConnect, ConnectSerialDemo, HasSerialDemo,
  SftpConnect, FtpConnect, ConnectSftpDemo, HasSftpDemo, ConnectFtpDemo, HasFtpDemo,
  VncConnect, ConnectVncDemo, HasVncDemo,
  RdpConnect, ConnectRdpDemo, HasRdpDemo,
  type Session, type Folder, type RemoteSession, type VncView, type RdpView,
} from "./api";
import { DeleteSession, DuplicateSession, RenameSession, SaveFolder, DeleteFolder, SshTrustRespond, SshPasswordRespond, GetSettings, Quit } from "./api";

// Toolbar/tab appearance flags from Settings → Display (config.toml, no unlock needed).
const ui = ref({ toolbarCaptions: true, showTabNumbers: true, showCloseButtons: true });
async function loadUI() {
  try {
    const s = await GetSettings();
    ui.value = { toolbarCaptions: s.toolbarCaptions, showTabNumbers: s.showTabNumbers, showCloseButtons: s.showCloseButtons };
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
const ctx = ref<{ x: number; y: number; session?: Session; folder?: Folder } | null>(null);

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
  for (const m of [tabState, tabUrl, tabMon, tabBackend, tabCwd, enterTick]) delete m.value[id];
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
              <img :src="icon('key')" class="dbtn-ico" /> {{ unlocking ? "Unlocking…" : "Unlock" }}
            </button>
            <button class="dbtn" @click="showUnlock = false">Cancel</button>
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
    <header class="toolbar flex items-stretch gap-0.5 px-1.5 py-1" :class="ui.toolbarCaptions ? 'cap' : ''">
      <button class="tbtn" title="New session" @click="newSession()"><img :src="icon('session-new')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">New</span></button>
      <button class="tbtn" title="Local terminal" @click="openLocal"><img :src="icon('terminal')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">Local</span></button>
      <button class="tbtn" :class="selected ? '' : 'off'" title="Open SSH terminal" @click="selected && openTerm(selected.name, 'ssh', () => Connect(selected!.id), true, sessionUrl(selected))"><img :src="icon('ssh')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">SSH</span></button>
      <button class="tbtn" :class="selected ? '' : 'off'" title="Open SFTP file browser" @click="openSftpFor(selected)"><img :src="icon('sftp')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">SFTP</span></button>
      <button class="tbtn" :class="selected ? '' : 'off'" title="Open FTP/FTPS file browser" @click="openFtpFor(selected)"><img :src="icon('ftp')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">FTP</span></button>
      <button class="tbtn" :class="selected ? '' : 'off'" title="Open VNC" @click="openVncFor(selected)"><img :src="icon('vnc')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">VNC</span></button>
      <button class="tbtn" :class="selected ? '' : 'off'" title="Open RDP" @click="openRdpFor(selected)"><img :src="icon('rdp')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">RDP</span></button>
      <span class="tbsep"></span>
      <button class="tbtn" title="SSH tunnels (port forwarding)" @click="tunnelsOpen = true"><img :src="icon('tunnel')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">Tunnels</span></button>
      <span class="tbsep"></span>
      <input class="qc self-center" placeholder="Quick connect — user@host[:port]" disabled />
      <span class="ml-auto flex items-stretch gap-0.5">
        <button class="tbtn" title="Settings" @click="settingsOpen = true"><img :src="icon('gear')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">Settings</span></button>
        <button class="tbtn" title="Help"><img :src="icon('help')" class="tbico" /><span v-if="ui.toolbarCaptions" class="tbcap">Help</span></button>
      </span>
    </header>

    <div class="flex min-h-0 flex-1">
      <!-- vertical page strip (MobaXterm): Sessions / SFTP; clicking the open page hides the sidebar -->
      <nav class="strip">
        <button
          v-for="p in (['sessions', 'sftp'] as const)"
          :key="p"
          class="strip-b"
          :class="!sidebarCollapsed && sidePage === p ? 'on' : ''"
          :title="p === 'sessions' ? 'Sessions' : 'SFTP browser'"
          @click="sidebarCollapsed = !sidebarCollapsed && sidePage === p; sidePage = p"
        >
          <img :src="icon(p === 'sessions' ? 'star' : 'sftp')" class="strip-ico" />
          <span class="strip-t">{{ p === "sessions" ? "Sessions" : "SFTP" }}</span>
        </button>
      </nav>
      <aside v-show="!sidebarCollapsed" class="sidebar flex w-72 shrink-0 flex-col">
        <SftpPanel
          v-if="sidePage === 'sftp'"
          v-model:follow="followFolder"
          :term-id="sftpTerm"
          :cwd="tabCwd[activeSsh] ?? ''"
          :enter-tick="enterTick[activeSsh] ?? 0"
          :monitor="!!tabMon[activeSsh]"
          @toggle-monitor="tabMon[activeSsh] = !tabMon[activeSsh]"
        />
        <template v-else>
        <div class="side-head flex items-center justify-between px-2 py-1">
          <span class="flex items-center gap-1.5"><img :src="icon('session')" class="h-3.5 w-3.5" /> Sessions</span>
          <span class="flex items-center gap-1.5">
            <img v-if="!locked" :src="icon('folder')" class="h-3.5 w-3.5 cursor-pointer opacity-70 hover:opacity-100" title="New folder" @click="newFolder(null)" />
            <span v-if="!loading && !error" class="count">{{ sessions.length }}</span>
          </span>
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto">
          <div v-if="locked" class="locked-note">
            <div class="locked-head"><img :src="icon('key')" class="h-3.5 w-3.5 opacity-70" /> Saved sessions are locked.</div>
            <button class="locked-unlock" @click="unlockError = ''; showUnlock = true">Unlock…</button>
          </div>
          <div v-else-if="loading" class="p-3 text-xs text-neutral-500">Loading…</div>
          <div v-else-if="error" class="m-2 border border-amber-400 bg-amber-50 p-2 text-xs text-amber-800">{{ error }}</div>
          <template v-else>
            <div v-for="(row, i) in rows" :key="i">
              <div
                v-if="row.kind === 'folder'"
                class="treerow folder"
                :style="{ paddingLeft: 6 + row.depth * 14 + 'px' }"
                @click="toggleFolder(row.folder.id)"
                @contextmenu.prevent="openFolderCtx($event, row.folder)"
              >
                <span class="tw">{{ row.hasKids ? (row.open ? "▾" : "▸") : "" }}</span>
                <img :src="folderIcon(row.folder)" class="ico" />
                <span class="truncate">{{ row.folder.name }}</span>
              </div>
              <div
                v-else
                class="treerow session"
                :class="selected?.id === row.session.id ? 'sel' : ''"
                :style="{ paddingLeft: 6 + row.depth * 14 + 'px' }"
                :title="'Click to select, double-click to open, right-click for actions'"
                @click="selected = row.session"
                @dblclick="openSession(row.session)"
                @contextmenu.prevent="openCtx($event, row.session)"
              >
                <span class="tw"></span>
                <img :src="sessionIcon(row.session)" class="ico" />
                <span class="truncate">{{ row.session.name }}</span>
                <img v-if="row.session.password === 'saved'" :src="icon('key')" class="ml-auto h-3 w-3 opacity-70" title="password saved" />
              </div>
            </div>
          </template>
        </div>
        </template>
      </aside>

      <main class="flex min-w-0 flex-1 flex-col bg-[#0b0e11]">
        <div class="tabbar flex items-stretch">
          <button class="tab-home" :class="!active ? 'tab-active' : ''" title="Home" @click="active = ''"><img :src="icon('house')" class="h-4 w-4" /></button>
          <button v-for="(t, i) in tabs" :key="t.id" class="tab flex items-center gap-1.5" :class="t.id === active ? 'tab-active' : ''" :title="tabUrl[t.id] || t.title" @click="active = t.id" @mousedown.middle.prevent="closeTab(t.id)">
            <img :src="tabIcon(t)" class="h-4 w-4" />
            <span class="tab-dot" :class="tabState[t.id] ?? 'connecting'" :title="STATE_LABEL[tabState[t.id] ?? 'connecting']"></span>
            <span class="max-w-[180px] truncate"><template v-if="ui.showTabNumbers">{{ i + 1 }}. </template>{{ t.title }}</span>
            <span v-if="ui.showCloseButtons" class="tab-x" title="Close" @click.stop="closeTab(t.id)">×</span>
          </button>
          <button class="tab-new" title="New local terminal" @click="openLocal">+</button>
        </div>
        <div class="relative min-h-0 flex-1">
          <template v-for="t in tabs" :key="t.id">
            <TerminalView
              v-if="t.kind === 'ssh'"
              v-show="t.id === active"
              :connect="t.connect"
              :monitor="!!tabMon[t.id]"
              @close="closeTab(t.id)"
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
          <div v-if="!active" class="home">
            <div class="home-brand"><img :src="icon('logo')" class="home-logo" @error="($event.target as HTMLImageElement).src = icon('session')" /><span>rustXterm</span></div>
            <div class="home-actions">
              <button class="home-btn" @click="newSession"><img :src="icon('session-new')" class="home-btn-ico" /> New session</button>
              <button class="home-btn" @click="importSessions"><img :src="icon('folder')" class="home-btn-ico" /> Import sessions</button>
            </div>
            <div v-if="recentSessions.length" class="recent">
              <div class="recent-title">Recent sessions</div>
              <div class="recent-grid">
                <div v-for="s in recentSessions" :key="s.id" class="recent-item" @click="openSession(s)" :title="s.host">
                  <img :src="sessionIcon(s)" class="recent-ico" /><span class="truncate">{{ s.name }}</span>
                </div>
              </div>
            </div>
          </div>
        </div>
      </main>
    </div>

    <footer class="statusbar flex items-center gap-3 px-2">
      <span>Ready</span>
      <span class="ml-auto">
        <template v-if="active && tabUrl[active]">{{ tabUrl[active] }} — {{ STATE_LABEL[tabState[active] ?? "connecting"] }}</template>
      </span>
      <span class="sb-tabs">{{ tabs.length }} tab(s)</span>
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
.app { background: var(--k-eef1f4); color: var(--k-1e1e1e); }
.menubar { background: var(--k-eef1f4); border-bottom: 1px solid var(--k-c9d1d9); font-size: 12.5px; color: var(--k-2b3a47); user-select: none; }
.menu { position: relative; }
.menu-t { padding: 3px 10px; background: transparent; border: none; cursor: pointer; color: inherit; }
.menu-t:hover, .menu-t.on { background: var(--k-d7e3f4); }
.menu-d { position: absolute; top: 100%; left: 0; z-index: 70; min-width: 190px; background: var(--k-f7f9fb); border: 1px solid var(--k-9aa7b4); box-shadow: 0 8px 22px rgba(0,0,0,.28); padding: 3px; display: flex; flex-direction: column; }
.menu-i { display: flex; align-items: center; gap: 8px; padding: 5px 10px; background: transparent; border: none; cursor: pointer; text-align: left; color: var(--k-1e2a36); font-size: 12.5px; }
.menu-i:hover { background: var(--k-2f6fb3); color: #fff; }
.menu-ico { height: 16px; width: 16px; }
.menu-sep { height: 1px; margin: 3px 0; background: var(--k-d6dde4); }
.toolbar { background: linear-gradient(var(--k-fbfcfd), var(--k-e9edf1)); border-bottom: 1px solid var(--k-c9d1d9); }
.tbtn { display: inline-flex; align-items: center; gap: 5px; padding: 2px 6px; background: transparent; border: 1px solid transparent; cursor: pointer; color: var(--k-2b3a47); }
.tbtn:hover { background: var(--k-d7e3f4); border-color: var(--k-a9c7ef); }
.tbtn.off { opacity: .4; cursor: default; }
.tbtn.off:hover { background: transparent; border-color: transparent; }
.toolbar.cap .tbtn { flex-direction: column; gap: 1px; padding: 2px 8px; min-width: 46px; }
.tbico { height: 20px; width: 20px; padding: 1px; }
.toolbar.cap .tbico { height: 24px; width: 24px; }
.tbcap { font-size: 10.5px; line-height: 1; white-space: nowrap; }
.tbsep { width: 1px; margin: 2px 4px; background: var(--k-c9d1d9); align-self: stretch; }
.qc { width: 300px; height: 24px; padding: 0 8px; font-size: 12px; background: var(--k-ffffff); border: 1px solid var(--k-c9d1d9); }
.sidebar { background: var(--k-f3f5f8); border-right: 1px solid var(--k-c9d1d9); }
.side-head { font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: .04em; color: var(--k-5a6b7b); background: var(--k-e7ecf1); border-bottom: 1px solid var(--k-d6dde4); }
.count { background: var(--k-d6dde4); color: var(--k-4a5a68); font-size: 10px; font-weight: 600; padding: 0 6px; }
.treerow { display: flex; align-items: center; gap: 5px; height: 22px; font-size: 13px; cursor: default; user-select: none; }
.treerow .tw { flex: 0 0 12px; text-align: center; font-size: 9px; color: var(--k-8a97a5); }
.treerow .ico { height: 16px; width: 16px; flex: none; }
.treerow.session { cursor: pointer; padding-right: 6px; }
.treerow.session:hover { background: var(--k-d7e3f4); }
.treerow.session.sel { background: var(--k-cfe0f6); outline: 1px solid var(--k-a9c7ef); }
.treerow.folder { color: var(--k-33465a); font-weight: 700; cursor: pointer; }
.treerow.folder:hover { background: var(--k-e3ebf3); }
.strip { display: flex; flex-direction: column; width: 26px; flex: none; background: var(--k-e2e7ec); border-right: 1px solid var(--k-c9d1d9); }
.strip-b { display: flex; flex-direction: column; align-items: center; gap: 4px; padding: 6px 0 8px; background: transparent; border: none; border-left: 2px solid transparent; border-bottom: 1px solid var(--k-d0d7de); cursor: pointer; color: var(--k-4a5a68); }
.strip-b:hover { background: var(--k-d7e3f4); }
.strip-b.on { background: var(--k-f3f5f8); color: var(--k-1e2a36); border-left-color: var(--k-2f6fb3); }
.strip-ico { width: 16px; height: 16px; }
.strip-t { writing-mode: vertical-rl; transform: rotate(180deg); font-size: 11.5px; letter-spacing: .02em; }
.tabbar { height: 28px; background: var(--k-dfe5eb); border-bottom: 1px solid var(--k-c2ccd6); }
.tab, .tab-home, .tab-new { border: none; border-right: 1px solid var(--k-c2ccd6); border-top: 2px solid transparent; cursor: pointer; }
.tab { padding: 0 8px; font-size: 12px; color: var(--k-3a4a5a); background: var(--k-e9edf1); max-width: 240px; }
.tab:hover, .tab-home:hover, .tab-new:hover { background: var(--k-f4f6f8); }
.tab-home { width: 32px; display: flex; align-items: center; justify-content: center; background: var(--k-e9edf1); }
.tab-new { width: 28px; font-size: 17px; line-height: 1; color: var(--k-4a5a68); background: transparent; }
.tab-active, .tab-active:hover { color: var(--k-11202e); background: var(--k-ffffff); border-top-color: var(--k-2f6fb3); }
.tab-x { margin-left: 2px; padding: 0 4px; color: var(--k-7f8ea0); }
.tab-x:hover { color: #fff; background: var(--k-c0392b); }
.tab-dot { width: 8px; height: 8px; clip-path: circle(50%); flex: none; background: var(--k-d4a72c); }
.tab-dot.connected { background: var(--k-3fb950); }
.tab-dot.stopped { background: var(--k-e05561); }
.sb-tabs { min-width: 56px; text-align: right; }
.statusbar { height: 22px; font-size: 11px; color: var(--k-5a6b7b); background: var(--k-e7ecf1); border-top: 1px solid var(--k-c9d1d9); }
/* home (shown behind the unlock dialog and whenever no tab is open) */
.home { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 22px; background: var(--k-eef1f4); color: var(--k-2b3a4a); }
.home-brand { display: flex; align-items: center; gap: 14px; font-size: 34px; font-weight: 600; letter-spacing: .5px; }
.home-logo { height: 52px; width: 52px; filter: drop-shadow(0 2px 3px rgba(0,0,0,.18)); }
.home-actions { display: flex; gap: 14px; }
.home-btn { display: flex; align-items: center; gap: 8px; height: 38px; padding: 0 20px; font-size: 14px; color: var(--k-1e1e1e);
  background: linear-gradient(var(--k-ffffff), var(--k-eef2f6)); border: 1px solid var(--k-b9c4cf); box-shadow: 0 1px 2px rgba(0,0,0,.12); cursor: pointer; }
.home-btn:hover { background: linear-gradient(var(--k-ffffff), var(--k-e3ebf3)); border-color: var(--k-8fb4dd); }
.home-btn-ico { height: 18px; width: 18px; }
.recent { width: 540px; max-width: 80%; background: var(--k-f3f6f9); border: 1px solid var(--k-c9d1d9); box-shadow: 0 1px 3px rgba(0,0,0,.08); padding: 10px 14px 14px; }
.recent-title { text-align: center; font-size: 13px; font-weight: 700; color: var(--k-4a5a68); margin-bottom: 8px; }
.recent-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 4px 16px; }
.recent-item { display: flex; align-items: center; gap: 7px; font-size: 13px; padding: 3px 4px; cursor: pointer; color: var(--k-1e1e1e); }
.recent-item:hover { background: var(--k-d7e3f4); }
.recent-ico { height: 16px; width: 16px; flex: none; }

/* locked sidebar note */
.locked-note { padding: 10px; }
.locked-head { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--k-7a6a2a); }
.locked-unlock { margin-top: 8px; height: 26px; padding: 0 12px; font-size: 12px; background: linear-gradient(var(--k-ffffff), var(--k-eef2f6)); border: 1px solid var(--k-b9c4cf); box-shadow: 0 1px 2px rgba(0,0,0,.1); cursor: pointer; }
.locked-unlock:hover { border-color: var(--k-8fb4dd); }

/* modal unlock dialog, raised off the shell with a backdrop and shadow (volume) */
.modal-backdrop { position: absolute; inset: 0; z-index: 50; display: flex; align-items: center; justify-content: center; background: rgba(20, 28, 38, .28); }
.dialog { width: 380px; background: var(--k-f2f4f7); border: 1px solid var(--k-9aa7b4); box-shadow: 0 14px 40px rgba(0,0,0,.35), 0 2px 6px rgba(0,0,0,.25); }
.dialog-title { font-size: 12px; font-weight: 700; color: var(--k-33465a); background: linear-gradient(var(--k-e9eef3), var(--k-dbe3ea)); border-bottom: 1px solid var(--k-c2ccd6); padding: 7px 12px; }
.dialog-body { padding: 14px 16px 16px; }
.dialog-sub { font-size: 12.5px; color: var(--k-42536a); margin-bottom: 10px; }
.dialog-label { display: block; font-size: 12px; color: var(--k-1e1e1e); margin-bottom: 4px; }
.dialog-input { width: 100%; height: 30px; padding: 0 9px; font-size: 13px; background: var(--k-ffffff); border: 1px solid var(--k-a9b4c0); box-shadow: inset 0 1px 2px rgba(0,0,0,.08); }
.dialog-input:focus { outline: none; border-color: var(--k-5b9bd5); box-shadow: 0 0 0 2px rgba(91,155,213,.35); }
.dialog-err { color: var(--k-c0392b); font-size: 12px; margin-top: 8px; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
.dbtn { display: flex; align-items: center; gap: 6px; height: 30px; padding: 0 16px; font-size: 13px; background: linear-gradient(var(--k-ffffff), var(--k-e9eef3)); border: 1px solid var(--k-a9b4c0); box-shadow: 0 1px 2px rgba(0,0,0,.12); cursor: pointer; color: var(--k-1e1e1e); }
.dbtn:hover { border-color: var(--k-8fb4dd); }
.dbtn.primary { background: linear-gradient(var(--k-4a8fd1), var(--k-2f6fb5)); border-color: var(--k-2a62a0); color: #fff; }
.dbtn.primary:hover { background: linear-gradient(var(--k-55a0e0), var(--k-3277c0)); }
.dbtn.primary:disabled { opacity: .55; cursor: default; }
.dbtn-ico { height: 14px; width: 14px; }
.fp-head { display: flex; align-items: center; gap: 6px; font-size: 11px; font-weight: 700; color: var(--k-4a5a68); background: var(--k-e7ecf1); border: 1px solid var(--k-d6dde4); padding: 4px 8px; margin: 4px 0; }
.fp { font-family: "DejaVu Sans Mono", monospace; font-size: 12px; color: var(--k-1e3a5a); background: var(--k-ffffff); border: 1px solid var(--k-c2ccd6); padding: 8px 10px; word-break: break-all; margin-bottom: 10px; }
/* tree context menu */
.ctx-mask { position: fixed; inset: 0; z-index: 70; }
.ctx { position: fixed; min-width: 180px; background: var(--k-f7f9fb); border: 1px solid var(--k-9aa7b4); box-shadow: 0 8px 24px rgba(0,0,0,.28); padding: 3px; }
.ctx-item { display: flex; align-items: center; gap: 8px; width: 100%; padding: 5px 10px; font-size: 13px; background: transparent; border: none; text-align: left; cursor: pointer; color: var(--k-1e1e1e); }
.ctx-item:hover { background: var(--k-d7e3f4); }
.ctx-item.danger:hover { background: var(--k-f4d7d7); }
.ctx-ico { height: 16px; width: 16px; display: inline-flex; align-items: center; justify-content: center; }
.ctx-del { color: var(--k-c0392b); font-weight: 700; }
.ctx-sep { height: 1px; background: var(--k-d6dde4); margin: 3px 0; }
.app :deep(*), .app { border-radius: 0 !important; }
</style>

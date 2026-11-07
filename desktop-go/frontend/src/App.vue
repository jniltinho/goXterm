<script setup lang="ts">
import { ref, computed, onMounted, shallowRef } from "vue";
import {
  Sessions, Folders, Locked, Unlock, Connect, ConnectDemo, HasDemo,
  TelnetConnect, ConnectTelnetDemo, HasTelnetDemo,
  SerialConnect, ConnectSerialDemo, HasSerialDemo,
  SftpConnect, FtpConnect, ConnectSftpDemo, HasSftpDemo, ConnectFtpDemo, HasFtpDemo,
  VncConnect, ConnectVncDemo, HasVncDemo,
  RdpConnect, ConnectRdpDemo, HasRdpDemo,
  type Session, type Folder, type RemoteSession, type VncView, type RdpView,
} from "./api";
import { DeleteSession, DuplicateSession, RenameSession, SaveFolder, DeleteFolder, SshTrustRespond, SshPasswordRespond } from "./api";
import { Events } from "@wailsio/runtime";
import TerminalView from "./Terminal.vue";
import FileBrowser from "./FileBrowser.vue";
import VncCanvas from "./VncView.vue";
import RdpCanvas from "./RdpView.vue";
import SessionEditor from "./SessionEditor.vue";
import SettingsDialog from "./SettingsDialog.vue";

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
  | { id: string; title: string; kind: "ssh"; icon: string; connect: () => Promise<string>; monitor?: boolean }
  | { id: string; title: string; kind: "files"; icon: string; connect: () => Promise<RemoteSession> }
  | { id: string; title: string; kind: "vnc"; icon: string; connect: () => Promise<VncView> }
  | { id: string; title: string; kind: "rdp"; icon: string; connect: () => Promise<RdpView> };
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
  try { if (await HasRdpDemo()) openRdp("lab: rdp demo", () => ConnectRdpDemo()); } catch { /* none */ }
}

onMounted(async () => {
  Events.On("ssh:trust", (ev: { data: unknown }) => { trustQ.value = [...trustQ.value, ev.data as TrustReq]; });
  Events.On("ssh:password", (ev: { data: unknown }) => { passValue.value = ""; passQ.value = [...passQ.value, ev.data as PassReq]; });
  try { locked.value = await Locked(); } catch { locked.value = true; }
  if (locked.value) { loading.value = false; return; }
  await loadData();
  await openDemos();
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
    await openDemos();
  } catch (e) {
    unlockError.value = String(e);
  } finally {
    unlocking.value = false;
  }
}

// openTerm opens a terminal tab (SSH, Telnet or Serial — all stream term:out/term:close). monitor
// enables the remote-monitoring bar (SSH only).
function openTerm(title: string, icon: string, connect: () => Promise<string>, monitor = false) {
  const id = "tab" + ++seq;
  tabs.value = [...tabs.value, { id, title, kind: "ssh", icon, connect, monitor }];
  active.value = id;
}
function openFiles(title: string, icon: string, connect: () => Promise<RemoteSession>) {
  const id = "tab" + ++seq;
  tabs.value = [...tabs.value, { id, title, kind: "files", icon, connect }];
  active.value = id;
}
function openVnc(title: string, connect: () => Promise<VncView>) {
  const id = "tab" + ++seq;
  tabs.value = [...tabs.value, { id, title, kind: "vnc", icon: "vnc", connect }];
  active.value = id;
}
// Session editor (create/edit) and the tree context menu.
const editorOpen = ref(false);
const settingsOpen = ref(false);
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
type PassReq = { reqId: string; host: string; user: string };
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
    case "telnet": openTerm(s.name, "terminal", () => TelnetConnect(s.id)); break;
    case "serial": openTerm(s.name, "plug", () => SerialConnect(s.id)); break;
    case "rdp": openRdpFor(s); break;
    case "vnc": openVncFor(s); break;
    case "sftp": openSftpFor(s); break;
    case "ftp": case "ftps": openFtpFor(s); break;
    default: openTerm(s.name, "ssh", () => Connect(s.id), true); break; // ssh
  }
}
function openSftpFor(s: Session | null) { if (s) openFiles("SFTP: " + s.name, "sftp", () => SftpConnect(s.id)); }
function openFtpFor(s: Session | null) { if (s) openFiles("FTP: " + s.name, "ftp", () => FtpConnect(s.id, "explicit")); }
function openVncFor(s: Session | null) { if (s) openVnc("VNC: " + s.name, () => VncConnect(s.id)); }
function openRdp(title: string, connect: () => Promise<RdpView>) {
  const id = "tab" + ++seq;
  tabs.value = [...tabs.value, { id, title, kind: "rdp", icon: "rdp", connect }];
  active.value = id;
}
function openRdpFor(s: Session | null) { if (s) openRdp("RDP: " + s.name, () => RdpConnect(s.id)); }
function closeTab(id: string) {
  const i = tabs.value.findIndex((t) => t.id === id);
  tabs.value = tabs.value.filter((t) => t.id !== id);
  if (active.value === id) active.value = tabs.value[Math.max(0, i - 1)]?.id ?? "";
}

type Row =
  | { kind: "folder"; folder: Folder; depth: number }
  | { kind: "session"; session: Session; depth: number };
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
      out.push({ kind: "folder", folder: f, depth });
      for (const s of ses(f.id)) out.push({ kind: "session", session: s, depth: depth + 1 });
      walk(f.id, depth + 1);
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
    <header class="toolbar flex items-center gap-1 px-1.5 py-1">
      <img :src="icon('session-new')" class="tbico" title="New session" @click="newSession()" />
      <img :src="icon('ssh')" class="tbico" title="Open SSH terminal" @click="selected && openTerm(selected.name, 'ssh', () => Connect(selected!.id), true)" :class="selected ? '' : 'off'" />
      <img :src="icon('sftp')" class="tbico" title="Open SFTP file browser" @click="openSftpFor(selected)" :class="selected ? '' : 'off'" />
      <img :src="icon('ftp')" class="tbico" title="Open FTP/FTPS file browser" @click="openFtpFor(selected)" :class="selected ? '' : 'off'" />
      <img :src="icon('vnc')" class="tbico" title="Open VNC" @click="openVncFor(selected)" :class="selected ? '' : 'off'" />
      <img :src="icon('rdp')" class="tbico" title="Open RDP" @click="openRdpFor(selected)" :class="selected ? '' : 'off'" />
      <span class="mx-1 h-5 w-px bg-black/10"></span>
      <input class="qc" placeholder="Quick connect — user@host[:port]" disabled />
      <span class="ml-auto flex items-center gap-1">
        <img :src="icon('gear')" class="tbico" title="Settings" @click="settingsOpen = true" />
        <img :src="icon('help')" class="tbico" title="Help" />
      </span>
    </header>

    <div class="flex min-h-0 flex-1">
      <aside class="sidebar flex w-64 shrink-0 flex-col">
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
                @contextmenu.prevent="openFolderCtx($event, row.folder)"
              >
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
                <img :src="sessionIcon(row.session)" class="ico" />
                <span class="truncate">{{ row.session.name }}</span>
                <img v-if="row.session.password === 'saved'" :src="icon('key')" class="ml-auto h-3 w-3 opacity-70" title="password saved" />
              </div>
            </div>
          </template>
        </div>
      </aside>

      <main class="flex min-w-0 flex-1 flex-col bg-[#0b0e11]">
        <div v-if="tabs.length" class="tabbar flex items-stretch">
          <button v-for="t in tabs" :key="t.id" class="tab flex items-center gap-1.5" :class="t.id === active ? 'tab-active' : ''" @click="active = t.id">
            <img :src="tabIcon(t)" class="h-3.5 w-3.5" />
            <span class="max-w-[160px] truncate">{{ t.title }}</span>
            <span class="tab-x" @click.stop="closeTab(t.id)">×</span>
          </button>
        </div>
        <div class="relative min-h-0 flex-1">
          <template v-for="t in tabs" :key="t.id">
            <TerminalView v-if="t.kind === 'ssh'" v-show="t.id === active" :connect="t.connect" :monitor="t.monitor" @close="closeTab(t.id)" class="absolute inset-0 p-1" />
            <FileBrowser v-else-if="t.kind === 'files'" v-show="t.id === active" :connect="t.connect" class="absolute inset-0" />
            <VncCanvas v-else-if="t.kind === 'vnc'" v-show="t.id === active" :connect="t.connect" class="absolute inset-0" />
            <RdpCanvas v-else v-show="t.id === active" :connect="t.connect" class="absolute inset-0" />
          </template>
          <div v-if="!tabs.length" class="home">
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
      <span>{{ sessions.length }} sessions</span><span>·</span><span>{{ tabs.length }} open</span>
      <span class="ml-auto">rustXterm · Go + Wails</span>
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
    <SettingsDialog v-if="settingsOpen" @saved="settingsOpen = false" @close="settingsOpen = false" />

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
          <p class="dialog-sub">Enter the password for <b>{{ (passReq.user || "user") + "@" + passReq.host }}</b>.</p>
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
.app { background: #eef1f4; color: #1e1e1e; }
.toolbar { background: linear-gradient(#fbfcfd, #e9edf1); border-bottom: 1px solid #c9d1d9; }
.tbico { height: 20px; width: 20px; padding: 2px; cursor: pointer; }
.tbico:hover { background: #d7e3f4; outline: 1px solid #a9c7ef; }
.tbico.off { opacity: .4; cursor: default; }
.qc { width: 320px; height: 24px; padding: 0 8px; font-size: 12px; background: #fff; border: 1px solid #c9d1d9; }
.sidebar { background: #f3f5f8; border-right: 1px solid #c9d1d9; }
.side-head { font-size: 11px; font-weight: 700; text-transform: uppercase; letter-spacing: .04em; color: #5a6b7b; background: #e7ecf1; border-bottom: 1px solid #d6dde4; }
.count { background: #d6dde4; color: #4a5a68; font-size: 10px; font-weight: 600; padding: 0 6px; }
.treerow { display: flex; align-items: center; gap: 6px; height: 22px; font-size: 13px; cursor: default; user-select: none; }
.treerow .ico { height: 16px; width: 16px; flex: none; }
.treerow.session { cursor: pointer; padding-right: 6px; }
.treerow.session:hover { background: #d7e3f4; }
.treerow.session.sel { background: #cfe0f6; outline: 1px solid #a9c7ef; }
.treerow.folder { color: #4a5a68; font-weight: 600; }
.tabbar { background: #1b2129; border-bottom: 1px solid #2a3340; }
.tab { padding: 4px 8px; font-size: 12px; color: #9fb0c0; background: #222a33; border-right: 1px solid #2a3340; max-width: 220px; }
.tab-active { color: #e6edf3; background: #0b0e11; }
.tab-x { margin-left: 2px; padding: 0 4px; color: #7f8ea0; }
.tab-x:hover { color: #fff; background: #c0392b; }
.statusbar { height: 22px; font-size: 11px; color: #5a6b7b; background: #e7ecf1; border-top: 1px solid #c9d1d9; }
/* home (shown behind the unlock dialog and whenever no tab is open) */
.home { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 22px; background: #eef1f4; color: #2b3a4a; }
.home-brand { display: flex; align-items: center; gap: 14px; font-size: 34px; font-weight: 600; letter-spacing: .5px; }
.home-logo { height: 52px; width: 52px; filter: drop-shadow(0 2px 3px rgba(0,0,0,.18)); }
.home-actions { display: flex; gap: 14px; }
.home-btn { display: flex; align-items: center; gap: 8px; height: 38px; padding: 0 20px; font-size: 14px; color: #1e1e1e;
  background: linear-gradient(#ffffff, #eef2f6); border: 1px solid #b9c4cf; box-shadow: 0 1px 2px rgba(0,0,0,.12); cursor: pointer; }
.home-btn:hover { background: linear-gradient(#ffffff, #e3ebf3); border-color: #8fb4dd; }
.home-btn-ico { height: 18px; width: 18px; }
.recent { width: 540px; max-width: 80%; background: #f3f6f9; border: 1px solid #c9d1d9; box-shadow: 0 1px 3px rgba(0,0,0,.08); padding: 10px 14px 14px; }
.recent-title { text-align: center; font-size: 13px; font-weight: 700; color: #4a5a68; margin-bottom: 8px; }
.recent-grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 4px 16px; }
.recent-item { display: flex; align-items: center; gap: 7px; font-size: 13px; padding: 3px 4px; cursor: pointer; color: #1e1e1e; }
.recent-item:hover { background: #d7e3f4; }
.recent-ico { height: 16px; width: 16px; flex: none; }

/* locked sidebar note */
.locked-note { padding: 10px; }
.locked-head { display: flex; align-items: center; gap: 6px; font-size: 12px; color: #7a6a2a; }
.locked-unlock { margin-top: 8px; height: 26px; padding: 0 12px; font-size: 12px; background: linear-gradient(#ffffff, #eef2f6); border: 1px solid #b9c4cf; box-shadow: 0 1px 2px rgba(0,0,0,.1); cursor: pointer; }
.locked-unlock:hover { border-color: #8fb4dd; }

/* modal unlock dialog, raised off the shell with a backdrop and shadow (volume) */
.modal-backdrop { position: absolute; inset: 0; z-index: 50; display: flex; align-items: center; justify-content: center; background: rgba(20, 28, 38, .28); }
.dialog { width: 380px; background: #f2f4f7; border: 1px solid #9aa7b4; box-shadow: 0 14px 40px rgba(0,0,0,.35), 0 2px 6px rgba(0,0,0,.25); }
.dialog-title { font-size: 12px; font-weight: 700; color: #33465a; background: linear-gradient(#e9eef3, #dbe3ea); border-bottom: 1px solid #c2ccd6; padding: 7px 12px; }
.dialog-body { padding: 14px 16px 16px; }
.dialog-sub { font-size: 12.5px; color: #42536a; margin-bottom: 10px; }
.dialog-label { display: block; font-size: 12px; color: #1e1e1e; margin-bottom: 4px; }
.dialog-input { width: 100%; height: 30px; padding: 0 9px; font-size: 13px; background: #fff; border: 1px solid #a9b4c0; box-shadow: inset 0 1px 2px rgba(0,0,0,.08); }
.dialog-input:focus { outline: none; border-color: #5b9bd5; box-shadow: 0 0 0 2px rgba(91,155,213,.35); }
.dialog-err { color: #c0392b; font-size: 12px; margin-top: 8px; }
.dialog-actions { display: flex; justify-content: flex-end; gap: 8px; margin-top: 16px; }
.dbtn { display: flex; align-items: center; gap: 6px; height: 30px; padding: 0 16px; font-size: 13px; background: linear-gradient(#ffffff, #e9eef3); border: 1px solid #a9b4c0; box-shadow: 0 1px 2px rgba(0,0,0,.12); cursor: pointer; color: #1e1e1e; }
.dbtn:hover { border-color: #8fb4dd; }
.dbtn.primary { background: linear-gradient(#4a8fd1, #2f6fb5); border-color: #2a62a0; color: #fff; }
.dbtn.primary:hover { background: linear-gradient(#55a0e0, #3277c0); }
.dbtn.primary:disabled { opacity: .55; cursor: default; }
.dbtn-ico { height: 14px; width: 14px; }
.fp-head { display: flex; align-items: center; gap: 6px; font-size: 11px; font-weight: 700; color: #4a5a68; background: #e7ecf1; border: 1px solid #d6dde4; padding: 4px 8px; margin: 4px 0; }
.fp { font-family: "DejaVu Sans Mono", monospace; font-size: 12px; color: #1e3a5a; background: #fff; border: 1px solid #c2ccd6; padding: 8px 10px; word-break: break-all; margin-bottom: 10px; }
/* tree context menu */
.ctx-mask { position: fixed; inset: 0; z-index: 70; }
.ctx { position: fixed; min-width: 180px; background: #f7f9fb; border: 1px solid #9aa7b4; box-shadow: 0 8px 24px rgba(0,0,0,.28); padding: 3px; }
.ctx-item { display: flex; align-items: center; gap: 8px; width: 100%; padding: 5px 10px; font-size: 13px; background: transparent; border: none; text-align: left; cursor: pointer; color: #1e1e1e; }
.ctx-item:hover { background: #d7e3f4; }
.ctx-item.danger:hover { background: #f4d7d7; }
.ctx-ico { height: 16px; width: 16px; display: inline-flex; align-items: center; justify-content: center; }
.ctx-del { color: #c0392b; font-weight: 700; }
.ctx-sep { height: 1px; background: #d6dde4; margin: 3px 0; }
.app :deep(*), .app { border-radius: 0 !important; }
</style>

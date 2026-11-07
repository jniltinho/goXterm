<script setup lang="ts">
import { ref, computed, onMounted, shallowRef } from "vue";
import {
  Sessions, Folders, Connect, ConnectDemo, HasDemo,
  TelnetConnect, ConnectTelnetDemo, HasTelnetDemo,
  SerialConnect, ConnectSerialDemo, HasSerialDemo,
  SftpConnect, FtpConnect, ConnectSftpDemo, HasSftpDemo, ConnectFtpDemo, HasFtpDemo,
  VncConnect, ConnectVncDemo, HasVncDemo,
  RdpConnect, ConnectRdpDemo, HasRdpDemo,
  type Session, type Folder, type RemoteSession, type VncView, type RdpView,
} from "./api";
import TerminalView from "./Terminal.vue";
import FileBrowser from "./FileBrowser.vue";
import VncCanvas from "./VncView.vue";
import RdpCanvas from "./RdpView.vue";

const sessions = ref<Session[]>([]);
const folders = ref<Folder[]>([]);
const loading = ref(true);
const error = ref("");
const selected = ref<Session | null>(null);

type Tab =
  | { id: string; title: string; kind: "ssh"; icon: string; connect: () => Promise<string> }
  | { id: string; title: string; kind: "files"; icon: string; connect: () => Promise<RemoteSession> }
  | { id: string; title: string; kind: "vnc"; icon: string; connect: () => Promise<VncView> }
  | { id: string; title: string; kind: "rdp"; icon: string; connect: () => Promise<RdpView> };
const tabs = shallowRef<Tab[]>([]);
const active = ref("");
let seq = 0;

onMounted(async () => {
  try {
    [folders.value, sessions.value] = await Promise.all([Folders(), Sessions()]);
  } catch (e) {
    error.value = String(e);
  } finally {
    loading.value = false;
  }
  try { if (await HasDemo()) openTerm("lab: ssh demo", "ssh", () => ConnectDemo()); } catch { /* none */ }
  try { if (await HasTelnetDemo()) openTerm("lab: telnet demo", "terminal", () => ConnectTelnetDemo()); } catch { /* none */ }
  try { if (await HasSerialDemo()) openTerm("lab: serial demo", "plug", () => ConnectSerialDemo()); } catch { /* none */ }
  try { if (await HasSftpDemo()) openFiles("lab: sftp demo", "sftp", () => ConnectSftpDemo()); } catch { /* none */ }
  try { if (await HasFtpDemo()) openFiles("lab: ftps demo", "ftps", () => ConnectFtpDemo()); } catch { /* none */ }
  try { if (await HasVncDemo()) openVnc("lab: vnc demo", () => ConnectVncDemo()); } catch { /* none */ }
  try { if (await HasRdpDemo()) openRdp("lab: rdp demo", () => ConnectRdpDemo()); } catch { /* none */ }
});

// openTerm opens a terminal tab (SSH, Telnet or Serial — all stream term:out/term:close).
function openTerm(title: string, icon: string, connect: () => Promise<string>) {
  const id = "tab" + ++seq;
  tabs.value = [...tabs.value, { id, title, kind: "ssh", icon, connect }];
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
// openSession opens a session in the tab its protocol needs (double-click / Enter).
function openSession(s: Session) {
  switch (s.protocol) {
    case "telnet": openTerm(s.name, "terminal", () => TelnetConnect(s.id)); break;
    case "serial": openTerm(s.name, "plug", () => SerialConnect(s.id)); break;
    case "rdp": openRdpFor(s); break;
    case "vnc": openVncFor(s); break;
    case "sftp": openSftpFor(s); break;
    case "ftp": case "ftps": openFtpFor(s); break;
    default: openTerm(s.name, "ssh", () => Connect(s.id)); break; // ssh
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
    <header class="toolbar flex items-center gap-1 px-1.5 py-1">
      <img :src="icon('session-new')" class="tbico" title="New session" />
      <img :src="icon('ssh')" class="tbico" title="Open SSH terminal" @click="selected && openTerm(selected.name, 'ssh', () => Connect(selected!.id))" :class="selected ? '' : 'off'" />
      <img :src="icon('sftp')" class="tbico" title="Open SFTP file browser" @click="openSftpFor(selected)" :class="selected ? '' : 'off'" />
      <img :src="icon('ftp')" class="tbico" title="Open FTP/FTPS file browser" @click="openFtpFor(selected)" :class="selected ? '' : 'off'" />
      <img :src="icon('vnc')" class="tbico" title="Open VNC" @click="openVncFor(selected)" :class="selected ? '' : 'off'" />
      <img :src="icon('rdp')" class="tbico" title="Open RDP" @click="openRdpFor(selected)" :class="selected ? '' : 'off'" />
      <span class="mx-1 h-5 w-px bg-black/10"></span>
      <input class="qc" placeholder="Quick connect — user@host[:port]" disabled />
      <span class="ml-auto flex items-center gap-1">
        <img :src="icon('gear')" class="tbico" title="Settings" />
        <img :src="icon('help')" class="tbico" title="Help" />
      </span>
    </header>

    <div class="flex min-h-0 flex-1">
      <aside class="sidebar flex w-64 shrink-0 flex-col">
        <div class="side-head flex items-center justify-between px-2 py-1">
          <span class="flex items-center gap-1.5"><img :src="icon('session')" class="h-3.5 w-3.5" /> Sessions</span>
          <span v-if="!loading && !error" class="count">{{ sessions.length }}</span>
        </div>
        <div class="min-h-0 flex-1 overflow-y-auto">
          <div v-if="loading" class="p-3 text-xs text-neutral-500">Loading…</div>
          <div v-else-if="error" class="m-2 border border-amber-400 bg-amber-50 p-2 text-xs text-amber-800">{{ error }}</div>
          <template v-else>
            <div v-for="(row, i) in rows" :key="i">
              <div v-if="row.kind === 'folder'" class="treerow folder" :style="{ paddingLeft: 6 + row.depth * 14 + 'px' }">
                <img :src="folderIcon(row.folder)" class="ico" />
                <span class="truncate">{{ row.folder.name }}</span>
              </div>
              <div
                v-else
                class="treerow session"
                :class="selected?.id === row.session.id ? 'sel' : ''"
                :style="{ paddingLeft: 6 + row.depth * 14 + 'px' }"
                :title="'Click to select, double-click to open a terminal'"
                @click="selected = row.session"
                @dblclick="openSession(row.session)"
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
            <TerminalView v-if="t.kind === 'ssh'" v-show="t.id === active" :connect="t.connect" class="absolute inset-0 p-1" />
            <FileBrowser v-else-if="t.kind === 'files'" v-show="t.id === active" :connect="t.connect" class="absolute inset-0" />
            <VncCanvas v-else-if="t.kind === 'vnc'" v-show="t.id === active" :connect="t.connect" class="absolute inset-0" />
            <RdpCanvas v-else v-show="t.id === active" :connect="t.connect" class="absolute inset-0" />
          </template>
          <div v-if="!tabs.length" class="flex h-full items-center justify-center text-center text-neutral-500">
            <div>
              <div class="text-xl font-semibold text-neutral-300">rustXterm</div>
              <p class="mt-1 text-sm">Double-click a session for a terminal, or select one and use the SFTP button.</p>
            </div>
          </div>
        </div>
      </main>
    </div>

    <footer class="statusbar flex items-center gap-3 px-2">
      <span>{{ sessions.length }} sessions</span><span>·</span><span>{{ tabs.length }} open</span>
      <span class="ml-auto">rustXterm · Go + Wails</span>
    </footer>
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
.app :deep(*), .app { border-radius: 0 !important; }
</style>

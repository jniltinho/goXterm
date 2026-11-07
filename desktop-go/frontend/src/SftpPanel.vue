<script setup lang="ts">
import { ref, watch, computed } from "vue";
import {
  TermFiles, TermCwd, RemoteList, RemoteMkdir, RemoteRemove, RemoteRename, RemoteDownloadAs, RemoteUploadPick,
  RemoteNewFile, RemoteSetASCII, SetClipboardText, type Entry,
} from "./api";
import { askText, askConfirm } from "./ask";

// The SFTP browser side panel of the active SSH terminal tab (Rust/MobaXterm parity): an SFTP
// channel on the tab's own connection, a toolbar, the path, the listing, and at the bottom
// "Follow terminal folder" (OSC 7, else the shell's /proc/<pid>/cwd after each Enter) and
// "Remote monitoring" for the tab.
const props = defineProps<{ termId: string; cwd: string; enterTick: number; monitor: boolean }>();
const emit = defineEmits<{ "toggle-monitor": [] }>();
const follow = defineModel<boolean>("follow", { default: true });

const icon = (name: string) => `./icons/${name}.png`;
const path = ref("");
const pathInput = ref("");
const entries = ref<Entry[]>([]);
const sel = ref("");
const err = ref("");
const busy = ref(false);
const paths = new Map<string, string>(); // termId → last folder, kept when switching tabs

const join = (dir: string, name: string) => (dir.endsWith("/") ? dir + name : dir + "/" + name);
const parent = (dir: string) => (dir === "/" ? "/" : dir.replace(/\/[^/]*\/?$/, "") || "/");
const kb = (n: number) => (n / 1024).toFixed(2); // Rust "{:.2}" KB
const pad = (n: number) => String(n).padStart(2, "0");
const when = (t: number) => {
  if (!t) return "";
  const d = new Date(t * 1000); // local time, like the Rust listing
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
};

// Rust sftp_panel::file_icon: catalog file-type icon by extension; shell dotfiles get the script icon.
const SCRIPT_DOTFILES = [".bashrc", ".bash_profile", ".bash_logout", ".bash_history", ".profile", ".zshrc", ".zprofile"];
const EXT_ICONS: [string, string[]][] = [
  ["ft-executable", ["sh", "bash", "zsh", "ksh", "csh", "fish"]],
  ["ft-text", ["txt", "log", "md", "conf", "cfg", "ini", "csv", "json", "yaml", "yml", "toml", "xml"]],
  ["ft-c", ["c"]], ["ft-c-header", ["h", "hpp", "hh"]], ["ft-cpp", ["cpp", "cc", "cxx"]], ["ft-csharp", ["cs"]],
  ["ft-java", ["java", "jar", "class"]], ["ft-javascript", ["js", "mjs", "cjs", "ts"]], ["ft-php", ["php"]],
  ["ft-perl", ["pl", "pm"]], ["ft-python", ["py", "pyw"]], ["ft-tex", ["tex"]], ["ft-r", ["r"]],
  ["ft-fortran", ["f", "f77", "f90", "f95", "for"]], ["ft-rpm", ["rpm"]], ["ft-deb", ["deb"]], ["ft-apk", ["apk"]],
  ["ft-archive", ["zip", "tar", "gz", "tgz", "bz2", "xz", "7z", "rar", "zst"]],
  ["ft-disk-image", ["iso", "img", "qcow2", "vmdk", "vdi"]], ["ft-binary", ["bin", "o", "so", "a", "exe", "dll"]],
  ["ft-locked", ["pem", "key", "crt", "gpg", "asc", "kdbx"]], ["image", ["png", "jpg", "jpeg", "gif", "bmp", "svg", "webp"]],
];
function fileIcon(name: string): string {
  const lower = name.toLowerCase();
  if (SCRIPT_DOTFILES.includes(lower)) return "ft-executable";
  const dot = lower.lastIndexOf(".");
  const ext = dot > 0 ? lower.slice(dot + 1) : "";
  return EXT_ICONS.find(([, exts]) => exts.includes(ext))?.[0] ?? "ft-file";
}
const entryIcon = (e: Entry) => (e.dir ? "folder" : e.link ? "symlink" : fileIcon(e.name));

// Rust COLUMNS after the name: (key, title); click a header to sort, again to reverse.
type SortKey = "name" | "size" | "modTime" | "owner" | "group" | "mode";
const COLUMNS: [SortKey, string][] = [["size", "Size (KB)"], ["modTime", "Last modified"], ["owner", "Owner"], ["group", "Group"], ["mode", "Access"]];
const HEADERS: [SortKey, string][] = [["name", "Name"], ...COLUMNS];
const sortKey = ref<SortKey>("name");
const sortDesc = ref(false);
function sortBy(k: SortKey) { if (sortKey.value === k) sortDesc.value = !sortDesc.value; else { sortKey.value = k; sortDesc.value = false; } }
const cell = (e: Entry, k: SortKey) => (k === "size" ? (e.dir ? "" : kb(e.size)) : k === "modTime" ? when(e.modTime) : (e[k] ?? ""));

// ASCII mode (Rust toolbar "A"): text transfers convert line ends.
const ascii = ref(false);
watch(ascii, (on) => { if (props.termId) void RemoteSetASCII(props.termId, on); });

// Recent folders of the path combo.
const history = ref<string[]>([]);
const histOpen = ref(false);
const withSlash = (p: string) => (p.endsWith("/") ? p : p + "/");
const showHidden = ref(true); // Rust "Show hidden (dot) files", on by default
const sorted = computed(() => {
  const k = sortKey.value, dir = sortDesc.value ? -1 : 1;
  const cmp = (a: Entry, b: Entry) => {
    const x = a[k] ?? "", y = b[k] ?? "";
    const c = typeof x === "number" && typeof y === "number" ? x - y : String(x).localeCompare(String(y));
    return (c || a.name.localeCompare(b.name)) * dir;
  };
  return entries.value.filter((e) => showHidden.value || !e.name.startsWith("."))
    .sort((a, b) => (a.dir === b.dir ? cmp(a, b) : a.dir ? -1 : 1)); // folders first, like Rust
});
const selEntry = computed(() => entries.value.find((e) => e.name === sel.value));

async function list(dir: string) {
  if (!props.termId) return;
  busy.value = true;
  try {
    entries.value = (await RemoteList(props.termId, dir)) ?? [];
    path.value = dir;
    pathInput.value = withSlash(dir);
    history.value = [dir, ...history.value.filter((h) => h !== dir)].slice(0, 15);
    paths.set(props.termId, dir);
    sel.value = "";
    err.value = "";
  } catch (e) {
    err.value = String(e);
  } finally {
    busy.value = false;
  }
}

// Opening on a tab: reuse its last folder, else follow the terminal, else the SFTP home.
watch(
  () => props.termId,
  async (id) => {
    entries.value = [];
    err.value = "";
    if (!id) return;
    try {
      const s = await TermFiles(id);
      if (id !== props.termId) return;
      const known = paths.get(id);
      await list(known ?? (props.cwd || s.home || "/"));
      if (follow.value && !known && !props.cwd) void followNow();
    } catch (e) {
      err.value = String(e);
    }
  },
  { immediate: true },
);

async function followNow() {
  if (!props.termId) return;
  try {
    const dir = await TermCwd(props.termId);
    if (dir && dir !== path.value) await list(dir);
  } catch { /* the server does not support it (no /proc, BusyBox ps): stay */ }
}
watch(() => props.cwd, (d) => { if (follow.value && d && d !== path.value) void list(d); });
watch(() => props.enterTick, () => { if (follow.value && !props.cwd) void followNow(); });
watch(follow, (on) => { if (on) void (props.cwd ? list(props.cwd) : followNow()); });

function open(e: Entry) {
  if (e.dir) void list(join(path.value, e.name));
  else void download(e.name);
}
async function act(f: () => Promise<unknown>) {
  try { await f(); err.value = ""; } catch (e) { err.value = String(e); }
}
const download = (name = sel.value) => name && act(() => RemoteDownloadAs(props.termId, join(path.value, name)));
const upload = () => act(async () => { if (await RemoteUploadPick(props.termId, path.value)) await list(path.value); });
async function mkdir() {
  const n = await askText("New folder", "Folder name:");
  if (n) await act(async () => { await RemoteMkdir(props.termId, join(path.value, n)); await list(path.value); });
}
async function newFile() {
  const n = await askText("New file", "File name:");
  if (n) await act(async () => { await RemoteNewFile(props.termId, join(path.value, n)); await list(path.value); });
}
async function remove() {
  const name = sel.value;
  if (name && (await askConfirm("Confirm delete", `Delete "${name}"?`, "Delete")))
    await act(async () => { await RemoteRemove(props.termId, join(path.value, name)); await list(path.value); });
}
async function rename() {
  const old = sel.value;
  const n = old && (await askText("Rename", "New name:", old));
  if (n && n !== old)
    await act(async () => { await RemoteRename(props.termId, join(path.value, old), join(path.value, n)); await list(path.value); });
}
// Row menu (Rust sftp_panel row menu): Open, Download, Rename F2, Delete Del, Copy path.
const menu = ref<{ x: number; y: number; e: Entry } | null>(null);
function rowMenu(ev: MouseEvent, e: Entry) {
  sel.value = e.name;
  menu.value = { x: ev.clientX, y: ev.clientY, e };
}
function menuDo(f: () => unknown) { menu.value = null; void f(); }
function listKey(ev: KeyboardEvent) {
  if (ev.key === "F2" && sel.value) { ev.preventDefault(); void rename(); }
  else if (ev.key === "Delete" && sel.value) { ev.preventDefault(); void remove(); }
  else if (ev.key === "Enter" && selEntry.value) { ev.preventDefault(); open(selEntry.value); }
}
</script>

<template>
  <div class="sftp flex min-h-0 flex-1 flex-col">
    <div class="sp-head"><img :src="icon('sftp')" class="sp-hico" /> SFTP browser</div>
    <template v-if="termId">
      <div class="sp-tools">
        <!-- Rust sftp_panel toolbar: same icons, order and tooltips -->
        <button class="sp-tb" title="Parent directory" @click="list(parent(path))"><img :src="icon('folder-up')" /></button>
        <button class="sp-tb" title="Download selected files" :disabled="!selEntry || selEntry.dir" @click="download()"><img :src="icon('download')" /></button>
        <button class="sp-tb" title="Upload to current folder" @click="upload"><img :src="icon('upload')" /></button>
        <button class="sp-tb" title="Refresh folder" @click="list(path)"><img :src="icon('restart')" /></button>
        <button class="sp-tb" title="Create new directory" @click="mkdir"><img :src="icon('new-folder')" /></button>
        <button class="sp-tb" title="Create new file" @click="newFile"><img :src="icon('file')" /></button>
        <button class="sp-tb" title="Delete selected files" :disabled="!sel" @click="remove"><img :src="icon('cancel')" /></button>
        <button class="sp-tb" :class="ascii ? 'on' : ''" title="ASCII mode" @click="ascii = !ascii"><img :src="icon('font')" /></button>
        <button class="sp-tb" :class="showHidden ? 'on' : ''" title="Show hidden (dot) files" @click="showHidden = !showHidden"><img :src="icon('layout-sidebar')" /></button>
      </div>
      <div class="sp-pathrow">
        <input v-model="pathInput" class="sp-path" spellcheck="false" aria-label="Path" @keydown.enter="list(pathInput.replace(/(.)\/$/, '$1'))" />
        <button class="sp-hist" title="Recent folders" @click="histOpen = !histOpen"></button>
        <div v-if="histOpen" class="sp-menu-bg" @mousedown="histOpen = false"></div>
        <div v-if="histOpen" class="sp-histpop">
          <span v-if="!history.length" class="muted">No recent folders</span>
          <button v-for="h in history" :key="h" @click="histOpen = false; list(h)">{{ withSlash(h) }}</button>
        </div>
      </div>
      <!-- Rust listing: Name + COLUMNS, 20 px header (click to sort), 18 px rows, scrolls sideways -->
      <div class="sp-scroll min-h-0 flex-1">
        <div class="sp-cols">
          <button v-for="[k, t] in HEADERS" :key="k" :class="'h-' + k" @click="sortBy(k)">
            <img v-if="sortKey === k" :src="icon(sortDesc ? 'ft-sort-desc' : 'ft-sort-asc')" />{{ t }}
          </button>
        </div>
        <div class="sp-list" :class="busy ? 'busy' : ''" tabindex="0" @keydown="listKey">
          <div class="sp-row" @dblclick="list(parent(path))"><span class="c-name"><img :src="icon('ft-folder-up')" class="sp-ico" />..</span></div>
          <div
            v-for="e in sorted"
            :key="e.name"
            class="sp-row"
            :class="sel === e.name ? 'sel' : ''"
            :title="e.name"
            @click="sel = e.name"
            @dblclick="open(e)"
            @contextmenu.prevent="rowMenu($event, e)"
          >
            <span class="c-name"><img :src="icon(entryIcon(e))" class="sp-ico" /><span class="truncate">{{ e.name }}</span></span>
            <span v-for="[k] in COLUMNS" :key="k" :class="'c-' + k">{{ cell(e, k) }}</span>
          </div>
        </div>
      </div>
      <div v-if="err" class="sp-err">{{ err }}</div>
    </template>
    <div v-if="menu" class="sp-menu-bg" @mousedown="menu = null" @contextmenu.prevent="menu = null"></div>
    <div v-if="menu" class="sp-menu" :style="{ left: menu.x + 'px', top: menu.y + 'px' }">
      <button @click="menuDo(() => open(menu!.e))"><img :src="icon('open')" /><span>Open</span><i>Enter</i></button>
      <button :disabled="menu.e.dir" @click="menuDo(() => download(menu!.e.name))"><img :src="icon('download')" /><span>Download</span><i></i></button>
      <button @click="menuDo(rename)"><img :src="icon('rename')" /><span>Rename</span><i>F2</i></button>
      <button @click="menuDo(remove)"><img :src="icon('delete')" /><span>Delete</span><i>Del</i></button>
      <button @click="menuDo(() => SetClipboardText(join(path, menu!.e.name)))"><img :src="icon('copy')" /><span>Copy path</span><i></i></button>
    </div>
    <div v-if="!termId" class="sp-empty">Open an SSH session: its files show here, on the same connection.</div>
    <div class="sp-foot">
      <label class="sp-follow"><input v-model="follow" type="checkbox" :disabled="!termId" /> Follow terminal folder</label>
      <button class="sp-mon" :class="monitor ? 'on' : ''" :disabled="!termId" @click="emit('toggle-monitor')">
        <img :src="icon('monitor-pulse')" class="sp-ico" /> Remote monitoring
      </button>
    </div>
  </div>
</template>

<style scoped>
.sp-head { display: flex; align-items: center; gap: 6px; height: 26px; padding: 0 8px; font-size: 12.5px; font-weight: 600; color: var(--k-2b3a47); background: var(--k-e7ecf1); border-bottom: 1px solid var(--k-c9d1d9); }
.sp-hico { width: 16px; height: 16px; }
/* Rust tool(): 16 px icon in a 20×20 button, 1 px apart, framed only on hover or when toggled on */
.sp-tools { display: flex; flex-wrap: wrap; gap: 1px; padding: 3px 4px; border-bottom: 1px solid var(--border-subtle); background: var(--panel); }
.sp-tb { width: 20px; height: 20px; padding: 0; display: inline-flex; align-items: center; justify-content: center; background: transparent; border: 1px solid transparent; cursor: pointer; }
.sp-tb img { width: 16px; height: 16px; }
.sp-tb:hover:not(:disabled) { background: var(--hover); border-color: var(--outline); }
.sp-tb.on { background: var(--pressed); border-color: var(--border); }
.sp-tb:disabled { cursor: default; }
.sp-tb:disabled img { opacity: .35; filter: grayscale(1); }
.sp-menu-bg { position: fixed; inset: 0; z-index: 90; }
.sp-menu { position: fixed; z-index: 91; min-width: 180px; padding: 2px; background: var(--panel); border: 1px solid var(--border); box-shadow: 0 4px 12px rgba(0, 0, 0, 0.25); }
.sp-menu button { display: grid; grid-template-columns: 16px 1fr auto; align-items: center; gap: 8px; width: 100%; height: 22px; padding: 0 8px; font-size: 12px; color: var(--text); background: transparent; border: none; text-align: left; cursor: pointer; }
.sp-menu button:hover:not(:disabled) { background: var(--hover); }
.sp-menu button:disabled { color: var(--text-disabled); }
.sp-menu img { width: 16px; height: 16px; }
.sp-menu i { font-style: normal; color: var(--text-muted); }
.sp-pathrow { position: relative; display: flex; margin: 3px 4px; }
.sp-path { flex: 1; min-width: 0; height: 22px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.sp-path:focus { border-color: var(--focus); }
.sp-hist { position: relative; width: 20px; height: 22px; padding: 0; background: var(--input); border: 1px solid var(--border); border-left: none; cursor: pointer; }
.sp-hist:hover { background: var(--hover); }
.sp-hist::after { content: ""; position: absolute; left: 6px; top: 7px; width: 5px; height: 5px; border-right: 1px solid var(--text); border-bottom: 1px solid var(--text); transform: rotate(45deg); }
.sp-histpop { position: absolute; top: 23px; left: 0; right: 0; z-index: 91; padding: 2px; background: var(--panel); border: 1px solid var(--border); box-shadow: 0 4px 12px rgba(0, 0, 0, 0.25); }
.sp-histpop button { display: block; width: 100%; height: 22px; padding: 0 6px; font-size: 12px; text-align: left; color: var(--text); background: transparent; border: none; cursor: pointer; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.sp-histpop button:hover { background: var(--hover); }
.sp-histpop .muted { display: block; padding: 3px 6px; color: var(--text-muted); }
/* Rust COLUMNS: name ≥ 140 px, then 64 / 112 / 72 / 72 / 84 */
.sp-cols, .sp-row { display: grid; grid-template-columns: minmax(140px, 1fr) 64px 112px 72px 72px 84px; align-items: center; min-width: 544px; }
.sp-cols { height: 20px; background: var(--panel); border-bottom: 1px solid var(--border-subtle); }
.sp-cols button { display: flex; align-items: center; gap: 2px; height: 20px; padding: 0 4px; font-size: 12px; color: var(--text); background: transparent; border: none; border-right: 1px solid var(--border-subtle); text-align: left; cursor: pointer; white-space: nowrap; overflow: hidden; }
.sp-cols button:hover { background: var(--hover); }
.sp-cols img { width: 12px; height: 12px; }
.sp-row { height: 18px; font-size: 12px; color: var(--text); cursor: default; user-select: none; }
.sp-row > span { padding: 0 4px; white-space: nowrap; overflow: hidden; }
.sp-row:hover { background: var(--hover); }
.sp-row.sel .c-name { background: var(--selection); color: var(--selection-text); }
.c-name { display: flex; align-items: center; gap: 4px; min-width: 0; }
.sp-ico { width: 16px; height: 16px; flex: none; }
.c-size { text-align: right; }
.c-modTime, .c-owner, .c-group, .c-mode { color: var(--text-muted); }
.c-mode { font-family: "DejaVu Sans Mono", monospace; font-size: 11px; }
.sp-list { outline: none; }
.sp-list.busy { opacity: .6; }
.sp-scroll { overflow: auto; }
.sp-scroll .sp-cols { position: sticky; top: 0; z-index: 1; }
.sp-err { padding: 4px 8px; font-size: 11.5px; color: var(--k-c0392b); border-top: 1px solid var(--k-d6dde4); }
.sp-empty { flex: 1; padding: 12px 10px; font-size: 12px; color: var(--k-6b7a8a); }
.sp-foot { display: flex; flex-direction: column; gap: 4px; padding: 6px; border-top: 1px solid var(--k-c9d1d9); background: var(--k-eef1f4); }
.sp-follow { display: flex; align-items: center; gap: 6px; font-size: 12.5px; color: var(--k-2b3a47); }
.sp-mon { display: flex; align-items: center; gap: 6px; height: 26px; padding: 0 8px; font-size: 12.5px; color: var(--k-2b3a47); background: linear-gradient(var(--k-ffffff), var(--k-e9eef3)); border: 1px solid var(--k-a9b4c0); cursor: pointer; text-align: left; }
.sp-mon.on { background: var(--k-cfe0f6); border-color: var(--k-6aa0da); }
.sp-mon:disabled { opacity: .5; cursor: default; }
</style>

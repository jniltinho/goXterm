<script setup lang="ts">
import { ref, watch, computed } from "vue";
import {
  TermFiles, TermCwd, RemoteList, RemoteMkdir, RemoteRemove, RemoteRename, RemoteDownloadAs, RemoteUploadPick,
  type Entry,
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
const kb = (n: number) => (n / 1024).toFixed(2).replace(/\.?0+$/, "");
const when = (t: number) => (t ? new Date(t * 1000).toISOString().slice(0, 16).replace("T", " ") : "");
const sorted = computed(() =>
  [...entries.value].sort((a, b) => (a.dir === b.dir ? a.name.localeCompare(b.name) : a.dir ? -1 : 1)),
);

async function list(dir: string) {
  if (!props.termId) return;
  busy.value = true;
  try {
    entries.value = (await RemoteList(props.termId, dir)) ?? [];
    path.value = pathInput.value = dir;
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
</script>

<template>
  <div class="sftp flex min-h-0 flex-1 flex-col">
    <div class="sp-head"><img :src="icon('sftp')" class="sp-hico" /> SFTP browser</div>
    <template v-if="termId">
      <div class="sp-tools">
        <button class="sp-tb" title="Parent folder" @click="list(parent(path))"><img :src="icon('folder-up')" /></button>
        <button class="sp-tb" title="Download the selected file" :disabled="!sel" @click="download()"><img :src="icon('download')" /></button>
        <button class="sp-tb" title="Upload a file here" @click="upload"><img :src="icon('upload')" /></button>
        <button class="sp-tb" title="Refresh" @click="list(path)"><img :src="icon('refresh')" /></button>
        <button class="sp-tb" title="New folder" @click="mkdir"><img :src="icon('new-folder')" /></button>
        <button class="sp-tb" title="Rename" :disabled="!sel" @click="rename"><img :src="icon('rename')" /></button>
        <button class="sp-tb" title="Delete" :disabled="!sel" @click="remove"><img :src="icon('delete')" /></button>
      </div>
      <input v-model="pathInput" class="sp-path" spellcheck="false" @keydown.enter="list(pathInput)" />
      <div class="sp-scroll min-h-0 flex-1"><div class="sp-cols"><span class="c-name">Name</span><span class="c-size">Size (KB)</span><span class="c-time">Last modified</span></div>
      <div class="sp-list" :class="busy ? 'busy' : ''">
        <div class="sp-row" @dblclick="list(parent(path))"><img :src="icon('ft-folder-up')" class="sp-ico" /><span class="c-name">..</span></div>
        <div
          v-for="e in sorted"
          :key="e.name"
          class="sp-row"
          :class="sel === e.name ? 'sel' : ''"
          :title="e.name"
          @click="sel = e.name"
          @dblclick="open(e)"
        >
          <img :src="icon(e.dir ? 'ft-folder' : 'ft-file')" class="sp-ico" />
          <span class="c-name truncate">{{ e.name }}</span>
          <span class="c-size">{{ e.dir ? "" : kb(e.size) }}</span>
          <span class="c-time">{{ when(e.modTime) }}</span>
        </div>
      </div>
      </div>
      <div v-if="err" class="sp-err">{{ err }}</div>
    </template>
    <div v-else class="sp-empty">Open an SSH session: its files show here, on the same connection.</div>
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
.sp-tools { display: flex; gap: 1px; padding: 3px 4px; border-bottom: 1px solid var(--k-d6dde4); background: var(--k-f3f5f8); }
.sp-tb { width: 24px; height: 22px; display: inline-flex; align-items: center; justify-content: center; background: transparent; border: 1px solid transparent; cursor: pointer; }
.sp-tb img { width: 16px; height: 16px; }
.sp-tb:hover:not(:disabled) { background: var(--k-d7e3f4); border-color: var(--k-a9c7ef); }
.sp-tb:disabled { opacity: .35; cursor: default; }
.sp-path { margin: 4px; height: 24px; padding: 0 6px; font-size: 12px; background: var(--k-ffffff); border: 1px solid var(--k-a9b4c0); outline: none; }
.sp-path:focus { border-color: var(--k-6aa0da); }
.sp-cols, .sp-row { display: grid; grid-template-columns: 18px minmax(110px, 1fr) 58px 100px; align-items: center; gap: 4px; padding: 0 6px; min-width: 310px; }
.sp-cols { grid-template-columns: minmax(132px, 1fr) 58px 100px; height: 22px; font-size: 11.5px; color: var(--k-4a5a68); background: var(--k-eef1f4); border-top: 1px solid var(--k-d6dde4); border-bottom: 1px solid var(--k-d6dde4); }
.sp-cols .c-name { padding-left: 22px; }
.sp-row { height: 21px; font-size: 12px; color: var(--k-1e2a36); cursor: default; user-select: none; }
.sp-row:hover { background: var(--k-e3ebf3); }
.sp-row.sel { background: var(--k-cfe0f6); }
.sp-ico { width: 16px; height: 16px; }
.c-size { text-align: right; color: var(--k-4a5a68); }
.c-time { color: var(--k-6b7a8a); font-size: 11.5px; white-space: nowrap; overflow: hidden; }
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

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from "vue";
import {
  LocalHome, LocalList, RemoteList, RemoteDownload, RemoteUpload, RemoteDisconnect,
  type Entry, type RemoteSession,
} from "./api";

const props = defineProps<{ connect: () => Promise<RemoteSession> }>();

const localPath = ref("/");
const localEntries = ref<Entry[]>([]);
const localErr = ref("");
const remotePath = ref(".");
const remoteEntries = ref<Entry[]>([]);
const remoteErr = ref("");
const sel = ref<{ side: "local" | "remote"; name: string } | null>(null);
const busy = ref("");
let tabId = "";

const parent = (p: string) => {
  const i = p.replace(/\/+$/, "").lastIndexOf("/");
  return i <= 0 ? "/" : p.slice(0, i);
};
const join = (p: string, n: string) => (p.endsWith("/") ? p : p + "/") + n;
const withUp = (list: Entry[]): Entry[] => [{ name: "..", dir: true, size: 0, mode: "", modTime: 0 }, ...list];
const fmtSize = (n: number) =>
  n < 1024 ? n + " B" : n < 1048576 ? (n / 1024).toFixed(1) + " KB" : (n / 1048576).toFixed(1) + " MB";
const icon = (name: string) => `./icons/${name}.png`;
const entryIcon = (e: Entry) => (e.dir ? icon("folder") : icon("file"));

async function loadLocal() {
  try { localEntries.value = await LocalList(localPath.value); localErr.value = ""; }
  catch (e) { localErr.value = String(e); }
}
async function loadRemote() {
  try { remoteEntries.value = await RemoteList(tabId, remotePath.value); remoteErr.value = ""; }
  catch (e) { remoteErr.value = String(e); }
}

onMounted(async () => {
  localPath.value = await LocalHome();
  await loadLocal();
  try {
    const s = await props.connect();
    tabId = s.tabId;
    remotePath.value = s.home || ".";
    await loadRemote();
  } catch (e) { remoteErr.value = String(e); }
});
onBeforeUnmount(() => { if (tabId) void RemoteDisconnect(tabId); });

function clickLocal(e: Entry) {
  if (e.dir) { localPath.value = e.name === ".." ? parent(localPath.value) : join(localPath.value, e.name); sel.value = null; loadLocal(); }
  else sel.value = { side: "local", name: e.name };
}
function clickRemote(e: Entry) {
  if (e.dir) { remotePath.value = e.name === ".." ? parent(remotePath.value) : join(remotePath.value, e.name); sel.value = null; loadRemote(); }
  else sel.value = { side: "remote", name: e.name };
}
async function upload() {
  if (sel.value?.side !== "local") return;
  busy.value = "Uploading " + sel.value.name + " →"; const name = sel.value.name;
  try { await RemoteUpload(tabId, join(localPath.value, name), remotePath.value); await loadRemote(); }
  catch (e) { remoteErr.value = String(e); }
  busy.value = "";
}
async function download() {
  if (sel.value?.side !== "remote") return;
  busy.value = "Downloading " + sel.value.name + " ←"; const name = sel.value.name;
  try { await RemoteDownload(tabId, join(remotePath.value, name), localPath.value); await loadLocal(); }
  catch (e) { localErr.value = String(e); }
  busy.value = "";
}
</script>

<template>
  <div class="fb flex h-full flex-col">
    <div class="flex min-h-0 flex-1">
      <!-- local pane -->
      <section class="pane">
        <header class="pane-head"><img :src="icon('folder-home')" class="h-4 w-4" /> Local <span class="path">{{ localPath }}</span></header>
        <div v-if="localErr" class="err">{{ localErr }}</div>
        <ul class="list">
          <li v-for="e in withUp(localEntries)" :key="'l'+e.name" class="row" :class="sel?.side==='local' && sel.name===e.name ? 'sel' : ''" @click="clickLocal(e)">
            <img :src="entryIcon(e)" class="ico" />
            <span class="name">{{ e.name }}</span>
            <span class="size">{{ e.dir ? "" : fmtSize(e.size) }}</span>
          </li>
        </ul>
      </section>

      <!-- transfer buttons -->
      <div class="mid flex flex-col items-center justify-center gap-2">
        <button class="xfer" :disabled="sel?.side!=='local'" title="Upload to remote" @click="upload">▶</button>
        <button class="xfer" :disabled="sel?.side!=='remote'" title="Download to local" @click="download">◀</button>
      </div>

      <!-- remote pane -->
      <section class="pane">
        <header class="pane-head"><img :src="icon('sftp')" class="h-4 w-4" /> Remote <span class="path">{{ remotePath }}</span></header>
        <div v-if="remoteErr" class="err">{{ remoteErr }}</div>
        <ul class="list">
          <li v-for="e in withUp(remoteEntries)" :key="'r'+e.name" class="row" :class="sel?.side==='remote' && sel.name===e.name ? 'sel' : ''" @click="clickRemote(e)">
            <img :src="entryIcon(e)" class="ico" />
            <span class="name">{{ e.name }}</span>
            <span class="size">{{ e.dir ? "" : fmtSize(e.size) }}</span>
          </li>
        </ul>
      </section>
    </div>
    <footer class="fb-status">{{ busy || (localEntries.length + " local · " + remoteEntries.length + " remote") }}</footer>
  </div>
</template>

<style scoped>
.fb { background: #f3f5f8; color: #1e1e1e; }
.pane { display: flex; min-width: 0; flex: 1; flex-direction: column; background: #fff; border: 1px solid #c9d1d9; }
.pane-head { display: flex; align-items: center; gap: 6px; padding: 4px 8px; font-size: 12px; font-weight: 600; background: #e7ecf1; border-bottom: 1px solid #d6dde4; }
.pane-head .path { margin-left: auto; font-weight: 400; color: #5a6b7b; font-family: monospace; max-width: 55%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.list { flex: 1; overflow-y: auto; }
.row { display: flex; align-items: center; gap: 6px; height: 22px; padding: 0 8px; font-size: 13px; cursor: pointer; }
.row:hover { background: #d7e3f4; }
.row.sel { background: #cfe0f6; outline: 1px solid #a9c7ef; }
.row .ico { height: 16px; width: 16px; flex: none; }
.row .name { flex: 1; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.row .size { color: #6b7a88; font-size: 11px; font-variant-numeric: tabular-nums; }
.mid { width: 48px; background: #eef1f4; }
.xfer { width: 30px; height: 26px; font-size: 13px; background: #fbfcfd; border: 1px solid #c9d1d9; cursor: pointer; }
.xfer:hover:not(:disabled) { background: #d7e3f4; }
.xfer:disabled { opacity: .4; cursor: default; }
.err { margin: 6px; padding: 6px; font-size: 12px; color: #8a4b00; background: #fff4e5; border: 1px solid #f0c38a; }
.fb-status { height: 22px; display: flex; align-items: center; padding: 0 8px; font-size: 11px; color: #5a6b7b; background: #e7ecf1; border-top: 1px solid #d6dde4; }
</style>

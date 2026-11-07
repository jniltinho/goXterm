<script setup lang="ts">
import { askConfirm } from "./ask";
import { ref, computed, onMounted, onBeforeUnmount } from "vue";
import { Events } from "@wailsio/runtime";
import {
  Tunnels, TunnelSave, TunnelDelete, TunnelStart, TunnelStop, TunnelStartAll, TunnelStopAll,
  type Tunnel, type Session,
} from "./api";

// The tunnels window (MobaSSHTunnel parity): list saved forwards, add/edit, per-row start/stop and
// start-all/stop-all. Each tunnel rides a saved SSH session (the gateway), chosen by `via`.
const props = defineProps<{ sessions: Session[] }>();
const emit = defineEmits<{ close: [] }>();

const list = ref<Tunnel[]>([]);
const err = ref("");
const gateways = computed(() => props.sessions.filter((s) => s.protocol === "ssh"));

const blank = (): Tunnel => ({
  name: "", kind: "local", via: gateways.value[0]?.id ?? 0,
  bind_addr: "127.0.0.1", bind_port: 0, dest_host: "", dest_port: 0,
  exposure_confirmed: false, autostart: false,
});
const editing = ref<Tunnel | null>(null); // null = not editing; object = the form model
const original = ref<{ via: number; name: string } | null>(null); // key before an edit (for rename)

async function refresh() {
  try { list.value = await Tunnels(); err.value = ""; }
  catch (e) { err.value = String(e); }
}

let off: (() => void) | undefined;
onMounted(async () => {
  await refresh();
  off = Events.On("tunnel:state", (ev: { data: { via: number; name: string; state: string } }) => {
    const d = ev.data;
    const t = list.value.find((x) => x.via === d.via && x.name === d.name);
    if (t) t.running = d.state === "running" || d.state === "starting";
    if (d.state === "error") refresh();
  });
});
onBeforeUnmount(() => off?.());

function newTunnel() { original.value = null; editing.value = blank(); }
function edit(t: Tunnel) { original.value = { via: t.via, name: t.name }; editing.value = { ...t }; }

async function save() {
  const t = editing.value!;
  if (!t.name.trim()) { err.value = "name is required"; return; }
  if (!t.via) { err.value = "pick an SSH gateway session"; return; }
  try {
    // A rename changes the (via,name) key: delete the old row first.
    if (original.value && (original.value.via !== t.via || original.value.name !== t.name))
      await TunnelDelete(original.value.via, original.value.name);
    await TunnelSave(JSON.stringify(t));
    editing.value = null; original.value = null;
    await refresh();
  } catch (e) { err.value = String(e); }
}

async function del(t: Tunnel) {
  if (!(await askConfirm("Delete tunnel", `Delete tunnel "${t.name}"?`, "Delete"))) return;
  try { await TunnelDelete(t.via, t.name); await refresh(); } catch (e) { err.value = String(e); }
}
async function toggle(t: Tunnel) {
  try {
    if (t.running) await TunnelStop(t.via, t.name);
    else await TunnelStart(JSON.stringify(t));
  }
  catch (e) { err.value = String(e); }
  await refresh();
}
async function startAll() { try { await TunnelStartAll(); } catch (e) { err.value = String(e); } await refresh(); }
async function stopAll() { try { await TunnelStopAll(); } catch (e) { err.value = String(e); } await refresh(); }

const gwName = (id: number) => gateways.value.find((s) => s.id === id)?.name ?? `#${id}`;
const arrow = (t: Tunnel) =>
  t.kind === "dynamic" ? `SOCKS5 :${t.bind_port}`
  : t.kind === "remote" ? `remote :${t.bind_port} → ${t.dest_host}:${t.dest_port}`
  : `:${t.bind_port} → ${t.dest_host}:${t.dest_port}`;
</script>

<template>
  <div class="st-backdrop">
    <div class="st">
      <div class="st-title">SSH Tunnels — port forwarding</div>
      <div class="st-body">
        <div v-if="err" class="st-err">{{ err }}</div>

        <!-- editor form -->
        <div v-if="editing" class="form">
          <div class="field"><label>Name</label><input v-model="editing.name" class="in" placeholder="my-db-tunnel" /></div>
          <div class="field"><label>Type</label>
            <select v-model="editing.kind" class="in in-sm">
              <option value="local">Local</option><option value="remote">Remote</option><option value="dynamic">Dynamic (SOCKS5)</option>
            </select>
          </div>
          <div class="field"><label>Via (SSH gateway)</label>
            <select v-model.number="editing.via" class="in">
              <option v-if="!gateways.length" :value="0">— no SSH session saved —</option>
              <option v-for="g in gateways" :key="g.id" :value="g.id">{{ g.name }} ({{ g.user }}@{{ g.host }})</option>
            </select>
          </div>
          <div class="field"><label>Listen</label>
            <input v-model="editing.bind_addr" class="in in-sm" style="width:120px" /> :
            <input type="number" v-model.number="editing.bind_port" class="in in-sm" style="width:80px" placeholder="0=auto" />
          </div>
          <div class="field" v-if="editing.kind !== 'dynamic'"><label>{{ editing.kind === 'remote' ? 'Forward to (local)' : 'Destination' }}</label>
            <input v-model="editing.dest_host" class="in" placeholder="127.0.0.1" /> :
            <input type="number" v-model.number="editing.dest_port" class="in in-sm" style="width:80px" />
          </div>
          <label class="row" v-if="!['127.0.0.1','::1','localhost'].includes(editing.bind_addr)">
            <input type="checkbox" v-model="editing.exposure_confirmed" /> Expose on a non-loopback address (I understand the risk)
          </label>
          <div class="form-foot">
            <button class="dbtn primary" @click="save">Save</button>
            <button class="dbtn" @click="editing = null">Cancel</button>
          </div>
        </div>

        <!-- list -->
        <template v-else>
          <div v-if="!list.length" class="st-note">No tunnels yet. Create one bound to a saved SSH session.</div>
          <div v-for="t in list" :key="t.via + '/' + t.name" class="tun-row">
            <span class="dot" :class="t.running ? 'on' : ''" :title="t.running ? 'running' : 'stopped'"></span>
            <span class="tun-name">{{ t.name }}</span>
            <span class="tun-meta">{{ arrow(t) }} · via {{ gwName(t.via) }}</span>
            <span class="tun-act">
              <button class="mini" @click="toggle(t)">{{ t.running ? "Stop" : "Start" }}</button>
              <button class="mini" @click="edit(t)">Edit</button>
              <button class="mini danger" @click="del(t)">✕</button>
            </span>
          </div>
        </template>
      </div>
      <div class="st-actions">
        <template v-if="!editing">
          <button class="dbtn" @click="newTunnel" :disabled="!gateways.length">New tunnel</button>
          <button class="dbtn" @click="startAll" :disabled="!list.length">Start all</button>
          <button class="dbtn" @click="stopAll" :disabled="!list.length">Stop all</button>
          <button class="dbtn primary" @click="emit('close')">Close</button>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.st-backdrop { position: absolute; inset: 0; z-index: 60; display: flex; align-items: center; justify-content: center; background: rgba(20,28,38,.3); }
.st { width: 560px; max-width: 92%; background: var(--k-f2f4f7); border: 1px solid var(--k-9aa7b4); box-shadow: 0 16px 44px rgba(0,0,0,.4), 0 2px 6px rgba(0,0,0,.25); color: var(--k-1e1e1e); }
.st-title { font-size: 12px; font-weight: 700; color: var(--k-33465a); background: linear-gradient(var(--k-e9eef3), var(--k-dbe3ea)); border-bottom: 1px solid var(--k-c2ccd6); padding: 7px 12px; }
.st-body { padding: 14px 16px; min-height: 160px; max-height: 50vh; overflow-y: auto; display: flex; flex-direction: column; gap: 8px; background: var(--k-f7f9fb); }
.st-err { color: var(--k-c0392b); font-size: 12px; }
.st-note { font-size: 12px; color: var(--k-7a8794); }
.field { display: flex; align-items: center; gap: 8px; }
.field label { font-size: 13px; min-width: 130px; }
.row { display: flex; align-items: center; gap: 8px; font-size: 12.5px; }
.in { height: 26px; padding: 0 8px; font-size: 13px; background: var(--k-ffffff); border: 1px solid var(--k-a9b4c0); }
.in-sm { width: 120px; }
.form { display: flex; flex-direction: column; gap: 9px; }
.form-foot { display: flex; gap: 8px; margin-top: 6px; }
.tun-row { display: flex; align-items: center; gap: 8px; font-size: 13px; padding: 5px 2px; border-bottom: 1px solid var(--k-e3e9ee); }
.tun-name { font-weight: 600; color: var(--k-1e3a5a); }
.tun-meta { color: var(--k-6b7a8a); font-size: 11.5px; }
.tun-act { margin-left: auto; display: flex; gap: 4px; }
.dot { width: 9px; height: 9px; border-radius: 50%; background: var(--k-c0392b); box-shadow: inset 0 0 0 1px rgba(0,0,0,.2); }
.dot.on { background: var(--k-3f8b50); }
.mini { height: 22px; padding: 0 9px; font-size: 11.5px; background: linear-gradient(var(--k-ffffff), var(--k-e9eef3)); border: 1px solid var(--k-a9b4c0); cursor: pointer; }
.mini:hover { border-color: var(--k-8fb4dd); }
.mini.danger { color: var(--k-c0392b); }
.st-actions { display: flex; justify-content: center; gap: 10px; padding: 12px; border-top: 1px solid var(--k-d6dde4); background: var(--k-eef1f4); }
.dbtn { height: 30px; padding: 0 20px; font-size: 13px; background: linear-gradient(var(--k-ffffff), var(--k-e9eef3)); border: 1px solid var(--k-a9b4c0); box-shadow: 0 1px 2px rgba(0,0,0,.12); cursor: pointer; }
.dbtn:hover { border-color: var(--k-8fb4dd); }
.dbtn:disabled { opacity: .5; cursor: default; }
.dbtn.primary { background: linear-gradient(var(--k-5aa469), var(--k-3f8b50)); border-color: var(--k-367a46); color: #fff; }
.dbtn.primary:hover { background: linear-gradient(var(--k-64b074), var(--k-459657)); }
:deep(*) { border-radius: 0 !important; }
.dot { border-radius: 50% !important; }
</style>

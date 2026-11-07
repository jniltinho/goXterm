<script setup lang="ts">
import { reactive, ref, onMounted, computed } from "vue";
import { SaveSession, SessionJSON, PickKeyFile, type Folder } from "./api";

const props = defineProps<{ sessionId: number | null; folders: Folder[] }>();
const emit = defineEmits<{ saved: [id: number]; close: [] }>();

// Open the native file picker for the SSH private key; keep the current value on cancel/error.
async function browseKey() {
  try { const p = await PickKeyFile(); if (p) form.ssh.key_file = p; } catch { /* cancelled */ }
}

const PROTOCOLS = [
  { id: "ssh", label: "SSH", icon: "ssh", port: 22 },
  { id: "telnet", label: "Telnet", icon: "terminal", port: 23 },
  { id: "sftp", label: "SFTP", icon: "sftp", port: 22 },
  { id: "ftp", label: "FTP", icon: "ftp", port: 21 },
  { id: "ftps", label: "FTPS", icon: "ftps", port: 990 },
  { id: "rdp", label: "RDP", icon: "rdp", port: 3389 },
  { id: "vnc", label: "VNC", icon: "vnc", port: 5900 },
  { id: "serial", label: "Serial", icon: "plug", port: 0 },
];

type Form = {
  id: number; name: string; protocol: string; host: string; port: number; user: string; folder: number | null;
  ssh: Record<string, unknown>; rdp: Record<string, unknown>; vnc: Record<string, unknown>;
  ftp: Record<string, unknown>; telnet: Record<string, unknown>; serial: { port: string; line: Record<string, unknown> };
};

function defaults(): Form {
  return {
    id: 0, name: "", protocol: "ssh", host: "", port: 22, user: "", folder: null,
    ssh: { key_file: "", use_agent: true, allow_password: true, allow_keyboard_interactive: true,
      x11_forwarding: false, agent_forwarding: false, compression: false, keepalive_secs: 0,
      remote_command: "", startup_dir: "", auto_reconnect: false },
    rdp: { domain: "", keyboard_layout: 0, clipboard: true, allow_legacy_tls: false, allow_standard_security: false },
    vnc: { view_only: false, scale_to_fit: true, clipboard: true },
    ftp: { tls: "implicit", passive: true, ascii: false },
    telnet: { raw: false, auto_login: false },
    serial: { port: "", line: { baud: 115200, data_bits: "8", parity: "none", stop_bits: "1", flow: "none" } },
  };
}

const form = reactive<Form>(defaults());
const raw = ref<Record<string, unknown>>({}); // full loaded JSON, to preserve unknown fields on save
const tab = ref<"advanced" | "bookmark" | "agent">("advanced");
const error = ref("");
const saving = ref(false);
const loaded = ref(props.sessionId === null);

const isSerial = computed(() => form.protocol === "serial");
const isNet = computed(() => !isSerial.value);

onMounted(async () => {
  if (props.sessionId === null) return;
  try {
    const js = await SessionJSON(props.sessionId);
    const m = JSON.parse(js) as Record<string, unknown>;
    raw.value = m;
    Object.assign(form, {
      id: (m.id as number) ?? 0, name: (m.name as string) ?? "", protocol: (m.protocol as string) ?? "ssh",
      host: (m.host as string) ?? "", port: (m.port as number) ?? 22, user: (m.user as string) ?? "",
      folder: (m.folder as number | null) ?? null,
    });
    for (const k of ["ssh", "rdp", "vnc", "ftp", "telnet"] as const) {
      if (m[k]) Object.assign(form[k], m[k]);
    }
    if (m.serial) {
      const s = m.serial as { port?: string; line?: Record<string, unknown> };
      form.serial.port = s.port ?? "";
      if (s.line) Object.assign(form.serial.line, s.line);
    }
  } catch (e) {
    error.value = String(e);
  } finally {
    loaded.value = true;
  }
});

function pickProtocol(p: typeof PROTOCOLS[number]) {
  const wasDefault = !form.port || PROTOCOLS.some((x) => x.port === form.port);
  form.protocol = p.id;
  if (wasDefault) form.port = p.port; // keep a custom port, otherwise follow the protocol default
}

function build(): Record<string, unknown> {
  const out: Record<string, unknown> = { ...raw.value };
  out.id = form.id;
  out.name = form.name || (form.user ? `${form.user}@${form.host}` : form.host) || form.serial.port;
  out.protocol = form.protocol;
  out.folder = form.folder;
  if (isSerial.value) {
    out.host = form.serial.port;
    out.port = 0;
    out.serial = { ...(raw.value.serial as object), port: form.serial.port, line: { ...form.serial.line } };
  } else {
    out.host = form.host;
    out.port = Number(form.port) || PROTOCOLS.find((p) => p.id === form.protocol)!.port;
    out.user = form.user;
  }
  const opt: Record<string, keyof Form> = { ssh: "ssh", rdp: "rdp", vnc: "vnc", ftp: "ftp", ftps: "ftp", telnet: "telnet" };
  const key = opt[form.protocol];
  if (key) out[key] = { ...(raw.value[key] as object), ...(form[key] as object) };
  return out;
}

async function save() {
  if (saving.value) return;
  if (isNet.value && !form.host.trim()) { error.value = "Remote host is required."; return; }
  if (isSerial.value && !form.serial.port.trim()) { error.value = "A serial device is required."; return; }
  error.value = "";
  saving.value = true;
  try {
    const id = await SaveSession(JSON.stringify(build()));
    emit("saved", id);
  } catch (e) {
    error.value = String(e);
  } finally {
    saving.value = false;
  }
}

const icon = (name: string) => `./icons/${name}.png`;
</script>

<template>
  <div class="editor-backdrop">
    <div class="editor">
      <div class="editor-title">Session settings</div>
      <div class="editor-body">
        <!-- protocol selector -->
        <div class="proto-row">
          <button
            v-for="p in PROTOCOLS"
            :key="p.id"
            class="proto"
            :class="form.protocol === p.id ? 'proto-on' : ''"
            :title="p.label"
            @click="pickProtocol(p)"
          >
            <img :src="icon(p.icon)" class="proto-ico" />
            <span>{{ p.label }}</span>
          </button>
        </div>

        <!-- basic settings -->
        <fieldset class="basic">
          <legend>Basic {{ form.protocol.toUpperCase() }} settings</legend>
          <div v-if="isNet" class="grid-basic">
            <label>Remote host <span class="req">*</span></label>
            <input v-model="form.host" class="in" placeholder="host or IP" />
            <label>Username</label>
            <input v-model="form.user" class="in" />
            <label>Port</label>
            <input v-model.number="form.port" type="number" class="in in-port" />
          </div>
          <div v-else class="grid-basic">
            <label>Device</label>
            <input v-model="form.serial.port" class="in" placeholder="/dev/ttyUSB0 or COM3" />
            <label>Speed</label>
            <input v-model.number="form.serial.line.baud" type="number" class="in in-port" />
          </div>
        </fieldset>

        <!-- tabs -->
        <div class="tabs">
          <button class="etab" :class="tab === 'advanced' ? 'etab-on' : ''" @click="tab = 'advanced'">Advanced settings</button>
          <button class="etab" :class="tab === 'bookmark' ? 'etab-on' : ''" @click="tab = 'bookmark'">Bookmark settings</button>
          <button class="etab" :class="tab === 'agent' ? 'etab-on' : ''" @click="tab = 'agent'">Agent settings</button>
        </div>

        <div class="tab-body">
          <!-- ADVANCED -->
          <template v-if="tab === 'advanced'">
            <div v-if="form.protocol === 'ssh'" class="adv">
              <label class="row"><input type="checkbox" v-model="form.ssh.use_agent" /> Use SSH agent</label>
              <label class="row"><input type="checkbox" v-model="form.ssh.allow_password" /> Allow password authentication</label>
              <label class="row"><input type="checkbox" v-model="form.ssh.allow_keyboard_interactive" /> Allow keyboard-interactive</label>
              <label class="row"><input type="checkbox" v-model="form.ssh.x11_forwarding" /> X11 forwarding</label>
              <label class="row"><input type="checkbox" v-model="form.ssh.agent_forwarding" /> Agent forwarding</label>
              <label class="row"><input type="checkbox" v-model="form.ssh.compression" /> Compression</label>
              <label class="row"><input type="checkbox" v-model="form.ssh.auto_reconnect" /> Reconnect automatically</label>
              <div class="field"><label>Private key file</label>
                <input v-model="form.ssh.key_file" class="in" />
                <button type="button" class="browse" @click="browseKey">Browse…</button>
              </div>
              <div class="field"><label>Execute command</label><input v-model="form.ssh.remote_command" class="in" /></div>
              <div class="field"><label>Keepalive (s, 0 = off)</label><input v-model.number="form.ssh.keepalive_secs" type="number" class="in in-port" /></div>
            </div>

            <div v-else-if="form.protocol === 'rdp'" class="adv">
              <div class="field"><label>Domain</label><input v-model="form.rdp.domain" class="in" /></div>
              <label class="row"><input type="checkbox" v-model="form.rdp.clipboard" /> Share clipboard text</label>
              <label class="row"><input type="checkbox" v-model="form.rdp.allow_legacy_tls" /> Allow legacy TLS 1.0/1.1 (old Windows servers, less secure)</label>
              <label class="row"><input type="checkbox" v-model="form.rdp.allow_standard_security" /> Allow legacy RDP security (no TLS, insecure)</label>
            </div>

            <div v-else-if="form.protocol === 'vnc'" class="adv">
              <label class="row"><input type="checkbox" v-model="form.vnc.view_only" /> View only</label>
              <label class="row"><input type="checkbox" v-model="form.vnc.scale_to_fit" /> Scale to fit</label>
              <label class="row"><input type="checkbox" v-model="form.vnc.clipboard" /> Share clipboard</label>
            </div>

            <div v-else-if="form.protocol === 'ftp' || form.protocol === 'ftps'" class="adv">
              <div class="field"><label>TLS</label>
                <select v-model="form.ftp.tls" class="in"><option value="implicit">Implicit</option><option value="explicit">Explicit (AUTH TLS)</option></select>
              </div>
              <label class="row"><input type="checkbox" v-model="form.ftp.passive" /> Passive mode</label>
              <label class="row"><input type="checkbox" v-model="form.ftp.ascii" /> ASCII transfers</label>
            </div>

            <div v-else-if="form.protocol === 'telnet'" class="adv">
              <label class="row"><input type="checkbox" v-model="form.telnet.raw" /> Raw TCP (no negotiation)</label>
              <label class="row"><input type="checkbox" v-model="form.telnet.auto_login" /> Type username/password at the prompts</label>
            </div>

            <div v-else-if="form.protocol === 'serial'" class="adv">
              <div class="field"><label>Data bits</label>
                <select v-model="form.serial.line.data_bits" class="in in-port"><option>5</option><option>6</option><option>7</option><option>8</option></select>
              </div>
              <div class="field"><label>Parity</label>
                <select v-model="form.serial.line.parity" class="in"><option value="none">None</option><option value="odd">Odd</option><option value="even">Even</option></select>
              </div>
              <div class="field"><label>Stop bits</label>
                <select v-model="form.serial.line.stop_bits" class="in in-port"><option>1</option><option>2</option></select>
              </div>
              <div class="field"><label>Flow control</label>
                <select v-model="form.serial.line.flow" class="in"><option value="none">None</option><option value="hardware">RTS/CTS</option><option value="software">XON/XOFF</option></select>
              </div>
            </div>

            <div v-else class="adv muted">No advanced settings for {{ form.protocol }}.</div>
          </template>

          <!-- BOOKMARK -->
          <template v-else-if="tab === 'bookmark'">
            <div class="adv">
              <div class="field"><label>Session name</label><input v-model="form.name" class="in" placeholder="(auto: user@host)" /></div>
              <div class="field"><label>Folder</label>
                <select v-model="form.folder" class="in">
                  <option :value="null">User sessions (root)</option>
                  <option v-for="f in folders" :key="f.id" :value="f.id">{{ f.name }}</option>
                </select>
              </div>
            </div>
          </template>

          <!-- AGENT -->
          <template v-else>
            <div class="adv muted">Agent control (Deny / Ask / Allow) is a follow-up — it is not yet stored by the Go app.</div>
          </template>
        </div>

        <div v-if="error" class="editor-err">{{ error }}</div>
      </div>

      <div class="editor-actions">
        <button class="dbtn primary" :disabled="saving" @click="save">{{ saving ? "Saving…" : "OK" }}</button>
        <button class="dbtn" @click="emit('close')">Cancel</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.editor-backdrop { position: absolute; inset: 0; z-index: 60; display: flex; align-items: center; justify-content: center; background: rgba(20, 28, 38, .3); }
.editor { width: 720px; max-width: 94%; background: var(--k-f2f4f7); border: 1px solid var(--k-9aa7b4); box-shadow: 0 16px 44px rgba(0,0,0,.4), 0 2px 6px rgba(0,0,0,.25); color: var(--k-1e1e1e); }
.editor-title { font-size: 12px; font-weight: 700; color: var(--k-33465a); background: linear-gradient(var(--k-e9eef3), var(--k-dbe3ea)); border-bottom: 1px solid var(--k-c2ccd6); padding: 7px 12px; }
.editor-body { padding: 12px 14px; }
.proto-row { display: flex; gap: 4px; flex-wrap: wrap; margin-bottom: 12px; }
.proto { display: flex; flex-direction: column; align-items: center; gap: 3px; width: 62px; padding: 6px 2px; font-size: 11px; background: var(--k-eef2f6); border: 1px solid var(--k-c5cdd6); cursor: pointer; color: var(--k-40506a); }
.proto:hover { background: var(--k-e2ebf4); }
.proto-on { background: linear-gradient(var(--k-dcebfb), var(--k-c7ddf4)); border-color: var(--k-6aa0da); color: var(--k-1e3a5a); box-shadow: inset 0 0 0 1px var(--k-9cc2ec); }
.proto-ico { height: 24px; width: 24px; }
.basic { border: 1px solid var(--k-c9d1d9); background: var(--k-f7f9fb); padding: 8px 12px 12px; margin-bottom: 12px; }
.basic legend { font-size: 11px; font-weight: 700; color: var(--k-4a5a68); padding: 0 4px; }
.grid-basic { display: grid; grid-template-columns: auto 1fr; gap: 6px 10px; align-items: center; }
.grid-basic label { font-size: 12px; justify-self: end; }
.req { color: var(--k-c0392b); }
.in { height: 26px; padding: 0 8px; font-size: 13px; background: var(--k-ffffff); border: 1px solid var(--k-a9b4c0); box-shadow: inset 0 1px 2px rgba(0,0,0,.06); width: 100%; }
.in:focus { outline: none; border-color: var(--k-5b9bd5); box-shadow: 0 0 0 2px rgba(91,155,213,.3); }
.in-port { width: 110px; }
.browse { height: 28px; padding: 0 12px; font-size: 12.5px; background: linear-gradient(var(--k-ffffff), var(--k-e9eef3)); border: 1px solid var(--k-a9b4c0); cursor: pointer; white-space: nowrap; }
.browse:hover { border-color: var(--k-8fb4dd); }
.tabs { display: flex; gap: 2px; border-bottom: 1px solid var(--k-c2ccd6); }
.etab { padding: 6px 12px; font-size: 12px; background: var(--k-e7ecf1); border: 1px solid var(--k-c2ccd6); border-bottom: none; color: var(--k-4a5a68); cursor: pointer; }
.etab-on { background: var(--k-f7f9fb); color: var(--k-1e3a5a); font-weight: 600; margin-bottom: -1px; }
.tab-body { border: 1px solid var(--k-c2ccd6); border-top: none; background: var(--k-f7f9fb); padding: 12px; min-height: 160px; }
.adv { display: flex; flex-direction: column; gap: 8px; }
.adv.muted { color: var(--k-7a8794); font-size: 12.5px; }
.row { display: flex; align-items: center; gap: 7px; font-size: 12.5px; cursor: pointer; }
.field { display: flex; align-items: center; gap: 10px; }
.field label { font-size: 12.5px; min-width: 150px; }
.editor-err { color: var(--k-c0392b); font-size: 12px; margin-top: 10px; }
.editor-actions { display: flex; justify-content: center; gap: 10px; padding: 12px; border-top: 1px solid var(--k-d6dde4); background: var(--k-eef1f4); }
.dbtn { display: flex; align-items: center; gap: 6px; height: 30px; padding: 0 22px; font-size: 13px; background: linear-gradient(var(--k-ffffff), var(--k-e9eef3)); border: 1px solid var(--k-a9b4c0); box-shadow: 0 1px 2px rgba(0,0,0,.12); cursor: pointer; color: var(--k-1e1e1e); }
.dbtn:hover { border-color: var(--k-8fb4dd); }
.dbtn.primary { background: linear-gradient(var(--k-5aa469), var(--k-3f8b50)); border-color: var(--k-367a46); color: #fff; }
.dbtn.primary:hover { background: linear-gradient(var(--k-64b074), var(--k-459657)); }
.dbtn.primary:disabled { opacity: .55; cursor: default; }
:deep(*) { border-radius: 0 !important; }
</style>

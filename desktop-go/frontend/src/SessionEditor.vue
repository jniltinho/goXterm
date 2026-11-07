<script setup lang="ts">
// Session editor, Rust crates/ui/src/session_editor.rs (design R7): fixed 900×600 "Session settings"
// dialog with the session-type tiles, the "Basic <type> settings" group, icon tabs (Advanced, Terminal,
// Network, Bookmark, Agent) over a panel with the large protocol icon, and centered OK/Cancel.
import { reactive, ref, onMounted, onBeforeUnmount, computed, watch } from "vue";
import {
  SaveSession, SessionJSON, PickKeyFile, WslAvailable, WslDistributions, SerialPorts,
  SessionPolicy, SetSessionPolicy, KnownUsers, type Folder, type WslDistribution, type Policy,
} from "./api";
import Dropdown, { type Option } from "./Dropdown.vue";
import { SCHEMES, MONO_FONTS } from "./schemes";
import { ICON_PICKER } from "./iconCatalog";

const props = defineProps<{ sessionId: number | null; folders: Folder[]; initialFolder?: number | null }>();
const emit = defineEmits<{ saved: [id: number]; close: [] }>();
const icon = (name: string) => `./icons/${name}.png`;

type Proto = { id: string; label: string; icon: string; port: number; desc: string };
const PROTOCOLS: Proto[] = [
  { id: "ssh", label: "SSH", icon: "ssh", port: 22, desc: "Secure Shell (SSH) session" },
  { id: "telnet", label: "Telnet", icon: "terminal", port: 23, desc: "Telnet or raw TCP session (unencrypted)" },
  { id: "sftp", label: "SFTP", icon: "sftp", port: 22, desc: "Secure file transfer (SFTP) session" },
  { id: "ftp", label: "FTP", icon: "ftp", port: 21, desc: "File transfer (FTP) session" },
  { id: "ftps", label: "FTPS", icon: "ftps", port: 990, desc: "File transfer over TLS (FTPS) session" },
  { id: "rdp", label: "RDP", icon: "rdp", port: 3389, desc: "Remote desktop (RDP) session" },
  { id: "vnc", label: "VNC", icon: "vnc", port: 5900, desc: "Remote desktop (VNC) session" },
  { id: "serial", label: "Serial", icon: "plug", port: 0, desc: "Serial console (COM) session" },
  { id: "wsl", label: "WSL", icon: "tux", port: 0, desc: "WSL distribution shell (Windows)" },
];
const TERMINAL = new Set(["ssh", "telnet", "serial", "wsl"]);
const TERM_TYPES = ["xterm-256color", "xterm", "vt100", "vt220", "linux", "screen-256color", "ansi"];
const BAUDS = [300, 1200, 2400, 4800, 9600, 19200, 38400, 57600, 115200, 230400, 460800, 921600, 1000000, 2000000, 3000000, 4000000];
const LAYOUTS: Option<number>[] = [[0, "Server default"], [0x0409, "English (US)"], [0x0809, "English (UK)"], [0x0407, "German"],
  [0x040c, "French"], [0x040a, "Spanish"], [0x0410, "Italian"], [0x0416, "Portuguese (Brazil)"], [0x0816, "Portuguese (Portugal)"]];
const TRI: Option<boolean | null>[] = [[null, "Use global setting"], [true, "On"], [false, "Off"]];
const LINES: Option<boolean | null>[] = [[null, "Driver default"], [true, "On"], [false, "Off"]];
const NEWLINES: Option<string>[] = [["crlf", "CR LF"], ["crnul", "CR NUL"], ["cr", "CR"]];
const ENTERS: Option<string>[] = [["cr", "CR"], ["lf", "LF"], ["crlf", "CR LF"]];
const DATA_BITS: Option<string>[] = ["5", "6", "7", "8"].map((b): Option<string> => [b, b]);
const PARITY: Option<string>[] = [["none", "None"], ["odd", "Odd"], ["even", "Even"], ["mark", "Mark"], ["space", "Space"]];
const STOP_BITS: Option<string>[] = [["1", "1"], ["2", "2"]];
const FLOWS: Option<string>[] = [["none", "None"], ["hardware", "RTS/CTS"], ["software", "XON/XOFF"]];
const CURSORS: Option<string>[] = [["block", "Block"], ["underline", "Underline"], ["beam", "Beam"]];
const FOLLOW_UI = "follow-ui"; // Rust theme::FOLLOW_UI
const SCHEME_OPTIONS: Option<string>[] = [[FOLLOW_UI, "Follow UI theme"], ...Object.keys(SCHEMES).map((n): Option<string> => [n, n])];
const FONT_OPTIONS: Option<string>[] = [["monospace", `Default (${MONO_FONTS[0]})`], ...MONO_FONTS.map((f): Option<string> => [f, f])];
const POLICIES: [string, string, string][] = [
  ["deny", "Deny", "Agents cannot open or use this session (default)."],
  ["ask", "Ask", "Every command that changes something asks you first; reads run directly."],
  ["allow", "Allow", "Agents may open the session and run commands without asking."],
];

type Obj = Record<string, any>; // eslint-disable-line @typescript-eslint/no-explicit-any -- the stored session document

// The Rust Session defaults (crates/core/src/session.rs), so a new session saves the same document.
function defaults(): Obj {
  return {
    id: 0, name: "", protocol: "ssh", host: "", port: 22, user: null, folder: props.initialFolder ?? null, icon: null,
    ssh: { key_file: null, use_agent: true, allow_password: true, allow_keyboard_interactive: true, x11_forwarding: false,
      agent_forwarding: false, compression: false, keepalive_secs: 0, remote_command: null, startup_dir: null,
      auto_reconnect: false, jump_hosts: [], keep_open_after_command: false, env: [], follow_terminal_folder: true,
      remote_monitoring: null, terminal_type: "xterm-256color" },
    rdp: { domain: null, resolution: "fittotab", keyboard_layout: 0, clipboard: true, allow_legacy_tls: false, allow_standard_security: false },
    vnc: { view_only: false, scale_to_fit: true, clipboard: true, via_ssh: null },
    ftp: { tls: "implicit", passive: true, ascii: false, startup_folder: null },
    telnet: { raw: false, auto_login: false, terminal_type: "xterm-256color", newline: "crlf", login_prompt: "login:", password_prompt: "password:" },
    serial: { port: "", line: { baud: 115200, data_bits: "8", parity: "none", stop_bits: "1", flow: "none" },
      enter: "cr", lf_to_crlf: false, local_echo: false, dtr: null, rts: null, reconnect: false },
    wsl: { distribution: null, user: null, start_folder: null, startup_commands: null },
    terminal: { font_family: null, font_size: null, cursor_shape: null, cursor_blink: null, scheme: null, scrollback_lines: null, log_file: null, log_raw: false },
  };
}
const isObj = (v: unknown): v is Obj => typeof v === "object" && v !== null && !Array.isArray(v);
function merge(base: Obj, over: Obj): Obj {
  for (const [k, v] of Object.entries(over)) base[k] = isObj(v) && isObj(base[k]) ? merge(base[k], v) : v;
  return base;
}

const s = reactive<Obj>(defaults());
const typed = ref(props.sessionId !== null);
const page = ref<"advanced" | "terminal" | "network" | "bookmark" | "agent">("advanced");
const port = ref(String(s.port));
const error = ref("");
const saving = ref(false);
const iconPicker = ref(false);
const policy = ref<Policy | null>(null);
let policyLoaded = "";
const knownUsers = ref<string[]>([]);
const usersOpen = ref(false);
const serialPorts = ref<string[]>([]);
const portsNote = ref("");
const portsOpen = ref(false);

const proto = computed(() => PROTOCOLS.find((p) => p.id === s.protocol) ?? PROTOCOLS[0]);
const isNet = computed(() => s.protocol !== "serial" && s.protocol !== "wsl");

// WSL: offered where WSL runs (or when editing a saved WSL session), as in Rust.
const wslAvailable = ref(false);
const wslDistros = ref<WslDistribution[] | null>(null);
const wslNote = ref("Listing WSL distributions...");
async function refreshWsl() {
  wslNote.value = "Listing WSL distributions...";
  try {
    wslDistros.value = await WslDistributions();
    if (!wslDistros.value?.length) { wslDistros.value = null; wslNote.value = "No WSL distribution is installed"; }
  } catch (e) {
    wslDistros.value = null;
    wslNote.value = String(e).includes("only available") ? "WSL is not installed (run: wsl --install)" : `Could not query WSL: ${e}`;
  }
}
const wslOptions = computed<Option<string | null>[]>(() => {
  const d = wslDistros.value?.find((x) => x.default)?.name;
  return [[null, d ? `Default (${d})` : "Default"], ...(wslDistros.value ?? []).map((x): Option<string | null> => [x.name, x.name])];
});
const shownProtocols = computed(() => PROTOCOLS.filter((p) => p.id !== "wsl" || wslAvailable.value || s.protocol === "wsl"));

const pages = computed(() => {
  const v: [typeof page.value, string, string][] = [["advanced", `Advanced ${proto.value.label} settings`, proto.value.icon]];
  if (TERMINAL.has(s.protocol)) v.push(["terminal", "Terminal settings", "terminal"]);
  if (s.protocol === "ssh") v.push(["network", "Network settings", "network-nodes"]);
  v.push(["bookmark", "Bookmark settings", "star"], ["agent", "Agent settings", "rocket"]);
  return v;
});

const parsedPort = computed(() => { const n = Number(port.value.trim()); return Number.isInteger(n) && n >= 1 && n <= 65535 ? n : null; });
watch(parsedPort, (p) => { if (p) s.port = p; });
const problem = computed(() => {
  if (s.protocol === "serial") return s.serial.port.trim() ? "" : "Choose or type a serial port.";
  if (s.protocol === "wsl") return "";
  if (!parsedPort.value) return "Port must be between 1 and 65535.";
  if (s.protocol === "telnet" && s.telnet.auto_login && !(s.user ?? "").trim()) return "Auto-login needs a username.";
  return "";
});
const canSave = computed(() => typed.value && !problem.value);

onMounted(async () => {
  window.addEventListener("keydown", onKey);
  try { wslAvailable.value = await WslAvailable(); } catch { /* older backend */ }
  if (wslAvailable.value) void refreshWsl();
  try { knownUsers.value = (await KnownUsers()) ?? []; } catch { /* locked */ }
  if (props.sessionId === null) return;
  try {
    merge(s, JSON.parse(await SessionJSON(props.sessionId)) as Obj);
    port.value = String(s.port);
    if (s.protocol === "serial") void listPorts();
    try { policy.value = await SessionPolicy(props.sessionId); policyLoaded = JSON.stringify(policy.value); } catch { /* not editable */ }
  } catch (e) {
    error.value = String(e);
  }
});
onBeforeUnmount(() => window.removeEventListener("keydown", onKey));

// Enter confirms (not inside the WSL startup editor or with a list open), Escape cancels.
function onKey(e: KeyboardEvent) {
  if (document.querySelector(".dd-pop")) return;
  if (e.key === "Escape") { if (usersOpen.value || portsOpen.value) { usersOpen.value = portsOpen.value = false; } else emit("close"); }
  else if (e.key === "Enter" && !(e.target instanceof HTMLTextAreaElement) && !(e.target instanceof HTMLButtonElement)) { e.preventDefault(); void save(); }
}

// Choose the session type; the port follows the type's default unless the user changed it.
function pickProtocol(p: Proto) {
  if (!typed.value || s.port === proto.value.port) { s.port = p.port; port.value = String(p.port); }
  s.protocol = p.id;
  typed.value = true;
  if ((p.id !== "ssh" && page.value === "network") || (!TERMINAL.has(p.id) && page.value === "terminal")) page.value = "advanced";
  if (p.id === "serial" && !serialPorts.value.length) void listPorts();
}
function tileKey(e: KeyboardEvent) {
  const d = e.key === "ArrowRight" ? 1 : e.key === "ArrowLeft" ? -1 : 0;
  if (!d) return;
  const list = shownProtocols.value;
  const i = Math.max(0, list.findIndex((p) => typed.value && p.id === s.protocol));
  pickProtocol(list[Math.min(list.length - 1, Math.max(0, i + d))]);
}

// Optional text fields store null when empty (Rust Option<String>).
const opt = (o: Obj, k: string) => ({ value: o[k] ?? "", set: (v: string) => { o[k] = v.trim() ? v : null; } });
const val = (e: Event) => (e.target as HTMLInputElement).value;
async function browseKey(o: Obj) { try { const p = await PickKeyFile(); if (p) o.key_file = p; } catch { /* cancelled */ } }

// FTPS: the TLS mode moves the default port unless the user chose another one.
function setTls(t: string) {
  const old = s.ftp.tls === "explicit" ? 21 : 990;
  s.ftp.tls = t;
  if (parsedPort.value === old) port.value = String(t === "explicit" ? 21 : 990);
}
const fixed = computed(() => isObj(s.rdp.resolution));
function setResolution(f: boolean) { s.rdp.resolution = f ? (fixed.value ? s.rdp.resolution : { fixed: { width: 1280, height: 800 } }) : "fittotab"; }
const viaSsh = computed({
  get: () => s.vnc.via_ssh !== null,
  set: (on: boolean) => { s.vnc.via_ssh = on ? { host: "", port: 22, user: "", key_file: null } : null; },
});

// Terminal overrides: checked rows hold a value, unchecked ones are null (global default).
const OVERRIDES: [string, string, () => unknown][] = [
  ["font_family", "Font family", () => "monospace"], ["font_size", "Font size", () => 13],
  ["cursor_shape", "Cursor shape", () => "beam"], ["cursor_blink", "Blinking cursor", () => true],
  ["scheme", "Color scheme", () => FOLLOW_UI], ["scrollback_lines", "Scrollback lines", () => 10000],
];
function toggleOverride(k: string, on: boolean, def: () => unknown) { s.terminal[k] = on ? def() : null; }
const termTypes = (v: string) => [...(TERM_TYPES.includes(v) ? [] : [v]), ...TERM_TYPES].map((t): Option<string> => [t, t]);
const folderOptions = computed<Option<number | null>[]>(() => [[null, "User sessions"], ...props.folders.map((f): Option<number | null> => [f.id, f.name])]);
const sessionIcon = computed(() => s.icon || proto.value.icon);

async function listPorts() {
  portsNote.value = "";
  try { serialPorts.value = (await SerialPorts()) ?? []; if (!serialPorts.value.length) portsNote.value = "No serial ports found."; }
  catch (e) { serialPorts.value = []; portsNote.value = String(e); }
}
const bauds = computed<Option<number>[]>(() => [...(BAUDS.includes(s.serial.line.baud) ? [] : [s.serial.line.baud]), ...BAUDS].map((b): Option<number> => [b, String(b)]));

function validate(): boolean {
  if (s.protocol === "serial") {
    s.serial.port = s.serial.port.trim();
    error.value = problem.value;
    if (!error.value && !s.name.trim()) s.name = s.serial.port;
    return !error.value;
  }
  if (s.protocol === "wsl") {
    if (!s.name.trim()) s.name = `WSL: ${s.wsl.distribution ?? "Default"}`;
    return true;
  }
  s.host = s.host.trim();
  error.value = !s.host ? "Remote host is required."
    : /[\s/]/.test(s.host) ? `Invalid host '${s.host}'.`
    : !parsedPort.value ? "Port must be between 1 and 65535."
    : s.ssh.jump_hosts.some((j: Obj) => !j.host.trim()) ? "Every jump host needs a host name."
    : s.protocol === "vnc" && s.vnc.via_ssh && !s.vnc.via_ssh.host.trim() ? "The SSH gateway needs a host name."
    : problem.value;
  if (!error.value && !s.name.trim()) s.name = s.user ? `${s.user}@${s.host}` : s.host;
  return !error.value;
}

async function save() {
  if (saving.value || !canSave.value || !validate()) return;
  saving.value = true;
  try {
    const out: Obj = JSON.parse(JSON.stringify(s));
    if (out.protocol === "serial") { out.host = out.serial.port; out.port = 0; }
    if (out.protocol === "wsl") { out.host = out.wsl.distribution ?? ""; out.port = 0; out.user = out.wsl.user; }
    const id = await SaveSession(JSON.stringify(out));
    if (policy.value && s.id && JSON.stringify(policy.value) !== policyLoaded) await SetSessionPolicy(s.id, policy.value);
    emit("saved", id);
  } catch (e) {
    error.value = String(e);
  } finally {
    saving.value = false;
  }
}
const message = computed(() => error.value || (typed.value ? problem.value : ""));
</script>

<template>
  <div class="ed-backdrop">
    <div class="ed" role="dialog" aria-label="Session settings">
      <div class="ed-title">Session settings</div>
      <div class="ed-main">
        <!-- session-type tiles -->
        <div class="tiles" tabindex="0" @keydown="tileKey">
          <button v-for="p in shownProtocols" :key="p.id" class="tile" :class="typed && s.protocol === p.id ? 'on' : ''"
            :title="p.desc" @click="pickProtocol(p)">
            <img :src="icon(p.icon)" /><span>{{ p.label }}</span>
          </button>
        </div>

        <div class="ed-body">
          <div v-if="!typed" class="choose"><img :src="icon('session')" /><span>Choose a session type...</span></div>
          <template v-else>
            <!-- Basic group -->
            <section class="grp">
              <div class="grp-h"><img :src="icon(proto.icon)" />Basic {{ proto.label }} settings</div>
              <div class="grp-b">
                <div v-if="isNet" class="basic">
                  <label>Remote host<span class="req">*</span></label>
                  <input v-model="s.host" class="in" style="width: 200px" spellcheck="false" />
                  <label class="gap">Username</label>
                  <input :value="s.user ?? ''" class="in" style="width: 160px" spellcheck="false" @input="s.user = val($event).trim() ? val($event) : null" />
                  <span class="pop-anchor">
                    <button class="ibtn" title="Saved user names" @click="usersOpen = !usersOpen"><img :src="icon('users')" /></button>
                    <div v-if="usersOpen" class="menu">
                      <button v-if="!knownUsers.length" class="mi" disabled>No saved user names</button>
                      <button v-for="u in knownUsers" :key="u" class="mi" @click="s.user = u; usersOpen = false">{{ u }}</button>
                    </div>
                  </span>
                  <label class="gap">Port</label>
                  <span class="spin">
                    <input v-model="port" class="in" :class="parsedPort ? '' : 'bad-in'" style="width: 48px" />
                    <span class="spin-b">
                      <button title="Increase port" @click="port = String(Math.min(65535, (parsedPort ?? 0) + 1))"></button>
                      <button title="Decrease port" @click="port = String(Math.max(1, (parsedPort ?? 2) - 1))"></button>
                    </span>
                  </span>
                </div>
                <div v-else-if="s.protocol === 'serial'" class="basic">
                  <label>Serial port</label>
                  <input v-model="s.serial.port" class="in" style="width: 200px" placeholder="/dev/ttyUSB0" spellcheck="false" />
                  <span class="pop-anchor">
                    <button class="ibtn" title="Detected serial ports" @click="portsOpen = !portsOpen"><img :src="icon('plug')" /></button>
                    <div v-if="portsOpen" class="menu">
                      <button v-if="portsNote" class="mi" disabled>{{ portsNote }}</button>
                      <button v-for="p in serialPorts" :key="p" class="mi" @click="s.serial.port = p; portsOpen = false">{{ p }}</button>
                    </div>
                  </span>
                  <button class="ibtn" title="Refresh serial ports" @click="listPorts"><img :src="icon('refresh')" /></button>
                  <label class="gap">Speed (bps)</label>
                  <Dropdown v-model="s.serial.line.baud" :options="bauds" :width="100" />
                  <input v-model.number="s.serial.line.baud" type="number" min="50" max="12000000" class="in" style="width: 80px" aria-label="Custom speed" />
                </div>
                <div v-else class="basic">
                  <label>Distribution</label>
                  <Dropdown v-if="wslDistros" v-model="s.wsl.distribution" :options="wslOptions" :width="240" />
                  <input v-else :value="wslNote" class="in" style="width: 300px" disabled />
                  <button class="ibtn" title="Refresh distributions" @click="refreshWsl"><img :src="icon('refresh')" /></button>
                  <label class="gap">Username</label>
                  <input v-bind="{ value: opt(s.wsl, 'user').value }" class="in" style="width: 140px" @input="opt(s.wsl, 'user').set(val($event))" />
                </div>
              </div>
            </section>

            <!-- icon tabs -->
            <div class="tabs">
              <button v-for="[id, label, ic] in pages" :key="id" class="tab" :class="page === id ? 'on' : ''" @click="page = id; iconPicker = false">
                <img :src="icon(ic)" />{{ label }}
              </button>
            </div>
            <div class="panel">
              <div class="page">
                <!-- Bookmark: icon picker -->
                <template v-if="iconPicker">
                  <button class="btn" @click="iconPicker = false">Hide icons</button>
                  <template v-for="[cat, names] in ICON_PICKER" :key="cat">
                    <b class="cat">{{ cat[0].toUpperCase() + cat.slice(1) }}</b>
                    <div class="icons">
                      <button v-for="n in names" :key="n" class="icn" :class="s.icon === n ? 'on' : ''" :title="n" @click="s.icon = n; iconPicker = false"><img :src="icon(n)" /></button>
                    </div>
                  </template>
                </template>

                <!-- Agent -->
                <template v-else-if="page === 'agent'">
                  <p>What AI agents and the rustxterm CLI may do with this session over the local control channel.</p>
                  <div class="grid" :class="policy ? '' : 'off'">
                    <template v-for="[v, l, d] in POLICIES" :key="v">
                      <label class="chk"><input type="radio" :checked="(policy?.policy ?? 'deny') === v" :disabled="!policy" @change="policy && (policy.policy = v)" /> {{ l }}</label>
                      <span>{{ d }}</span>
                    </template>
                  </div>
                  <label class="chk" :class="policy ? '' : 'off'"><input type="checkbox" :checked="policy?.content_read ?? false" :disabled="!policy" @change="policy && (policy.content_read = ($event.target as HTMLInputElement).checked)" /> Agent may read content</label>
                  <p class="small">Terminal text, screenshots and file downloads. Off by default; it can contain anything shown on the remote side.</p>
                  <p v-if="!policy" class="warn">{{ s.id ? "Unlock the session database to change the agent policy." : "Save the session first to change its agent policy." }}</p>
                  <p class="small">Agent policies can only be changed here, never by an agent or the CLI.</p>
                </template>

                <!-- Bookmark -->
                <div v-else-if="page === 'bookmark'" class="grid">
                  <label>Session name</label><input v-model="s.name" class="in w320" />
                  <label>Folder</label><Dropdown v-model="s.folder" :options="folderOptions" :width="320" />
                  <label>Session icon</label>
                  <span class="row">
                    <button class="btn" @click="iconPicker = true"><img :src="icon(sessionIcon)" /> Choose icon…</button>
                    <button v-if="s.icon" class="btn" @click="s.icon = null">Use protocol icon</button>
                  </span>
                </div>

                <!-- Network (SSH) -->
                <template v-else-if="page === 'network'">
                  <section class="grp">
                    <div class="grp-h"><img :src="icon('network-nodes')" />SSH gateway (jump hosts)</div>
                    <div class="grp-b">
                      <div v-for="(j, i) in s.ssh.jump_hosts" :key="i" class="row">
                        <input v-model="j.host" class="in" style="width: 180px" placeholder="Host" />
                        <input v-model.number="j.port" type="number" min="1" max="65535" class="in" style="width: 56px" />
                        <input v-model="j.user" class="in" style="width: 120px" placeholder="User" />
                        <input :value="j.key_file ?? ''" class="in" style="width: 200px" placeholder="Key file" @input="opt(j, 'key_file').set(val($event))" />
                        <button class="ibtn" title="Remove jump host" @click="s.ssh.jump_hosts.splice(i, 1)"><img :src="icon('delete')" /></button>
                      </div>
                      <div><button class="btn" @click="s.ssh.jump_hosts.push({ host: '', port: 22, user: '', key_file: null })">Add jump host</button></div>
                    </div>
                  </section>
                  <section class="grp">
                    <div class="grp-h">Environment variables</div>
                    <div class="grp-b">
                      <div v-for="(e, i) in s.ssh.env" :key="i" class="row">
                        <input v-model="e[0]" class="in" style="width: 180px" placeholder="Name" />
                        <input v-model="e[1]" class="in" style="width: 300px" placeholder="Value" />
                        <button class="ibtn" title="Remove variable" @click="s.ssh.env.splice(i, 1)"><img :src="icon('delete')" /></button>
                      </div>
                      <div><button class="btn" @click="s.ssh.env.push(['', ''])">Add variable</button></div>
                    </div>
                  </section>
                </template>

                <!-- Terminal overrides -->
                <template v-else-if="page === 'terminal'">
                  <p>Checked values override the global terminal defaults for this session.</p>
                  <div class="grid">
                    <template v-for="[k, label, def] in OVERRIDES" :key="k">
                      <label class="chk"><input type="checkbox" :checked="s.terminal[k] !== null" @change="toggleOverride(k, ($event.target as HTMLInputElement).checked, def)" /> {{ label }}</label>
                      <span v-if="s.terminal[k] === null" class="muted">Global default</span>
                      <Dropdown v-else-if="k === 'font_family'" v-model="s.terminal.font_family" :options="FONT_OPTIONS" :width="170" />
                      <input v-else-if="k === 'font_size'" v-model.number="s.terminal.font_size" type="number" min="6" max="48" step="0.5" class="in" style="width: 60px" />
                      <Dropdown v-else-if="k === 'cursor_shape'" v-model="s.terminal.cursor_shape" :options="CURSORS" :width="116" />
                      <input v-else-if="k === 'cursor_blink'" v-model="s.terminal.cursor_blink" type="checkbox" />
                      <Dropdown v-else-if="k === 'scheme'" v-model="s.terminal.scheme" :options="SCHEME_OPTIONS" :width="170" />
                      <input v-else v-model.number="s.terminal.scrollback_lines" type="number" min="0" max="1000000" step="100" class="in" style="width: 76px" />
                    </template>
                  </div>
                  <div v-if="s.protocol === 'ssh'" class="grid">
                    <label>Terminal type (TERM)</label><Dropdown v-model="s.ssh.terminal_type" :options="termTypes(s.ssh.terminal_type)" :width="116" />
                  </div>
                </template>

                <!-- Advanced: SSH -->
                <template v-else-if="s.protocol === 'ssh'">
                  <div class="grid">
                    <label>Private key file</label>
                    <span class="row"><input :value="s.ssh.key_file ?? ''" class="in" style="width: 300px" @input="opt(s.ssh, 'key_file').set(val($event))" /><button class="btn" @click="browseKey(s.ssh)">Browse…</button></span>
                    <label>Execute command</label><input :value="s.ssh.remote_command ?? ''" class="in w320" @input="opt(s.ssh, 'remote_command').set(val($event))" />
                    <label>Startup directory</label><input :value="s.ssh.startup_dir ?? ''" class="in w320" @input="opt(s.ssh, 'startup_dir').set(val($event))" />
                    <label>Keepalive (seconds, 0 = off)</label><input v-model.number="s.ssh.keepalive_secs" type="number" min="0" max="3600" class="in" style="width: 44px" />
                    <label>Remote monitoring</label><Dropdown v-model="s.ssh.remote_monitoring" :options="TRI" :width="150" />
                  </div>
                  <div class="checks">
                    <label class="chk"><input v-model="s.ssh.keep_open_after_command" type="checkbox" /> Do not exit after command ends</label>
                    <label class="chk"><input v-model="s.ssh.use_agent" type="checkbox" /> Use SSH agent</label>
                    <label class="chk"><input v-model="s.ssh.allow_keyboard_interactive" type="checkbox" /> Allow keyboard-interactive authentication</label>
                    <label class="chk"><input v-model="s.ssh.allow_password" type="checkbox" /> Allow password authentication</label>
                    <label class="chk"><input v-model="s.ssh.x11_forwarding" type="checkbox" /> X11 forwarding</label>
                    <label class="chk"><input v-model="s.ssh.agent_forwarding" type="checkbox" /> Agent forwarding</label>
                    <label class="chk"><input v-model="s.ssh.compression" type="checkbox" /> Compression</label>
                    <label class="chk"><input v-model="s.ssh.follow_terminal_folder" type="checkbox" /> SFTP browser follows terminal folder</label>
                    <label class="chk"><input v-model="s.ssh.auto_reconnect" type="checkbox" /> Reconnect automatically</label>
                  </div>
                </template>

                <!-- Advanced: SFTP -->
                <template v-else-if="s.protocol === 'sftp'">
                  <div class="grid">
                    <label>Private key file</label>
                    <span class="row"><input :value="s.ssh.key_file ?? ''" class="in" style="width: 300px" @input="opt(s.ssh, 'key_file').set(val($event))" /><button class="btn" @click="browseKey(s.ssh)">Browse…</button></span>
                  </div>
                  <div class="checks">
                    <label class="chk"><input v-model="s.ssh.use_agent" type="checkbox" /> Use SSH agent</label>
                    <label class="chk"><input v-model="s.ssh.allow_keyboard_interactive" type="checkbox" /> Allow keyboard-interactive authentication</label>
                    <label class="chk"><input v-model="s.ssh.allow_password" type="checkbox" /> Allow password authentication</label>
                  </div>
                </template>

                <!-- Advanced: FTP / FTPS -->
                <template v-else-if="s.protocol === 'ftp' || s.protocol === 'ftps'">
                  <div class="grid">
                    <template v-if="s.protocol === 'ftps'">
                      <label>TLS mode</label>
                      <span class="row">
                        <label class="chk"><input type="radio" :checked="s.ftp.tls === 'explicit'" @change="setTls('explicit')" /> Explicit (AUTH TLS)</label>
                        <label class="chk"><input type="radio" :checked="s.ftp.tls === 'implicit'" @change="setTls('implicit')" /> Implicit</label>
                      </span>
                    </template>
                    <label>Data connection</label>
                    <span class="row">
                      <label class="chk"><input v-model="s.ftp.passive" type="radio" :value="true" /> Passive</label>
                      <label class="chk"><input v-model="s.ftp.passive" type="radio" :value="false" /> Active</label>
                    </span>
                    <label>Transfer type</label>
                    <span class="row">
                      <label class="chk"><input v-model="s.ftp.ascii" type="radio" :value="false" /> Binary</label>
                      <label class="chk"><input v-model="s.ftp.ascii" type="radio" :value="true" /> ASCII</label>
                    </span>
                    <label>Startup folder</label><input :value="s.ftp.startup_folder ?? ''" class="in w320" @input="opt(s.ftp, 'startup_folder').set(val($event))" />
                  </div>
                  <p v-if="s.protocol === 'ftp'" class="warn">Plain FTP sends the password and files unencrypted. Prefer FTPS or SFTP.</p>
                </template>

                <!-- Advanced: RDP -->
                <template v-else-if="s.protocol === 'rdp'">
                  <div class="grid">
                    <label>Domain</label><input :value="s.rdp.domain ?? ''" class="in w320" @input="opt(s.rdp, 'domain').set(val($event))" />
                    <label>Resolution</label>
                    <span class="row">
                      <label class="chk"><input type="radio" :checked="!fixed" @change="setResolution(false)" /> Fit to tab</label>
                      <label class="chk"><input type="radio" :checked="fixed" @change="setResolution(true)" /> Fixed</label>
                      <template v-if="fixed">
                        <input v-model.number="s.rdp.resolution.fixed.width" type="number" min="200" max="8192" class="in" style="width: 56px" title="Width" />
                        <span>×</span>
                        <input v-model.number="s.rdp.resolution.fixed.height" type="number" min="200" max="8192" class="in" style="width: 56px" title="Height" />
                      </template>
                    </span>
                    <label>Keyboard layout</label><Dropdown v-model="s.rdp.keyboard_layout" :options="LAYOUTS" :width="170" />
                  </div>
                  <div class="checks">
                    <label class="chk"><input v-model="s.rdp.clipboard" type="checkbox" /> Share clipboard text</label>
                    <label class="chk"><input v-model="s.rdp.allow_legacy_tls" type="checkbox" /> Allow legacy TLS 1.0/1.1 (old Windows servers, less secure)</label>
                    <label class="chk"><input v-model="s.rdp.allow_standard_security" type="checkbox" /> Allow legacy RDP security (no TLS, insecure)</label>
                  </div>
                </template>

                <!-- Advanced: VNC -->
                <template v-else-if="s.protocol === 'vnc'">
                  <div class="checks">
                    <label class="chk"><input v-model="s.vnc.view_only" type="checkbox" /> View only (send no keyboard or mouse input)</label>
                    <label class="chk"><input v-model="s.vnc.scale_to_fit" type="checkbox" /> Scale to fit the tab</label>
                    <label class="chk"><input v-model="s.vnc.clipboard" type="checkbox" /> Share clipboard text</label>
                    <label class="chk"><input v-model="viaSsh" type="checkbox" /> Connect through an SSH gateway</label>
                  </div>
                  <div v-if="s.vnc.via_ssh" class="grid mt">
                    <label>Gateway host</label><input v-model="s.vnc.via_ssh.host" class="in w320" />
                    <label>Gateway port</label><input v-model.number="s.vnc.via_ssh.port" type="number" min="1" max="65535" class="in" style="width: 56px" />
                    <label>Gateway user</label><input v-model="s.vnc.via_ssh.user" class="in w320" />
                    <label>Gateway key file</label><input :value="s.vnc.via_ssh.key_file ?? ''" class="in w320" @input="opt(s.vnc.via_ssh, 'key_file').set(val($event))" />
                  </div>
                </template>

                <!-- Advanced: Telnet -->
                <template v-else-if="s.protocol === 'telnet'">
                  <p class="warn">Telnet sends all data, including passwords, in clear text. Prefer SSH where possible.</p>
                  <div class="grid">
                    <label>Terminal type</label><Dropdown v-model="s.telnet.terminal_type" :options="termTypes(s.telnet.terminal_type)" :width="116" />
                    <label>Enter sends</label><Dropdown v-model="s.telnet.newline" :options="NEWLINES" :width="90" />
                  </div>
                  <div class="checks">
                    <label class="chk"><input v-model="s.telnet.raw" type="checkbox" /> Raw TCP (no Telnet negotiation)</label>
                    <label class="chk"><input v-model="s.telnet.auto_login" type="checkbox" /> Log in automatically</label>
                  </div>
                  <div v-if="s.telnet.auto_login" class="grid mt">
                    <label>Login prompt</label><input v-model="s.telnet.login_prompt" class="in w320" />
                    <label>Password prompt</label><input v-model="s.telnet.password_prompt" class="in w320" />
                  </div>
                </template>

                <!-- Advanced: Serial -->
                <template v-else-if="s.protocol === 'serial'">
                  <div class="grid">
                    <label>Data bits</label><Dropdown v-model="s.serial.line.data_bits" :options="DATA_BITS" :width="70" />
                    <label>Parity</label><Dropdown v-model="s.serial.line.parity" :options="PARITY" :width="90" />
                    <label>Stop bits</label><Dropdown v-model="s.serial.line.stop_bits" :options="STOP_BITS" :width="70" />
                    <label>Flow control</label><Dropdown v-model="s.serial.line.flow" :options="FLOWS" :width="110" />
                    <label>Enter sends</label><Dropdown v-model="s.serial.enter" :options="ENTERS" :width="90" />
                    <label>DTR at open</label><Dropdown v-model="s.serial.dtr" :options="LINES" :width="130" />
                    <label>RTS at open</label><Dropdown v-model="s.serial.rts" :options="LINES" :width="130" />
                  </div>
                  <div class="checks">
                    <label class="chk"><input v-model="s.serial.lf_to_crlf" type="checkbox" /> Show received LF as CR LF</label>
                    <label class="chk"><input v-model="s.serial.local_echo" type="checkbox" /> Local echo</label>
                    <label class="chk"><input v-model="s.serial.reconnect" type="checkbox" /> Reconnect when the device returns</label>
                  </div>
                </template>

                <!-- Advanced: WSL -->
                <template v-else-if="s.protocol === 'wsl'">
                  <div class="grid">
                    <label>Startup folder</label><input :value="s.wsl.start_folder ?? ''" class="in w320" placeholder="~ or /srv/app" @input="opt(s.wsl, 'start_folder').set(val($event))" />
                  </div>
                  <p>Execute the following commands at startup:</p>
                  <textarea :value="s.wsl.startup_commands ?? ''" class="in cmds" rows="4" spellcheck="false" @input="opt(s.wsl, 'startup_commands').set(val($event))"></textarea>
                  <p class="muted">Typed into the shell once it is ready, like keyboard input. Not saved as credentials.</p>
                </template>
              </div>
              <!-- 48 px protocol icon in a 64×64 tile, vertically centered on the right -->
              <div class="big"><img :src="icon(proto.icon)" /></div>
            </div>
          </template>
        </div>

        <div class="msg">{{ message }}</div>
        <div class="actions">
          <button class="dbtn" :disabled="!canSave || saving" @click="save"><img :src="icon('ok')" /> OK</button>
          <button class="dbtn" @click="emit('close')"><img :src="icon('cancel')" /> Cancel</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ed-backdrop { position: absolute; inset: 0; z-index: 60; display: flex; align-items: center; justify-content: center; }
.ed { width: 900px; height: 600px; max-width: calc(100% - 16px); max-height: calc(100% - 16px); display: flex; flex-direction: column; background: var(--panel); border: 1px solid var(--border); color: var(--text); font-size: 12px; }
.ed-title { height: 24px; flex: none; display: flex; align-items: center; padding: 0 8px; border-bottom: 1px solid var(--border-subtle); }
.ed-main { flex: 1; min-height: 0; display: flex; flex-direction: column; padding: 8px 16px; }

/* session-type tiles */
.tiles { display: flex; gap: 2px; flex: none; outline: none; }
.tile { position: relative; width: 56px; height: 56px; padding: 0; background: transparent; border: 1px solid transparent; color: var(--text); font-size: 12px; cursor: pointer; }
.tile img { position: absolute; left: 11px; top: 4px; width: 32px; height: 32px; }
.tile span { position: absolute; left: 0; right: 0; bottom: 3px; text-align: center; }
.tile:hover { background: var(--hover); border-color: var(--accent); }
.tile.on { background: var(--pressed); border-color: var(--accent); }
:root[data-theme="dark"] .tile:hover, :root[data-theme="dark"] .tile.on { border-color: var(--border); }
:root[data-theme="dark"] .tile.on { background: var(--selection); }
@media (prefers-color-scheme: dark) {
  :root[data-theme="system"] .tile:hover, :root[data-theme="system"] .tile.on { border-color: var(--border); }
  :root[data-theme="system"] .tile.on { background: var(--selection); }
}

.ed-body { flex: 1; min-height: 0; display: flex; flex-direction: column; margin-top: 12px; }
.choose { flex: 1; display: flex; align-items: center; justify-content: center; gap: 16px; color: var(--text-muted); font-size: 16px; }
.choose img { width: 48px; height: 48px; }

/* Rust widgets::group_box */
.grp { flex: none; margin-bottom: 0; }
.grp + .grp { margin-top: 12px; }
.grp-h { display: inline-flex; align-items: center; gap: 4px; height: 22px; position: relative; z-index: 1; padding: 0 8px; font-weight: 700; background: var(--panel); border: 1px solid var(--border); border-bottom-color: var(--panel); margin-bottom: -1px; }
.grp-h img { width: 16px; height: 16px; }
.grp-b { display: flex; flex-direction: column; gap: 4px; padding: 8px; border: 1px solid var(--border); }
.basic { display: flex; align-items: center; gap: 8px; padding-left: 16px; }
.basic .gap { margin-left: 16px; }
.req { color: var(--danger); margin-left: 2px; }

/* icon tabs joined to the panel */
.tabs { display: flex; flex: none; margin-top: 16px; position: relative; z-index: 1; }
.tab { display: flex; align-items: center; gap: 4px; height: 24px; padding: 0 8px; font-size: 12px; color: var(--text); background: var(--toolbar); border: 1px solid var(--border); margin-right: -1px; cursor: pointer; }
.tab img { width: 16px; height: 16px; }
.tab:hover { background: var(--hover); }
.tab.on { background: var(--panel); border-bottom-color: var(--panel); }
.panel { flex: 1; min-height: 0; display: flex; gap: 16px; align-items: center; margin-top: -1px; padding: 12px; background: var(--panel); border: 1px solid var(--border); }
.page { flex: 1; align-self: stretch; min-width: 0; overflow-y: auto; display: flex; flex-direction: column; gap: 4px; }
.big { flex: none; width: 64px; height: 64px; display: flex; align-items: center; justify-content: center; background: var(--input); border: 1px solid var(--border); }
.big img { width: 48px; height: 48px; }

.grid { display: grid; grid-template-columns: auto 1fr; align-items: center; gap: 6px 12px; justify-items: start; }
.grid.off { color: var(--text-disabled); }
.checks { display: grid; grid-template-columns: auto 1fr; gap: 6px 24px; margin-top: 6px; justify-items: start; }
.row { display: flex; align-items: center; gap: 8px; }
.mt { margin-top: 8px; }
.chk { display: flex; align-items: center; gap: 6px; min-height: 20px; cursor: pointer; white-space: nowrap; }
.chk.off { color: var(--text-disabled); }
p { margin: 0; }
.small { font-size: 11px; }
.muted { color: var(--text-muted); }
.warn { color: var(--warning); }
.cat { margin-top: 6px; }
.icons { display: flex; flex-wrap: wrap; gap: 2px; }
.icn { width: 40px; height: 40px; padding: 0; display: flex; align-items: center; justify-content: center; background: transparent; border: 1px solid transparent; cursor: pointer; }
.icn img { width: 32px; height: 32px; }
.icn:hover { background: var(--hover); border-color: var(--outline); }
.icn.on { background: var(--selection); border-color: var(--border); }

.in { height: 22px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.in:focus { border-color: var(--focus); }
.in:disabled { color: var(--text-disabled); }
.bad-in, .bad-in:focus { border-color: var(--danger); }
.w320 { width: 320px; }
input[type="number"].in { -moz-appearance: textfield; appearance: textfield; }
input[type="number"].in::-webkit-inner-spin-button, input[type="number"].in::-webkit-outer-spin-button { -webkit-appearance: none; margin: 0; }
.cmds { height: auto; min-height: 76px; padding: 4px; width: 100%; font-family: "DejaVu Sans Mono", monospace; resize: vertical; }
.btn { display: inline-flex; align-items: center; gap: 4px; height: 22px; padding: 0 8px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; white-space: nowrap; }
.btn img { width: 16px; height: 16px; }
.btn:hover { background: var(--hover); border-color: var(--outline); }
.ibtn { width: 22px; height: 22px; padding: 0; display: inline-flex; align-items: center; justify-content: center; background: transparent; border: 1px solid transparent; cursor: pointer; }
.ibtn img { width: 16px; height: 16px; }
.ibtn:hover { background: var(--hover); border-color: var(--outline); }
.spin { display: inline-flex; align-items: stretch; }
.spin-b { display: flex; flex-direction: column; width: 14px; }
.spin-b button { position: relative; flex: 1; padding: 0; background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.spin-b button + button { border-top: none; }
.spin-b button:hover { background: var(--hover); }
.spin-b button::after { content: ""; position: absolute; left: 3px; top: 50%; margin-top: -2px; border: 3px solid transparent; }
.spin-b button:first-child::after { border-bottom: 4px solid var(--text); border-top: 0; }
.spin-b button:last-child::after { border-top: 4px solid var(--text); border-bottom: 0; }
.pop-anchor { position: relative; display: inline-flex; }
.menu { position: absolute; top: 24px; left: 0; z-index: 80; min-width: 160px; padding: 2px; background: var(--panel); border: 1px solid var(--border); box-shadow: 0 4px 12px rgba(0, 0, 0, 0.25); }
.mi { display: block; width: 100%; height: 24px; padding: 0 8px; text-align: left; font-size: 12px; color: var(--text); background: transparent; border: none; white-space: nowrap; cursor: pointer; }
.mi:hover:not(:disabled) { background: var(--hover); }
.mi:disabled { color: var(--text-disabled); cursor: default; }

.msg { flex: none; height: 22px; padding-top: 4px; text-align: center; color: var(--danger); }
.actions { flex: none; display: flex; justify-content: center; gap: 28px; }
.dbtn { display: flex; align-items: center; justify-content: center; gap: 4px; min-width: 80px; height: 30px; padding: 0 12px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.dbtn img { width: 16px; height: 16px; }
.dbtn:hover:not(:disabled) { background: var(--hover); border-color: var(--outline); }
.dbtn:disabled { color: var(--text-disabled); cursor: default; }
.dbtn:disabled img { opacity: 0.5; }
</style>

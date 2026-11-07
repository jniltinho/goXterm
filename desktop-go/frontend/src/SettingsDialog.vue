<script setup lang="ts">
// Settings window, Rust crates/ui/src/settings.rs: fixed 800x560, icon tabs joined to a bordered page,
// titled group boxes, edits applied at once (saved shortly after each change), OK / Esc / X only close.
import { ref, onMounted, onBeforeUnmount, watch, computed } from "vue";
import {
  GetSettings, SaveSettings, Diagnostics, GetStoreState, SetStoreKeyring, ChangeMasterPassword,
  type Settings, type Diag,
} from "./api";
import { SCHEMES } from "./schemes";
import { COMMANDS, DEFAULT_SHORTCUTS, shortcutValid } from "./commands";

const emit = defineEmits<{ saved: []; close: []; import: []; unlock: [] }>();
const icon = (n: string) => `./icons/${n}.png`;

const TABS = [
  ["general", "General", "gear"], ["terminal", "Terminal", "terminal"], ["ssh", "SSH", "ssh"],
  ["display", "Display", "monitor"], ["shortcuts", "Shortcuts", "keyboard"], ["security", "Security", "lock"],
  ["agent", "Agent control", "rocket"], ["diagnostics", "Diagnostics", "terminal-list"],
] as const;
type Tab = (typeof TABS)[number][0];
let lastTab: Tab = "general"; // the Rust window reopens on the last tab (app lifetime)
const tab = ref<Tab>((window as unknown as { __rxtSettingsTab?: Tab }).__rxtSettingsTab ?? lastTab);
watch(tab, (t) => { lastTab = t; (window as unknown as { __rxtSettingsTab?: Tab }).__rxtSettingsTab = t; });

// Rust theme::MONO_FONTS, the first is the default.
const MONO_FONTS = ["Cascadia Mono", "JetBrains Mono", "Hack", "DejaVu Sans Mono", "Inconsolata", "IBM Plex Mono",
  "IBM Plex Mono Text", "Source Code Pro", "Fira Mono", "Ubuntu Mono", "Terminus"];
const GPU = [["auto", "Automatic"], ["gl", "OpenGL"], ["dx12", "DirectX 12"], ["vulkan", "Vulkan"], ["cpu", "Software (CPU)"]];
const LEVELS: [string, string, string][] = [
  ["off", "Off", "No log file is written."], ["error", "Error", "Warnings and errors only."],
  ["info", "Info", "Connections, disconnections and errors."],
  ["debug", "Debug", "Every connection step of every protocol; changes apply at once."],
  ["trace", "Trace", "Full debug plus the protocol details of the chosen protocols."],
];

const s = ref<Settings | null>(null);
const diag = ref<Diag | null>(null);
const store = ref({ unlocked: false, keyring: false });
const error = ref("");

// Font family: "" / "monospace" = the default (Cascadia Mono); a face set elsewhere stays selectable.
const fontNames = computed(() => {
  const cur = s.value?.terminal.fontFamily ?? "";
  return cur && cur !== "monospace" && !MONO_FONTS.includes(cur) ? [...MONO_FONTS, cur] : MONO_FONTS;
});
const fontValue = computed({
  get: () => (!s.value?.terminal.fontFamily || s.value.terminal.fontFamily === "monospace" ? "" : s.value.terminal.fontFamily),
  set: (v: string) => { if (s.value) s.value.terminal.fontFamily = v || "monospace"; },
});

// Colour scheme picker with swatches (Rust scheme_combo): background, foreground + 8 ANSI colours.
const schemeOpen = ref(false);
const schemePos = ref({ left: 0, top: 0 });
function toggleScheme(e: MouseEvent) {
  const r = (e.currentTarget as HTMLElement).getBoundingClientRect();
  schemePos.value = { left: r.left, top: Math.min(r.bottom, window.innerHeight - 434) }; // floats over the page
  schemeOpen.value = !schemeOpen.value;
}
const schemeName = computed(() => s.value?.terminal.scheme || "Follow UI theme");
function swatch(n: string) { const [fg, bg, , , ansi] = SCHEMES[n]; return { bg, cells: [fg, ...ansi.slice(0, 8)] }; }
function pickScheme(n: string) { if (s.value) s.value.terminal.scheme = n; schemeOpen.value = false; }
function closeScheme(e: MouseEvent) { if (!(e.target as HTMLElement).closest(".sch")) schemeOpen.value = false; }

// Shortcuts: the defaults overridden by the saved table.
const shortcuts = computed(() => {
  if (!s.value) return {} as Record<string, string>;
  s.value.shortcuts = { ...DEFAULT_SHORTCUTS, ...s.value.shortcuts };
  return s.value.shortcuts;
});
function resetShortcuts() { if (s.value) s.value.shortcuts = { ...DEFAULT_SHORTCUTS }; }

const releaseOk = computed(() => /^\s*((ctrl|control|alt|shift|super|meta)\s*\+\s*)+((ctrl|control|alt|shift|super|meta)|[a-z0-9]+|f\d{1,2})?\s*$/i.test(s.value?.desktopRelease ?? ""));

// Edits apply at once: save shortly after each change and let the app re-read (theme, zoom, …).
let timer: ReturnType<typeof setTimeout> | undefined;
let ready = false;
watch(s, () => {
  if (!ready || !s.value) return;
  clearTimeout(timer);
  timer = setTimeout(async () => {
    try { await SaveSettings(s.value!); error.value = ""; emit("saved"); } catch (e) { error.value = String(e); }
  }, 300);
}, { deep: true });

// Security page actions.
const pwOpen = ref(false);
const pw1 = ref("");
const pw2 = ref("");
const pwErr = ref("");
const pwProblem = computed(() => ([...pw1.value].length < 8 ? "Use at least 8 characters." : pw1.value !== pw2.value ? "The passwords do not match." : ""));
async function changePassword() {
  if (pwProblem.value) return;
  try { await ChangeMasterPassword(pw1.value); pwOpen.value = false; pw1.value = pw2.value = ""; pwErr.value = ""; }
  catch (e) { pwErr.value = String(e); }
}
async function toggleKeyring(on: boolean) {
  try { await SetStoreKeyring(on); } catch (e) { error.value = String(e); }
  try { store.value = await GetStoreState(); } catch { /* keep */ }
}

function onKey(e: KeyboardEvent) { if (e.key === "Escape" && !schemeOpen.value && !pwOpen.value) emit("close"); }
onMounted(async () => {
  window.addEventListener("keydown", onKey);
  window.addEventListener("mousedown", closeScheme);
  try { s.value = await GetSettings(); } catch (e) { error.value = String(e); }
  try { store.value = await GetStoreState(); } catch { /* older backend */ }
  setTimeout(() => (ready = true)); // the initial load is not an edit
});
onBeforeUnmount(() => {
  window.removeEventListener("keydown", onKey);
  window.removeEventListener("mousedown", closeScheme);
  if (timer && s.value) { clearTimeout(timer); void SaveSettings(s.value).then(() => emit("saved")); }
});
watch(tab, async (t) => {
  if (t === "diagnostics" && !diag.value) {
    try { diag.value = await Diagnostics(); } catch (e) { error.value = String(e); }
  }
});
</script>

<template>
  <div class="st-backdrop">
    <div v-if="s" class="st" role="dialog" aria-label="Settings">
      <div class="st-title"><span>Settings</span><button class="st-x" title="Close" @click="emit('close')"></button></div>
      <div class="st-main">
        <div class="st-tabs">
          <button v-for="[id, label, ic] in TABS" :key="id" class="st-tab" :class="tab === id ? 'on' : ''" @click="tab = id">
            <img :src="icon(ic)" />{{ label }}
          </button>
        </div>
        <div class="st-page">
          <!-- General -->
          <template v-if="tab === 'general'">
            <section class="grp"><div class="grp-h">Tabs</div><div class="grp-b">
              <label class="chk"><input v-model="s.confirmClose" type="checkbox" /> Confirm before closing a connected tab</label>
            </div></section>
            <section class="grp"><div class="grp-h">Notifications</div><div class="grp-b">
              <label class="chk"><input v-model="s.notifications" type="checkbox" /> Show desktop notifications while rustXterm is in the background</label>
            </div></section>
            <section class="grp"><div class="grp-h">Remote desktop</div><div class="grp-b">
              <div class="line"><label>Keyboard release keys</label><input v-model="s.desktopRelease" class="in w160" spellcheck="false" />
                <span v-if="!releaseOk" class="bad">Use modifiers such as Ctrl+Alt, optionally with a key</span></div>
            </div></section>
          </template>

          <!-- Terminal -->
          <template v-else-if="tab === 'terminal'">
            <section class="grp"><div class="grp-h">Font and cursor</div><div class="grp-b">
              <div class="grid">
                <label>Font family</label>
                <select v-model="fontValue" class="in w150"><option value="">Default (Cascadia Mono)</option><option v-for="f in fontNames" :key="f" :value="f">{{ f }}</option></select>
                <label>Font size</label>
                <input v-model.number="s.terminal.fontSize" type="number" min="6" max="48" step="0.5" class="in w60" />
                <label>Cursor</label>
                <select v-model="s.terminal.cursorShape" class="in w110"><option value="block">Block</option><option value="underline">Underline</option><option value="beam">Beam</option></select>
                <span></span>
                <label class="chk"><input v-model="s.terminal.cursorBlink" type="checkbox" /> Blinking cursor</label>
                <label>Color scheme</label>
                <div class="sch">
                  <button class="in w150 sch-btn" @click="toggleScheme">{{ schemeName }}</button>
                  <div v-if="schemeOpen" class="sch-pop" :style="{ left: schemePos.left + 'px', top: schemePos.top + 'px' }">
                    <button class="sch-item" :class="!s.terminal.scheme ? 'on' : ''" @click="pickScheme('')"><span>Follow UI theme</span></button>
                    <button v-for="n in Object.keys(SCHEMES)" :key="n" class="sch-item" :class="s.terminal.scheme === n ? 'on' : ''" @click="pickScheme(n)">
                      <span class="sw" :style="{ background: swatch(n).bg }"><i v-for="(c, k) in swatch(n).cells" :key="k" :style="{ background: c }"></i></span>
                      <span>{{ n }}</span>
                    </button>
                  </div>
                </div>
              </div>
            </div></section>
            <section class="grp"><div class="grp-h">Terminal features</div><div class="grp-b">
              <div class="line"><label>Scrollback lines</label><input v-model.number="s.terminal.scrollbackLines" type="number" min="0" max="1000000" step="100" class="in w70" /></div>
              <label class="chk"><input v-model="s.copyOnSelect" type="checkbox" /> Copy on select</label>
              <label class="chk"><input v-model="s.rightClickPaste" type="checkbox" /> Paste with right click (Ctrl+right click opens the menu)</label>
              <label class="chk"><input v-model="s.pasteGuard" type="checkbox" /> Confirm multi-line paste</label>
            </div></section>
          </template>

          <!-- SSH -->
          <template v-else-if="tab === 'ssh'">
            <section class="grp"><div class="grp-h">SSH sessions</div><div class="grp-b">
              <label class="chk"><input v-model="s.sftpAutoSwitch" type="checkbox" /> Automatically switch to SFTP browser after login</label>
              <label class="chk"><input v-model="s.showSessionBanner" type="checkbox" /> Show session banner</label>
              <p class="muted">Host keys, authentication, jump hosts and port forwarding are set per session in the session editor and in the Tunneling window.</p>
            </div></section>
            <section class="grp"><div class="grp-h">Remote monitoring</div><div class="grp-b">
              <label class="chk"><input v-model="s.remoteMonitoring" type="checkbox" /> Show remote monitoring bar on SSH tabs</label>
              <div class="line"><label>Refresh interval</label><span class="unit"><input v-model.number="s.remoteMonitoringInterval" type="number" min="1" max="30" class="in w50" /> s</span></div>
              <p class="muted">Sessions can override the bar in their advanced SSH settings. A running tab is toggled with the bar's close button or the SFTP panel's Remote monitoring button.</p>
            </div></section>
          </template>

          <!-- Display -->
          <template v-else-if="tab === 'display'">
            <section class="grp"><div class="grp-h">Appearance</div><div class="grp-b">
              <div class="line"><label>Skin</label><select v-model="s.theme" class="in w90"><option value="light">Light</option><option value="dark">Dark</option><option value="system">System</option></select></div>
            </div></section>
            <section class="grp"><div class="grp-h">Toolbar and tabs</div><div class="grp-b">
              <label class="chk"><input v-model="s.toolbarCaptions" type="checkbox" /> Toolbar captions</label>
              <label class="chk"><input v-model="s.showTabNumbers" type="checkbox" /> Show tab numbers</label>
              <label class="chk"><input v-model="s.showCloseButtons" type="checkbox" /> Show close buttons</label>
            </div></section>
            <section class="grp"><div class="grp-h">Graphics</div><div class="grp-b">
              <div class="line"><label>Rendering backend</label><select v-model="s.gpuBackend" class="in w125"><option v-for="[v, l] in GPU" :key="v" :value="v">{{ l }}</option></select></div>
              <p class="small">Applies at the next start. After a driver crash rustXterm picks a safer one.</p>
            </div></section>
            <section class="grp"><div class="grp-h">Interface size</div><div class="grp-b">
              <div class="line"><label>Interface size</label><select v-model="s.uiScale" class="in w90"><option value="auto">Auto</option><option value="100">100%</option><option value="125">125%</option><option value="150">150%</option><option value="200">200%</option></select></div>
            </div></section>
          </template>

          <!-- Shortcuts -->
          <template v-else-if="tab === 'shortcuts'">
            <section class="grp"><div class="grp-h">Keyboard shortcuts</div><div class="grp-b">
              <div class="line"><span class="muted">Use Ctrl+Shift+key, Ctrl+Tab or F11/F12; terminal keys such as Ctrl+C are reserved.</span>
                <button class="btn" @click="resetShortcuts">Reset defaults</button></div>
              <div class="sc">
                <template v-for="[id, label] in COMMANDS" :key="id">
                  <label>{{ label }}</label>
                  <input v-model="shortcuts[id]" class="in w160" spellcheck="false" />
                  <span class="bad">{{ shortcutValid(shortcuts[id] ?? "") ? "" : "Not allowed" }}</span>
                </template>
              </div>
            </div></section>
          </template>

          <!-- Security -->
          <template v-else-if="tab === 'security'">
            <section class="grp"><div class="grp-h">Master password</div><div class="grp-b">
              <div v-if="!store.unlocked" class="line"><span>The session database is locked.</span><button class="btn" @click="emit('unlock')">Unlock…</button></div>
              <label class="chk" :class="store.unlocked ? '' : 'off'"><input type="checkbox" :checked="store.keyring" :disabled="!store.unlocked" @change="toggleKeyring(($event.target as HTMLInputElement).checked)" /> Unlock with system keyring</label>
              <p class="muted">The master password always unlocks the database, also without the keyring.</p>
              <div><button class="btn" :disabled="!store.unlocked" @click="pwOpen = true">Change master password…</button></div>
            </div></section>
            <section class="grp"><div class="grp-h">Import and export</div><div class="grp-b">
              <div class="line">
                <button class="btn" :disabled="!store.unlocked" @click="emit('import')">Import MobaXterm.ini…</button>
                <button class="btn" disabled title="Not available yet">Import rustXterm TOML…</button>
                <button class="btn" disabled title="Not available yet">Export rustXterm TOML…</button>
              </div>
            </div></section>
          </template>

          <!-- Agent control -->
          <template v-else-if="tab === 'agent'">
            <section class="plain">
              <div class="heading">Agent control</div>
              <label class="chk"><input v-model="s.agentControl" type="checkbox" /> Enable agent control (CLI live commands and MCP)</label>
              <p class="small">Uses a local socket reachable only by processes of your own user; nothing listens on the network. Each session's agent policy still applies.</p>
            </section>
          </template>

          <!-- Diagnostics -->
          <template v-else>
            <section class="grp"><div class="grp-h">Diagnostic log</div><div class="grp-b">
              <div class="line"><label>Level</label><select v-model="s.logLevel" class="in w90"><option v-for="[v, l] in LEVELS" :key="v" :value="v">{{ l }}</option></select>
                <span class="muted">{{ LEVELS.find((x) => x[0] === s!.logLevel)?.[2] }}</span></div>
            </div></section>
            <section class="grp"><div class="grp-h">Store</div><div class="grp-b">
              <div v-if="diag" class="kv">
                <span>Version</span><b>{{ diag.version || "dev" }}</b>
                <span>Data folder</span><b class="mono">{{ diag.dataDir }}</b>
                <span>Unlocked</span><b>{{ diag.locked ? "no (locked)" : "yes" }}</b>
                <span>Sessions</span><b>{{ diag.sessions }}</b>
                <span>Folders</span><b>{{ diag.folders }}</b>
                <span>SSH trusted keys</span><b>{{ diag.sshTrust }}</b>
                <span>Pinned TLS certificates</span><b>{{ diag.certTrust }}</b>
              </div>
              <span v-else class="muted">Loading…</span>
            </div></section>
          </template>
          <div v-if="error" class="bad">{{ error }}</div>
        </div>
        <div class="st-foot"><button class="ok" @click="emit('close')"><img :src="icon('ok')" /> OK</button></div>
      </div>
    </div>

    <!-- Change master password (Rust StoreDialog::ChangePassword) -->
    <div v-if="pwOpen" class="pw-backdrop" @keydown.esc.stop="pwOpen = false">
      <div class="pw">
        <div class="st-title"><span>Change master password</span></div>
        <div class="pw-body">
          <p class="warn">If you forget the new master password, the saved data cannot be recovered.</p>
          <label>Master password</label><input v-model="pw1" type="password" class="in wfull" @keydown.enter="changePassword" />
          <label>Confirm master password</label><input v-model="pw2" type="password" class="in wfull" @keydown.enter="changePassword" />
          <p v-if="pw1 && pw2 && pwProblem" class="bad">{{ pwProblem }}</p>
          <p v-if="pwErr" class="bad">{{ pwErr }}</p>
          <div class="pw-actions">
            <button class="ok" :disabled="!!pwProblem" @click="changePassword"><img :src="icon('ok')" /> Change</button>
            <button class="ok" @click="pwOpen = false"><img :src="icon('cancel')" /> Cancel</button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.st-backdrop { position: absolute; inset: 0; z-index: 60; display: flex; align-items: center; justify-content: center; }
.st { width: 800px; height: 560px; max-width: calc(100% - 16px); max-height: calc(100% - 16px); display: flex; flex-direction: column; background: var(--panel); border: 1px solid var(--border); color: var(--text); font-size: 12px; }
.st-title { height: 24px; flex: none; display: flex; align-items: center; justify-content: space-between; padding: 0 4px 0 8px; border-bottom: 1px solid var(--border-subtle); }
.st-x { width: 20px; height: 20px; background: transparent; border: none; cursor: pointer; position: relative; }
.st-x:hover { background: var(--hover); }
.st-x::before, .st-x::after { content: ""; position: absolute; left: 4px; right: 4px; top: 9px; height: 1.2px; background: var(--text); transform: rotate(45deg); }
.st-x::after { transform: rotate(-45deg); }
.st-main { flex: 1; min-height: 0; display: flex; flex-direction: column; padding: 8px 8px 0; }
.st-tabs { display: flex; flex-wrap: wrap; position: relative; z-index: 1; }
.st-tab { display: flex; align-items: center; gap: 4px; height: 36px; padding: 0 6px; font-size: 12px; color: var(--text); background: var(--toolbar); border: 1px solid var(--border); margin-right: -1px; cursor: pointer; }
.st-tab img { width: 24px; height: 24px; }
.st-tab:hover { background: var(--hover); }
.st-tab.on { background: var(--panel); border-bottom-color: var(--panel); }
.st-page { flex: 1; min-height: 0; overflow-y: auto; margin-top: -1px; padding: 8px; background: var(--panel); border: 1px solid var(--border); }
.st-foot { flex: none; display: flex; justify-content: center; padding: 8px; }
.ok { display: flex; align-items: center; justify-content: center; gap: 4px; min-width: 80px; height: 30px; padding: 0 12px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.ok:hover:not(:disabled) { background: var(--hover); border-color: var(--outline); }
.ok:disabled { color: var(--text-disabled); }
.ok img { width: 16px; height: 16px; }

/* Rust widgets::group_box: a bordered title tab joined to the bordered body */
.grp { margin-bottom: 8px; }
.grp-h { display: inline-block; position: relative; z-index: 1; padding: 2px 8px; font-weight: 700; background: var(--panel); border: 1px solid var(--border); border-bottom-color: var(--panel); margin-bottom: -1px; }
.grp-b { display: flex; flex-direction: column; gap: 4px; padding: 8px; border: 1px solid var(--border); }
.plain { padding: 8px; border: 1px solid var(--border); display: flex; flex-direction: column; gap: 6px; }
.heading { font-size: 15px; }
.chk { display: flex; align-items: center; gap: 6px; min-height: 22px; cursor: pointer; }
.chk.off { color: var(--text-disabled); }
.line { display: flex; align-items: center; gap: 12px; min-height: 22px; }
.grid { display: grid; grid-template-columns: auto 1fr; align-items: center; gap: 6px 12px; justify-items: start; }
.sc { display: grid; grid-template-columns: minmax(150px, auto) 160px 1fr; align-items: center; gap: 4px 12px; margin-top: 4px; }
.kv { display: grid; grid-template-columns: auto 1fr; gap: 4px 16px; }
.kv b { font-weight: 400; }
.mono { font-family: "DejaVu Sans Mono", monospace; word-break: break-all; }
.muted { color: var(--text-muted); font-size: 11px; margin: 0; }
.small { font-size: 11px; margin: 0; }
.bad { color: var(--danger); }
.warn { color: var(--warning); }
.unit { display: flex; align-items: center; gap: 4px; }
.in { height: 22px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.in:focus { border-color: var(--focus); }
/* egui DragValue look: no spinner arrows */
input[type="number"].in { -moz-appearance: textfield; appearance: textfield; }
input[type="number"].in::-webkit-inner-spin-button, input[type="number"].in::-webkit-outer-spin-button { -webkit-appearance: none; margin: 0; }
.w50 { width: 50px; } .w60 { width: 60px; } .w70 { width: 76px; } .w90 { width: 90px; } .w110 { width: 116px; }
.w125 { width: 130px; } .w150 { width: 170px; } .w160 { width: 160px; } .wfull { width: 100%; }
.btn { height: 22px; padding: 0 8px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.btn:hover:not(:disabled) { background: var(--hover); }
.btn:disabled { color: var(--text-disabled); cursor: default; }

/* Colour scheme combo with swatches */
.sch { position: relative; }
.sch-btn { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; text-align: left; padding-right: 24px; cursor: pointer; background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='10' height='6'%3E%3Cpath d='M0 0h10L5 6z' fill='%237a8794'/%3E%3C/svg%3E"); background-repeat: no-repeat; background-position: right 8px center; }
.sch-pop { position: fixed; z-index: 75; min-width: 206px; padding: 2px; background: var(--panel); border: 1px solid var(--border); }
.sch-item { display: flex; align-items: center; gap: 8px; width: 100%; height: 30px; padding: 0 4px; font-size: 12px; color: var(--text); background: transparent; border: none; text-align: left; cursor: pointer; }
.sch-item:hover { background: var(--hover); }
.sch-item.on span:last-child { padding: 2px 6px; background: var(--selection); color: var(--selection-text); }
.sw { display: inline-flex; gap: 1px; padding: 2px; border: 1px solid var(--border-subtle); flex: none; }
.sw i { width: 6px; height: 7px; }

/* Change master password */
.pw-backdrop { position: fixed; inset: 0; z-index: 70; display: flex; align-items: center; justify-content: center; }
.pw { width: 460px; background: var(--panel); border: 1px solid var(--border); }
.pw-body { padding: 8px 12px 12px; display: flex; flex-direction: column; gap: 4px; }
.pw-body p { margin: 0 0 4px; }
.pw-actions { display: flex; justify-content: center; gap: 20px; margin-top: 10px; }
</style>

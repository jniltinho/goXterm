<script setup lang="ts">
import { ref, onMounted, watch } from "vue";
import { GetSettings, SaveSettings, Diagnostics, type Settings, type Diag } from "./api";

const emit = defineEmits<{ saved: []; close: [] }>();

const tab = ref<"general" | "terminal" | "ssh" | "display" | "shortcuts" | "security" | "agent" | "diagnostics">("general");

// Keyboard shortcuts / gestures reference (read-only for now).
const shortcuts = [
  ["Ctrl+Shift+N", "New session"],
  ["Ctrl+Tab / Ctrl+Shift+Tab", "Next / previous tab"],
  ["Ctrl+Shift+W", "Close the active tab"],
  ["Ctrl+,", "Open Settings"],
  ["Enter (session selected)", "Open the selected session"],
  ["F2 (session selected)", "Rename the selected session"],
  ["Delete (session selected)", "Delete the selected session"],
  ["Double-click a session", "Open in its protocol"],
  ["Right-click a session/folder", "Context menu (connect, edit, rename, delete)"],
  ["Ctrl+Shift+C / Ctrl+Shift+V", "Copy / paste in the terminal"],
  ["In a stopped tab: Return / R", "Close the tab / restart the session"],
];
const s = ref<Settings | null>(null);
const diag = ref<Diag | null>(null);
const error = ref("");
const saving = ref(false);

onMounted(async () => {
  try { s.value = await GetSettings(); } catch (e) { error.value = String(e); }
});

watch(tab, async (t) => {
  if (t === "diagnostics" && !diag.value) {
    try { diag.value = await Diagnostics(); } catch (e) { error.value = String(e); }
  }
});

async function save() {
  if (!s.value || saving.value) return;
  saving.value = true;
  error.value = "";
  try {
    await SaveSettings(s.value);
    emit("saved");
  } catch (e) {
    error.value = String(e);
  } finally {
    saving.value = false;
  }
}
</script>

<template>
  <div class="st-backdrop">
    <div class="st" v-if="s">
      <div class="st-title">Settings</div>
      <div class="st-tabs">
        <button class="st-tab" :class="tab === 'general' ? 'on' : ''" @click="tab = 'general'">General</button>
        <button class="st-tab" :class="tab === 'terminal' ? 'on' : ''" @click="tab = 'terminal'">Terminal</button>
        <button class="st-tab" :class="tab === 'ssh' ? 'on' : ''" @click="tab = 'ssh'">SSH</button>
        <button class="st-tab" :class="tab === 'display' ? 'on' : ''" @click="tab = 'display'">Display</button>
        <button class="st-tab" :class="tab === 'shortcuts' ? 'on' : ''" @click="tab = 'shortcuts'">Shortcuts</button>
        <button class="st-tab" :class="tab === 'security' ? 'on' : ''" @click="tab = 'security'">Security</button>
        <button class="st-tab" :class="tab === 'agent' ? 'on' : ''" @click="tab = 'agent'">Agent</button>
        <button class="st-tab" :class="tab === 'diagnostics' ? 'on' : ''" @click="tab = 'diagnostics'">Diagnostics</button>
      </div>
      <div class="st-body">
        <template v-if="tab === 'general'">
          <label class="row"><input type="checkbox" v-model="s.copyOnSelect" /> Copy on select</label>
          <label class="row"><input type="checkbox" v-model="s.rightClickPaste" /> Paste with right click</label>
          <label class="row"><input type="checkbox" v-model="s.pasteGuard" /> Confirm multi-line paste</label>
          <label class="row"><input type="checkbox" v-model="s.confirmClose" /> Confirm before closing a running tab</label>
          <label class="row"><input type="checkbox" v-model="s.notifications" /> Desktop notifications</label>
        </template>

        <template v-else-if="tab === 'terminal'">
          <div class="field"><label>Font family</label>
            <select v-model="s.terminal.fontFamily" class="in">
              <option>DejaVu Sans Mono</option><option>Cascadia Mono</option><option>JetBrains Mono</option>
              <option>Hack</option><option>Consolas</option><option>monospace</option>
            </select>
          </div>
          <div class="field"><label>Font size</label><input type="number" min="6" max="40" v-model.number="s.terminal.fontSize" class="in in-sm" /></div>
          <div class="field"><label>Cursor</label>
            <select v-model="s.terminal.cursorShape" class="in in-sm"><option value="block">Block</option><option value="underline">Underline</option><option value="beam">Beam</option></select>
          </div>
          <label class="row"><input type="checkbox" v-model="s.terminal.cursorBlink" /> Blinking cursor</label>
          <div class="field"><label>Color scheme</label>
            <select v-model="s.terminal.scheme" class="in"><option>xterm</option><option>dark</option><option>light</option><option>solarized-dark</option><option>solarized-light</option></select>
          </div>
          <div class="field"><label>Scrollback lines</label><input type="number" min="0" max="1000000" v-model.number="s.terminal.scrollbackLines" class="in in-sm" /></div>
        </template>

        <template v-else-if="tab === 'ssh'">
          <label class="row"><input type="checkbox" v-model="s.remoteMonitoring" /> Show the remote monitoring bar on SSH tabs</label>
          <div class="field"><label>Refresh interval (s)</label><input type="number" min="1" max="60" v-model.number="s.remoteMonitoringInterval" class="in in-sm" /></div>
        </template>

        <template v-else-if="tab === 'display'">
          <fieldset class="grp"><legend>Appearance</legend>
            <div class="field"><label>Skin</label>
              <select v-model="s.theme" class="in in-sm"><option value="system">System</option><option value="light">Light</option><option value="dark">Dark</option></select>
            </div>
          </fieldset>
          <fieldset class="grp"><legend>Toolbar and tabs</legend>
            <label class="row"><input type="checkbox" v-model="s.toolbarCaptions" /> Toolbar captions</label>
            <label class="row"><input type="checkbox" v-model="s.showTabNumbers" /> Show tab numbers</label>
            <label class="row"><input type="checkbox" v-model="s.showCloseButtons" /> Show close buttons</label>
          </fieldset>
          <fieldset class="grp"><legend>Graphics</legend>
            <div class="field"><label>Rendering backend</label>
              <select v-model="s.gpuBackend" class="in"><option value="auto">Automatic</option><option value="hardware">Hardware (GPU)</option><option value="software">Software (CPU)</option></select>
            </div>
            <p class="st-note">Applies at the next start. After a driver crash rustXterm picks a safer one.</p>
          </fieldset>
          <fieldset class="grp"><legend>Interface size</legend>
            <div class="field"><label>Interface size</label>
              <select v-model="s.uiScale" class="in in-sm"><option value="auto">Auto</option><option value="100">100%</option><option value="125">125%</option><option value="150">150%</option><option value="200">200%</option></select>
            </div>
          </fieldset>
        </template>

        <template v-else-if="tab === 'shortcuts'">
          <div class="sc">
            <div class="sc-row" v-for="[k, v] in shortcuts" :key="k"><span class="sc-key">{{ k }}</span><span class="sc-act">{{ v }}</span></div>
          </div>
          <p class="st-note">Customizable key bindings are a follow-up; these are the current gestures.</p>
        </template>

        <template v-else-if="tab === 'security'">
          <label class="row"><input type="checkbox" v-model="s.pasteGuard" /> Confirm multi-line paste (paste guard)</label>
          <p class="st-note">SSH host keys and TLS certificates are trusted on first use, per connection (prompted when unknown); the accepted counts are on the Diagnostics tab. Credentials are kept in the encrypted store and never written to config.</p>
        </template>

        <template v-else-if="tab === 'agent'">
          <label class="row"><input type="checkbox" v-model="s.agentControl" /> Allow AI agents / the CLI to drive sessions over the local control channel</label>
          <p class="st-note">Per-session agent policy (Deny / Ask / Allow, and whether the agent may read content) is set in each session's editor under “Agent settings”.</p>
        </template>

        <template v-else-if="tab === 'diagnostics'">
          <div v-if="diag" class="diag">
            <div class="diag-row"><span>Version</span><b>{{ diag.version || "dev" }}</b></div>
            <div class="diag-row"><span>Store</span><b class="mono">{{ diag.dataDir }}</b></div>
            <div class="diag-row"><span>Unlocked</span><b>{{ diag.locked ? "no (locked)" : "yes" }}</b></div>
            <div class="diag-row"><span>Sessions</span><b>{{ diag.sessions }}</b></div>
            <div class="diag-row"><span>Folders</span><b>{{ diag.folders }}</b></div>
            <div class="diag-row"><span>SSH trusted keys</span><b>{{ diag.sshTrust }}</b></div>
            <div class="diag-row"><span>Pinned TLS certs</span><b>{{ diag.certTrust }}</b></div>
          </div>
          <p v-else class="st-note">Loading…</p>
        </template>
        <div v-if="error" class="st-err">{{ error }}</div>
      </div>
      <div class="st-actions">
        <button class="dbtn primary" :disabled="saving" @click="save">{{ saving ? "Saving…" : "OK" }}</button>
        <button class="dbtn" @click="emit('close')">Cancel</button>
      </div>
    </div>
  </div>
</template>

<style scoped>
.st-backdrop { position: absolute; inset: 0; z-index: 60; display: flex; align-items: center; justify-content: center; background: rgba(20,28,38,.3); }
.st { width: 540px; max-width: 92%; background: #f2f4f7; border: 1px solid #9aa7b4; box-shadow: 0 16px 44px rgba(0,0,0,.4), 0 2px 6px rgba(0,0,0,.25); color: #1e1e1e; }
.st-title { font-size: 12px; font-weight: 700; color: #33465a; background: linear-gradient(#e9eef3, #dbe3ea); border-bottom: 1px solid #c2ccd6; padding: 7px 12px; }
.st-tabs { display: flex; gap: 2px; padding: 8px 10px 0; border-bottom: 1px solid #c2ccd6; background: #eef1f4; }
.st-tab { padding: 6px 14px; font-size: 12px; background: #e7ecf1; border: 1px solid #c2ccd6; border-bottom: none; color: #4a5a68; cursor: pointer; }
.st-tab.on { background: #f7f9fb; color: #1e3a5a; font-weight: 600; margin-bottom: -1px; }
.st-body { padding: 16px; min-height: 180px; display: flex; flex-direction: column; gap: 10px; background: #f7f9fb; }
.row { display: flex; align-items: center; gap: 8px; font-size: 13px; cursor: pointer; }
.field { display: flex; align-items: center; gap: 12px; }
.field label { font-size: 13px; min-width: 130px; }
.in { height: 26px; padding: 0 8px; font-size: 13px; background: #fff; border: 1px solid #a9b4c0; }
.in-sm { width: 90px; }
.st-err { color: #c0392b; font-size: 12px; }
.st-note { font-size: 12px; color: #7a8794; margin-top: 4px; }
.grp { border: 1px solid #c2ccd6; padding: 10px 12px; margin: 0; display: flex; flex-direction: column; gap: 8px; background: #fbfcfd; }
.grp legend { font-size: 12px; font-weight: 600; color: #33465a; padding: 0 6px; }
.sc { display: flex; flex-direction: column; gap: 1px; }
.sc-row { display: flex; justify-content: space-between; gap: 16px; font-size: 12.5px; padding: 4px 0; border-bottom: 1px solid #e3e9ee; }
.sc-key { color: #1e3a5a; font-weight: 600; }
.sc-act { color: #4a5a68; text-align: right; }
.diag { display: flex; flex-direction: column; gap: 2px; }
.diag-row { display: flex; justify-content: space-between; gap: 16px; font-size: 13px; padding: 4px 0; border-bottom: 1px solid #e3e9ee; }
.diag-row span { color: #4a5a68; }
.diag-row b { color: #1e3a5a; }
.mono { font-family: "DejaVu Sans Mono", monospace; font-size: 11.5px; word-break: break-all; }
.st-actions { display: flex; justify-content: center; gap: 10px; padding: 12px; border-top: 1px solid #d6dde4; background: #eef1f4; }
.dbtn { height: 30px; padding: 0 22px; font-size: 13px; background: linear-gradient(#ffffff, #e9eef3); border: 1px solid #a9b4c0; box-shadow: 0 1px 2px rgba(0,0,0,.12); cursor: pointer; }
.dbtn:hover { border-color: #8fb4dd; }
.dbtn.primary { background: linear-gradient(#5aa469, #3f8b50); border-color: #367a46; color: #fff; }
.dbtn.primary:hover { background: linear-gradient(#64b074, #459657); }
.dbtn.primary:disabled { opacity: .55; }
:deep(*) { border-radius: 0 !important; }
</style>

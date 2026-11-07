<script setup lang="ts">
import { ref, onMounted } from "vue";
import { GetSettings, SaveSettings, type Settings } from "./api";

const emit = defineEmits<{ saved: []; close: [] }>();

const tab = ref<"general" | "terminal" | "ssh">("general");
const s = ref<Settings | null>(null);
const error = ref("");
const saving = ref(false);

onMounted(async () => {
  try { s.value = await GetSettings(); } catch (e) { error.value = String(e); }
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

        <template v-else>
          <label class="row"><input type="checkbox" v-model="s.remoteMonitoring" /> Show the remote monitoring bar on SSH tabs</label>
          <div class="field"><label>Refresh interval (s)</label><input type="number" min="1" max="60" v-model.number="s.remoteMonitoringInterval" class="in in-sm" /></div>
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
.st-actions { display: flex; justify-content: center; gap: 10px; padding: 12px; border-top: 1px solid #d6dde4; background: #eef1f4; }
.dbtn { height: 30px; padding: 0 22px; font-size: 13px; background: linear-gradient(#ffffff, #e9eef3); border: 1px solid #a9b4c0; box-shadow: 0 1px 2px rgba(0,0,0,.12); cursor: pointer; }
.dbtn:hover { border-color: #8fb4dd; }
.dbtn.primary { background: linear-gradient(#5aa469, #3f8b50); border-color: #367a46; color: #fff; }
.dbtn.primary:hover { background: linear-gradient(#64b074, #459657); }
.dbtn.primary:disabled { opacity: .55; }
:deep(*) { border-radius: 0 !important; }
</style>

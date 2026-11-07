<script setup lang="ts">
import { computed } from "vue";
import type { ImportPreview } from "./api";

// Import MobaXterm.ini preview (Rust StoreDialog::Import): the file, a summary, the sessions grouped
// by folder, then skipped entries and warnings with their line numbers.
const props = defineProps<{ preview: ImportPreview; busy: boolean }>();
const emit = defineEmits<{ ok: []; cancel: [] }>();
const icon = (n: string) => `./icons/${n}.png`;
const r = computed(() => props.preview.report);
const sessions = computed(() => r.value.sessions ?? []);
const skipped = computed(() => r.value.skipped ?? []);
const warnings = computed(() => r.value.warnings ?? []);
const groups = computed(() => {
  const order = ["", ...(r.value.folders ?? [])];
  return order
    .map((f) => ({ folder: f, items: sessions.value.filter((s) => s.folder === f).map((s) => s.session) }))
    .filter((g) => g.items.length);
});
const target = (s: Record<string, unknown>) =>
  s.protocol === "wsl" ? `wsl://${(s.wsl as Record<string, string>)?.distribution ?? "Default"}`
    : `${s.protocol}://${s.user ? s.user + "@" : ""}${s.host}${s.port ? ":" + s.port : ""}`;
</script>

<template>
  <div class="im-backdrop" @keydown.esc="emit('cancel')">
    <div class="im" role="dialog" aria-label="Import MobaXterm.ini">
      <div class="im-title">Import MobaXterm.ini</div>
      <div class="im-body">
        <div class="im-path">{{ preview.path }}</div>
        <p>{{ sessions.length }} session(s) in {{ (r.folders ?? []).length }} folder(s) can be imported; {{ skipped.length }}
          {{ skipped.length === 1 ? "entry is" : "entries are" }} skipped. Passwords are never read.</p>
        <div class="im-list">
          <template v-for="g in groups" :key="g.folder">
            <div class="im-folder"><img :src="icon('folder')" /> {{ g.folder || "User sessions" }}</div>
            <div v-for="(s, i) in g.items" :key="g.folder + i" class="im-row"><span>{{ s.name }}</span><span class="im-muted">{{ target(s) }}</span></div>
          </template>
          <template v-if="warnings.length">
            <div class="im-folder warn">Warnings</div>
            <div v-for="w in warnings" :key="'w' + w.line + w.name" class="im-row"><span>{{ w.line }}: {{ w.name }}</span><span class="im-muted">{{ w.reason }}</span></div>
          </template>
          <template v-if="skipped.length">
            <div class="im-folder skip">Skipped</div>
            <div v-for="s in skipped" :key="'s' + s.line" class="im-row"><span>{{ s.line }}: {{ s.name || "—" }}</span><span class="im-muted">{{ s.reason }}</span></div>
          </template>
        </div>
        <div class="im-actions">
          <button class="im-btn" :disabled="!sessions.length || busy" @click="emit('ok')"><img :src="icon('ok')" /> {{ busy ? "Working..." : "Import" }}</button>
          <button class="im-btn" @click="emit('cancel')"><img :src="icon('cancel')" /> Cancel</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.im-backdrop { position: fixed; inset: 0; z-index: 85; display: flex; align-items: center; justify-content: center; }
.im { width: 560px; max-width: calc(100% - 32px); background: var(--panel); border: 1px solid var(--border); color: var(--text); font-size: 12px; }
.im-title { padding: 3px 8px; border-bottom: 1px solid var(--border-subtle); }
.im-body { padding: 8px 12px 12px; }
.im-path { font-size: 11px; color: var(--text-muted); word-break: break-all; }
.im-body p { margin: 6px 0; }
.im-list { max-height: 320px; overflow-y: auto; border: 1px solid var(--border-subtle); background: var(--surface); }
.im-folder { display: flex; align-items: center; gap: 4px; padding: 3px 6px; font-weight: 700; background: var(--panel); }
.im-folder img { width: 16px; height: 16px; }
.im-folder.warn { color: var(--warning); }
.im-folder.skip { color: var(--danger); }
.im-row { display: grid; grid-template-columns: minmax(0, 1fr) minmax(0, 1.4fr); gap: 12px; padding: 2px 6px 2px 26px; }
.im-row:nth-child(odd) { background: var(--hover); }
.im-row span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.im-muted { color: var(--text-muted); }
.im-actions { display: flex; justify-content: center; gap: 24px; margin-top: 12px; }
.im-btn { display: flex; align-items: center; justify-content: center; gap: 4px; min-width: 92px; height: 30px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.im-btn:hover:not(:disabled) { background: var(--hover); border-color: var(--outline); }
.im-btn:disabled { color: var(--text-disabled); }
.im-btn img { width: 16px; height: 16px; }
</style>

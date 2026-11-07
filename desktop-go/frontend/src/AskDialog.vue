<script setup lang="ts">
import { ref, watch, nextTick } from "vue";
import { askState } from "./ask";

// Renders the pending ask.ts request as a flat Rust-style modal (panel fill, 1 px border).
const value = ref("");
const field = ref<HTMLInputElement | null>(null);
const okBtn = ref<HTMLButtonElement | null>(null);
const icon = (name: string) => `./icons/${name}.png`;

watch(askState, async (r) => {
  if (!r) return;
  value.value = r.input ?? "";
  await nextTick();
  if (r.input !== undefined) { field.value?.focus(); field.value?.select(); } else okBtn.value?.focus();
});

function answer(ok: boolean) {
  const r = askState.value;
  if (!r) return;
  askState.value = null;
  if (r.input !== undefined) r.resolve(ok && value.value.trim() ? value.value.trim() : null);
  else r.resolve(ok);
}
</script>

<template>
  <div v-if="askState" class="ask-backdrop" @keydown.esc="answer(false)">
    <div class="ask" role="dialog" :aria-label="askState.title">
      <div class="ask-title">{{ askState.title }}</div>
      <div class="ask-body">
        <p class="ask-msg">{{ askState.message }}</p>
        <input v-if="askState.input !== undefined" ref="field" v-model="value" class="ask-in" spellcheck="false" @keydown.enter="answer(true)" />
        <div class="ask-actions">
          <button ref="okBtn" class="ask-btn" @click="answer(true)"><img :src="icon('ok')" /> {{ askState.ok }}</button>
          <button v-if="askState.cancel" class="ask-btn" @click="answer(false)"><img :src="icon('cancel')" /> {{ askState.cancel }}</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.ask-backdrop { position: fixed; inset: 0; z-index: 90; display: flex; align-items: center; justify-content: center; }
.ask { width: 400px; max-width: calc(100% - 32px); background: var(--panel); border: 1px solid var(--border); color: var(--text); font-size: 12px; }
.ask-title { padding: 3px 8px; border-bottom: 1px solid var(--border-subtle); background: var(--title-bg); color: var(--title-fg); font-weight: var(--title-weight); }
.ask-body { padding: 8px 10px 10px; }
.ask-msg { margin: 0 0 6px; white-space: pre-wrap; }
.ask-in { width: 100%; height: 22px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.ask-in:focus { border-color: var(--focus); }
.ask-actions { display: flex; justify-content: center; gap: 20px; margin-top: 12px; }
.ask-btn { display: flex; align-items: center; justify-content: center; gap: 4px; min-width: 74px; height: 26px; padding: 0 8px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.ask-btn:hover, .ask-btn:focus { background: var(--hover); border-color: var(--outline); outline: none; }
.ask-btn img { width: 16px; height: 16px; }
</style>

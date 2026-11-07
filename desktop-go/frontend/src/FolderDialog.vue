<script setup lang="ts">
import { ref, onMounted } from "vue";

// New / Edit folder (Rust Modal::FolderName): the name and the catalog's folder icons in a grid.
const props = defineProps<{ title: string; name: string; icon: string }>();
const emit = defineEmits<{ ok: [name: string, icon: string]; cancel: [] }>();

// Rust icons::folder_icons(): the catalog "folders" category, without the folder-up/move actions.
const FOLDER_ICONS = (
  "folder folder-blue folder-green folder-gray folder-red folder-purple folder-silver folder-steel folder-orange " +
  "folder-pink folder-teal folder-cyan folder-brown folder-lime folder-navy folder-black folder-video folder-images " +
  "folder-documents folder-user folder-web folder-camera folder-l folder-lock folder-music folder-printer folder-run " +
  "folder-zip folder-edit folder-warning folder-tools folder-server folder-key folder-apple folder-linux folder-windows " +
  "folder-android folder-debian folder-redhat folder-u folder-cubes folder-windows-tiles folder-terminal folder-wifi " +
  "folder-raspberry folder-factory folder-users folder-fedora folder-centos folder-virtualbox folder-lab folder-home " +
  "folder-docker folder-settings folder-search"
).split(" ");

const name = ref(props.name);
const pick = ref(props.icon || "folder");
const field = ref<HTMLInputElement | null>(null);
const icon = (n: string) => `./icons/${n}.png`;
onMounted(() => setTimeout(() => { field.value?.focus(); field.value?.select(); }));
const ok = () => { if (name.value.trim()) emit("ok", name.value.trim(), pick.value); };
</script>

<template>
  <div class="fd-backdrop" @keydown.esc="emit('cancel')">
    <div class="fd" role="dialog" :aria-label="title">
      <div class="fd-title">{{ title }}</div>
      <div class="fd-body">
        <label class="fd-label">Folder name</label>
        <input ref="field" v-model="name" class="fd-in" spellcheck="false" @keydown.enter="ok" />
        <label class="fd-label">Folder icon</label>
        <div class="fd-grid">
          <button v-for="n in FOLDER_ICONS" :key="n" class="fd-ico" :class="pick === n ? 'on' : ''" :title="n" @click="pick = n"><img :src="icon(n)" /></button>
        </div>
        <div class="fd-actions">
          <button class="fd-btn" :disabled="!name.trim()" @click="ok"><img :src="icon('ok')" /> OK</button>
          <button class="fd-btn" @click="emit('cancel')"><img :src="icon('cancel')" /> Cancel</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.fd-backdrop { position: fixed; inset: 0; z-index: 85; display: flex; align-items: center; justify-content: center; }
.fd { width: 492px; background: var(--panel); border: 1px solid var(--border); color: var(--text); font-size: 12px; }
.fd-title { padding: 3px 8px; border-bottom: 1px solid var(--border-subtle); background: var(--title-bg); color: var(--title-fg); font-weight: var(--title-weight); }
.fd-body { padding: 8px 14px 12px; }
.fd-label { display: block; margin: 6px 0 3px; }
.fd-in { width: 100%; height: 22px; padding: 0 4px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.fd-in:focus { border-color: var(--focus); }
.fd-grid { display: flex; flex-wrap: wrap; gap: 2px; }
.fd-ico { width: 42px; height: 33px; display: flex; align-items: center; justify-content: center; background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.fd-ico:hover { background: var(--hover); }
.fd-ico.on { background: var(--selection); border-color: var(--selection); }
.fd-ico img { width: 24px; height: 24px; }
.fd-actions { display: flex; justify-content: center; gap: 28px; margin-top: 14px; }
.fd-btn { display: flex; align-items: center; justify-content: center; gap: 4px; min-width: 92px; height: 32px; font-size: 12px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.fd-btn:hover:not(:disabled) { background: var(--hover); border-color: var(--outline); }
.fd-btn:disabled { color: var(--text-disabled); }
.fd-btn img { width: 16px; height: 16px; }
</style>

<script setup lang="ts">
import { ordered } from "./ordered";
import { releaseCombo, isRelease } from "./release";
import { ref, watch, onMounted, onBeforeUnmount } from "vue";
import { Events } from "@wailsio/runtime";
import { VncStart, VncPointer, VncKey, VncDisconnect, VncSendClipboard, VncSetPaused, type VncView } from "./api";
const input = ordered(); // pointer and key events reach the session in order

const props = defineProps<{ connect: () => Promise<VncView>; active?: boolean }>();
const emit = defineEmits<{ state: ["connecting" | "connected" | "stopped"] }>();

const canvas = ref<HTMLCanvasElement | null>(null);
const status = ref("Connecting…");
let tabId = "";
let ctx: CanvasRenderingContext2D | null = null;
let offFrame: (() => void) | undefined;
let offClose: (() => void) | undefined;
let offClip: (() => void) | undefined;
let buttons = 0;

// server clipboard → local; local clipboard → server (on focus), so copy/paste crosses the VNC.
async function pushLocalClipboard() {
  try { const t = await navigator.clipboard.readText(); if (t) void VncSendClipboard(tabId, t); } catch { /* no permission */ }
}

// X keysyms for the common non-printable keys; printable chars map to their Unicode code point.
const special: Record<string, number> = {
  Enter: 0xff0d, Backspace: 0xff08, Tab: 0xff09, Escape: 0xff1b, Delete: 0xffff,
  Home: 0xff50, End: 0xff57, PageUp: 0xff55, PageDown: 0xff56,
  ArrowLeft: 0xff51, ArrowUp: 0xff52, ArrowRight: 0xff53, ArrowDown: 0xff54,
  Shift: 0xffe1, Control: 0xffe3, Alt: 0xffe9, Meta: 0xffeb, CapsLock: 0xffe5,
  F1: 0xffbe, F2: 0xffbf, F3: 0xffc0, F4: 0xffc1, F5: 0xffc2, F6: 0xffc3,
  F7: 0xffc4, F8: 0xffc5, F9: 0xffc6, F10: 0xffc7, F11: 0xffc8, F12: 0xffc9,
};
function keysym(e: KeyboardEvent): number | null {
  if (special[e.key] !== undefined) return special[e.key];
  if (e.key.length === 1) return e.key.charCodeAt(0);
  return null;
}
function pos(e: MouseEvent): [number, number] {
  const c = canvas.value!;
  const r = c.getBoundingClientRect();
  const x = Math.round(((e.clientX - r.left) / r.width) * c.width);
  const y = Math.round(((e.clientY - r.top) / r.height) * c.height);
  return [Math.max(0, Math.min(c.width - 1, x)), Math.max(0, Math.min(c.height - 1, y))];
}

onMounted(async () => {
  let view: VncView;
  try {
    view = await props.connect();
  } catch (e) {
    status.value = String(e);
    emit("state", "stopped");
    return;
  }
  tabId = view.tabId;
  viewOnly = !!view.viewOnly;
  emit("state", "connected");
  const c = canvas.value!;
  c.width = view.width || 1024;
  c.height = view.height || 768;
  ctx = c.getContext("2d");
  status.value = "";
  offFrame = Events.On("vnc:frame:" + tabId, (ev: { data: unknown }) => {
    const d = ev.data as { x: number; y: number; png: string };
    const img = new Image();
    img.onload = () => ctx && ctx.drawImage(img, d.x, d.y);
    img.src = "data:image/png;base64," + d.png;
  });
  offClose = Events.On("vnc:close:" + tabId, (ev: { data: unknown }) => {
    status.value = "[disconnected] " + String(ev.data ?? "");
    emit("state", "stopped");
  });
  offClip = Events.On("vnc:clipboard:" + tabId, (ev: { data: unknown }) => {
    try { void navigator.clipboard.writeText(String(ev.data ?? "")); } catch { /* no permission */ }
  });
  await VncStart(tabId);
  if (props.active === false) void VncSetPaused(tabId, true);
  c.focus();
});
// A hidden tab asks for no framebuffer updates; showing it again resumes them.
watch(() => props.active, (on) => { if (tabId) void VncSetPaused(tabId, on === false); });

onBeforeUnmount(() => {
  offFrame?.();
  offClose?.();
  offClip?.();
  if (tabId) void VncDisconnect(tabId);
});

let viewOnly = false; // the session sends no input (the backend refuses it too)
const held = new Set<number>();
let release: Awaited<ReturnType<typeof releaseCombo>> = null;
void releaseCombo().then((c) => (release = c));
function onMove(e: MouseEvent) {
  if (viewOnly) return;
  const [x, y] = pos(e);
  input(() => VncPointer(tabId, buttons, x, y));
}
function onDown(e: MouseEvent) {
  canvas.value?.focus();
  if (viewOnly) return;
  buttons |= 1 << e.button; // 0 left,1 middle,2 right
  const [x, y] = pos(e);
  input(() => VncPointer(tabId, buttons, x, y));
}
function onUp(e: MouseEvent) {
  if (viewOnly) return;
  buttons &= ~(1 << e.button);
  const [x, y] = pos(e);
  input(() => VncPointer(tabId, buttons, x, y));
}
function onWheel(e: WheelEvent) {
  if (viewOnly) return;
  const [x, y] = pos(e);
  const bit = e.deltaY < 0 ? 8 : 16; // wheel up / down
  input(() => VncPointer(tabId, buttons | bit, x, y));
  input(() => VncPointer(tabId, buttons, x, y));
}
function onKey(e: KeyboardEvent, down: boolean) {
  if (viewOnly) return;
  if (down && isRelease(e, release)) { // keyboard release: focus back to the app, nothing more is sent
    e.preventDefault();
    for (const k of held) input(() => VncKey(tabId, false, k));
    held.clear();
    canvas.value?.blur();
    return;
  }
  const k = keysym(e);
  if (k === null) return;
  e.preventDefault();
  if (down) held.add(k); else held.delete(k);
  input(() => VncKey(tabId, down, k));
}
</script>

<template>
  <div class="vnc flex h-full w-full items-center justify-center overflow-auto bg-black">
    <canvas
      ref="canvas"
      tabindex="0"
      class="vnc-canvas"
      @mousemove="onMove"
      @mousedown="onDown"
      @mouseup="onUp"
      @wheel.prevent="onWheel"
      @contextmenu.prevent
      @focus="pushLocalClipboard"
      @keydown="onKey($event, true)"
      @keyup="onKey($event, false)"
    ></canvas>
    <div v-if="status" class="status">{{ status }}</div>
  </div>
</template>

<style scoped>
.vnc { position: relative; }
.vnc-canvas { image-rendering: pixelated; outline: none; max-width: 100%; max-height: 100%; }
.status { position: absolute; top: 8px; left: 8px; color: #cbd5e1; background: rgba(0, 0, 0, .6); padding: 4px 8px; font-size: 12px; }
</style>

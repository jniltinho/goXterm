<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from "vue";
import { Events } from "@wailsio/runtime";
import { RdpStart, RdpMouse, RdpKey, RdpDisconnect, type RdpView } from "./api";

const props = defineProps<{ connect: () => Promise<RdpView> }>();

const canvas = ref<HTMLCanvasElement | null>(null);
const status = ref("Connecting…");
let tabId = "";
let ctx: CanvasRenderingContext2D | null = null;
let offFrame: (() => void) | undefined;
let offReady: (() => void) | undefined;
let offClose: (() => void) | undefined;

// browser KeyboardEvent.code → PC Set-1 scancode (common keys; extended keys are a follow-up).
const SC: Record<string, number> = {
  Escape: 0x01, Digit1: 0x02, Digit2: 0x03, Digit3: 0x04, Digit4: 0x05, Digit5: 0x06, Digit6: 0x07,
  Digit7: 0x08, Digit8: 0x09, Digit9: 0x0a, Digit0: 0x0b, Minus: 0x0c, Equal: 0x0d, Backspace: 0x0e,
  Tab: 0x0f, KeyQ: 0x10, KeyW: 0x11, KeyE: 0x12, KeyR: 0x13, KeyT: 0x14, KeyY: 0x15, KeyU: 0x16,
  KeyI: 0x17, KeyO: 0x18, KeyP: 0x19, BracketLeft: 0x1a, BracketRight: 0x1b, Enter: 0x1c,
  ControlLeft: 0x1d, KeyA: 0x1e, KeyS: 0x1f, KeyD: 0x20, KeyF: 0x21, KeyG: 0x22, KeyH: 0x23,
  KeyJ: 0x24, KeyK: 0x25, KeyL: 0x26, Semicolon: 0x27, Quote: 0x28, Backquote: 0x29, ShiftLeft: 0x2a,
  Backslash: 0x2b, KeyZ: 0x2c, KeyX: 0x2d, KeyC: 0x2e, KeyV: 0x2f, KeyB: 0x30, KeyN: 0x31, KeyM: 0x32,
  Comma: 0x33, Period: 0x34, Slash: 0x35, ShiftRight: 0x36, AltLeft: 0x38, Space: 0x39, CapsLock: 0x3a,
};

function pos(e: MouseEvent): [number, number] {
  const c = canvas.value!;
  const r = c.getBoundingClientRect();
  const x = Math.round(((e.clientX - r.left) / r.width) * c.width);
  const y = Math.round(((e.clientY - r.top) / r.height) * c.height);
  return [Math.max(0, Math.min(c.width - 1, x)), Math.max(0, Math.min(c.height - 1, y))];
}

onMounted(async () => {
  let view: RdpView;
  try {
    view = await props.connect();
  } catch (e) {
    status.value = String(e);
    return;
  }
  tabId = view.tabId;
  const c = canvas.value!;
  c.width = view.width || 1280;
  c.height = view.height || 800;
  ctx = c.getContext("2d");
  status.value = "Negotiating…";
  offFrame = Events.On("rdp:frame:" + tabId, (ev: { data: unknown }) => {
    const d = ev.data as { x: number; y: number; png: string };
    const img = new Image();
    img.onload = () => ctx && ctx.drawImage(img, d.x, d.y);
    img.src = "data:image/png;base64," + d.png;
    status.value = "";
  });
  offReady = Events.On("rdp:ready:" + tabId, () => (status.value = ""));
  offClose = Events.On("rdp:close:" + tabId, (ev: { data: unknown }) => {
    status.value = "[disconnected] " + String(ev.data ?? "");
  });
  await RdpStart(tabId);
  c.focus();
});

onBeforeUnmount(() => {
  offFrame?.();
  offReady?.();
  offClose?.();
  if (tabId) void RdpDisconnect(tabId);
});

function onMove(e: MouseEvent) { const [x, y] = pos(e); void RdpMouse(tabId, "move", 0, x, y); }
function onDown(e: MouseEvent) { canvas.value?.focus(); const [x, y] = pos(e); void RdpMouse(tabId, "down", e.button, x, y); }
function onUp(e: MouseEvent) { const [x, y] = pos(e); void RdpMouse(tabId, "up", e.button, x, y); }
function onWheel(e: WheelEvent) { const [x, y] = pos(e); void RdpMouse(tabId, "wheel", e.deltaY < 0 ? 120 : -120, x, y); }
function onKey(e: KeyboardEvent, down: boolean) {
  const sc = SC[e.code];
  if (sc === undefined) return;
  e.preventDefault();
  void RdpKey(tabId, down, sc);
}
</script>

<template>
  <div class="rdp flex h-full w-full items-center justify-center overflow-auto bg-black">
    <canvas
      ref="canvas"
      tabindex="0"
      class="rdp-canvas"
      @mousemove="onMove"
      @mousedown="onDown"
      @mouseup="onUp"
      @wheel.prevent="onWheel"
      @contextmenu.prevent
      @keydown="onKey($event, true)"
      @keyup="onKey($event, false)"
    ></canvas>
    <div v-if="status" class="status">{{ status }}</div>
  </div>
</template>

<style scoped>
.rdp { position: relative; }
.rdp-canvas { image-rendering: pixelated; outline: none; max-width: 100%; max-height: 100%; }
.status { position: absolute; top: 8px; left: 8px; color: #cbd5e1; background: rgba(0, 0, 0, .6); padding: 4px 8px; font-size: 12px; }
</style>

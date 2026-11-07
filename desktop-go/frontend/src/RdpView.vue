<script setup lang="ts">
import { ordered } from "./ordered";
import { releaseCombo, isRelease } from "./release";
import { ref, nextTick, onMounted, onBeforeUnmount } from "vue";
import { Events } from "@wailsio/runtime";
import { RdpStart, RdpMouse, RdpKey, RdpDisconnect, RdpSyncClipboard, type RdpView } from "./api";
const input = ordered(); // pointer and key events reach the session in order

const props = defineProps<{ connect: (tabW: number, tabH: number) => Promise<RdpView> }>();
const emit = defineEmits<{ close: []; state: ["connecting" | "connected" | "stopped"] }>();

const root = ref<HTMLDivElement | null>(null);
const canvas = ref<HTMLCanvasElement | null>(null);
const overlay = ref<HTMLDivElement | null>(null);
const status = ref("Connecting…");
// Set once the session ends (logoff, network drop, server kick): the last frame stays, dimmed,
// under a "Session stopped" notice — Return/Ctrl+D close the tab, R reconnects (terminal parity).
const stopped = ref<{ reason: string } | null>(null);
let tabId = "";
let ctx: CanvasRenderingContext2D | null = null;
let offFrame: (() => void) | undefined;
let offReady: (() => void) | undefined;
let offClose: (() => void) | undefined;
let ro: ResizeObserver | undefined;

// The remote desktop is created at the tab size ("fit to tab", Rust parity); when the window is
// resized later it is scaled to fit, keeping its aspect ratio (pos() maps the mouse back).
function fitCanvas() {
  const c = canvas.value, r = root.value;
  if (!c || !r || !c.width || !c.height) return;
  const s = Math.min(r.clientWidth / c.width, r.clientHeight / c.height);
  if (!(s > 0)) return;
  c.style.width = Math.floor(c.width * s) + "px";
  c.style.height = Math.floor(c.height * s) + "px";
}

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

// Server disconnect reasons → plain words (the raw reason stays visible, muted).
function friendly(raw: string): string {
  if (/ULTIMATUM/i.test(raw)) return "the remote session ended (logged off or disconnected by the server)";
  if (/EOF|reset|closed/i.test(raw)) return "the connection was closed";
  return raw || "disconnected";
}

function pos(e: MouseEvent): [number, number] {
  const c = canvas.value!;
  const r = c.getBoundingClientRect();
  const x = Math.round(((e.clientX - r.left) / r.width) * c.width);
  const y = Math.round(((e.clientY - r.top) / r.height) * c.height);
  return [Math.max(0, Math.min(c.width - 1, x)), Math.max(0, Math.min(c.height - 1, y))];
}

function unsubscribe() {
  offFrame?.(); offReady?.(); offClose?.();
  offFrame = offReady = offClose = undefined;
}

async function start() {
  emit("state", "connecting");
  stopped.value = null;
  status.value = "Connecting…";
  let view: RdpView;
  try {
    view = await props.connect(Math.floor(root.value?.clientWidth ?? 0), Math.floor(root.value?.clientHeight ?? 0));
  } catch (e) {
    status.value = "";
    emit("state", "stopped");
    stopped.value = { reason: String(e) };
    await nextTick();
    overlay.value?.focus();
    return;
  }
  tabId = view.tabId;
  const c = canvas.value!;
  c.width = view.width || 1280;
  c.height = view.height || 800;
  fitCanvas();
  ctx = c.getContext("2d");
  status.value = "Negotiating…";
  offFrame = Events.On("rdp:frame:" + tabId, (ev: { data: unknown }) => {
    const d = ev.data as { x: number; y: number; png: string };
    const img = new Image();
    img.onload = () => ctx && ctx.drawImage(img, d.x, d.y);
    img.src = "data:image/png;base64," + d.png;
    status.value = "";
  });
  offReady = Events.On("rdp:ready:" + tabId, () => { status.value = ""; emit("state", "connected"); });
  offClose = Events.On("rdp:close:" + tabId, async (ev: { data: unknown }) => {
    status.value = "";
    emit("state", "stopped");
    stopped.value = { reason: String(ev.data ?? "") };
    await nextTick();
    overlay.value?.focus();
  });
  await RdpStart(tabId);
  c.focus();
  void RdpSyncClipboard(tabId); // the focus above may have fired before tabId was known
}

async function reconnect() {
  unsubscribe();
  if (tabId) { void RdpDisconnect(tabId); tabId = ""; }
  ctx?.clearRect(0, 0, canvas.value?.width ?? 0, canvas.value?.height ?? 0);
  await start();
}

function onStoppedKey(e: KeyboardEvent) {
  const k = e.key.toLowerCase();
  if (e.key === "Enter" || (e.ctrlKey && k === "d")) { e.preventDefault(); emit("close"); }
  else if (k === "r" && !e.ctrlKey && !e.metaKey && !e.altKey) { e.preventDefault(); void reconnect(); }
}

onMounted(() => {
  ro = new ResizeObserver(fitCanvas);
  if (root.value) ro.observe(root.value);
  void start();
});

onBeforeUnmount(() => {
  ro?.disconnect();
  unsubscribe();
  if (tabId) void RdpDisconnect(tabId);
});

// Offer what was copied locally to the remote desktop each time the view gets focus.
function onFocus() { if (tabId && !stopped.value) void RdpSyncClipboard(tabId); }
function onMove(e: MouseEvent) { if (!stopped.value) { const [x, y] = pos(e); input(() => RdpMouse(tabId, "move", 0, x, y)); } }
function onDown(e: MouseEvent) { if (stopped.value) return; canvas.value?.focus(); const [x, y] = pos(e); input(() => RdpMouse(tabId, "down", e.button, x, y)); }
function onUp(e: MouseEvent) { if (!stopped.value) { const [x, y] = pos(e); input(() => RdpMouse(tabId, "up", e.button, x, y)); } }
function onWheel(e: WheelEvent) { if (!stopped.value) { const [x, y] = pos(e); input(() => RdpMouse(tabId, "wheel", e.deltaY < 0 ? 120 : -120, x, y)); } }
// Keys sent down and not yet up, released on the remote side when the keyboard is released.
const held = new Set<number>();
let release: Awaited<ReturnType<typeof releaseCombo>> = null;
void releaseCombo().then((c) => (release = c));
function onKey(e: KeyboardEvent, down: boolean) {
  if (stopped.value) return;
  if (down && isRelease(e, release)) { // keyboard release: focus back to the app, nothing more is sent
    e.preventDefault();
    for (const sc of held) input(() => RdpKey(tabId, false, sc));
    held.clear();
    canvas.value?.blur();
    return;
  }
  const sc = SC[e.code];
  if (sc === undefined) return;
  e.preventDefault();
  if (down) held.add(sc); else held.delete(sc);
  input(() => RdpKey(tabId, down, sc));
}
</script>

<template>
  <div ref="root" class="rdp flex h-full w-full items-center justify-center overflow-hidden bg-black">
    <canvas
      ref="canvas"
      tabindex="0"
      class="rdp-canvas"
      :class="stopped ? 'dim' : ''"
      @mousemove="onMove"
      @mousedown="onDown"
      @mouseup="onUp"
      @wheel.prevent="onWheel"
      @contextmenu.prevent
      @focus="onFocus"
      @keydown="onKey($event, true)"
      @keyup="onKey($event, false)"
    ></canvas>
    <div v-if="status && !stopped" class="status">{{ status }}</div>
    <div v-if="stopped" ref="overlay" class="stopped" tabindex="0" @keydown="onStoppedKey">
      <div class="stopped-card">
        <div class="stopped-rule"></div>
        <div class="stopped-title">✖ Session stopped</div>
        <div class="stopped-why">{{ friendly(stopped.reason) }}</div>
        <div v-if="stopped.reason && friendly(stopped.reason) !== stopped.reason" class="stopped-raw">{{ stopped.reason }}</div>
        <ul class="stopped-keys">
          <li>Press <b>Return</b> or <b>Ctrl+D</b> to close this tab</li>
          <li>Press <b>R</b> to reconnect</li>
        </ul>
        <div class="stopped-actions">
          <button class="sbtn primary" @click="reconnect">Reconnect</button>
          <button class="sbtn" @click="emit('close')">Close tab</button>
        </div>
        <div class="stopped-rule"></div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.rdp { position: relative; }
.rdp-canvas { outline: none; transition: filter .15s; }
.rdp-canvas.dim { filter: grayscale(.6) brightness(.45); }
.status { position: absolute; top: 8px; left: 8px; color: #cbd5e1; background: rgba(0, 0, 0, .6); padding: 4px 8px; font-size: 12px; }
.stopped { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; outline: none; }
.stopped-card { min-width: 420px; max-width: 80%; background: #0b0e11; border: 1px solid #c0392b; padding: 14px 18px; font-family: "DejaVu Sans Mono", monospace; font-size: 13px; color: #cbd5e1; box-shadow: 0 10px 30px rgba(0,0,0,.6); }
.stopped-rule { border-top: 1px dashed #c0392b; margin: 2px 0 10px; }
.stopped-card .stopped-rule:last-child { margin: 12px 0 2px; }
.stopped-title { color: #e05561; font-weight: 700; }
.stopped-why { margin-top: 4px; }
.stopped-raw { margin-top: 2px; color: #6b7a8a; font-size: 11.5px; word-break: break-all; }
.stopped-keys { margin: 10px 0 0; padding-left: 18px; color: #8a97a5; }
.stopped-keys b { color: #d6a8f0; font-weight: 600; }
.stopped-actions { display: flex; gap: 8px; margin-top: 12px; }
.sbtn { height: 26px; padding: 0 14px; font-size: 12px; background: #1b2129; color: #cbd5e1; border: 1px solid #3a4654; cursor: pointer; font-family: inherit; }
.sbtn:hover { border-color: #6aa0da; }
.sbtn.primary { background: #2f6fb3; border-color: #2f6fb3; color: #fff; }
</style>

<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { Events } from "@wailsio/runtime";
import "@xterm/xterm/css/xterm.css";
import { ref as vref } from "vue";
import { Send, Resize, Disconnect, MonitorStart, MonitorStop, GetSettings, TermAttach, SaveTerminalOutput, type Attach, type Metrics } from "./api";
import { renderBanner } from "./banner";

// connect returns the tabId once the terminal is open; the parent decides how (session/quick/demo).
// monitor shows the remote-monitoring bar (SSH only).
const props = defineProps<{ connect: () => Promise<string>; monitor?: boolean }>();
const emit = defineEmits<{ close: []; state: ["connecting" | "connected" | "stopped"] }>();

const metrics = vref<Metrics | null>(null);
const cpuHistory = vref<number[]>([]);
let offMon: (() => void) | undefined;
// warning/danger colouring and a tiny CPU sparkline for the monitoring bar.
function lvl(pct: number, warn: number, danger: number) { return pct >= danger ? "danger" : pct >= warn ? "warn" : ""; }
function sparkPath(h: number[]): string {
  if (h.length < 2) return "";
  const w = 38, ht = 11, n = h.length;
  return h.map((v, i) =>
    `${i === 0 ? "M" : "L"}${((i / (n - 1)) * w).toFixed(1)},${(ht - (Math.max(0, Math.min(100, v)) / 100) * ht).toFixed(1)}`,
  ).join(" ");
}
const fmtRate = (b: number) => (b >= 1048576 ? (b / 1048576).toFixed(1) + " MB/s" : b >= 1024 ? (b / 1024).toFixed(0) + " kB/s" : b + " B/s");
const fmtSize = (kib: number) => (kib >= 1048576 ? (kib / 1048576).toFixed(1) + " GiB" : (kib / 1024).toFixed(0) + " MiB");
function fmtUptime(s: number) {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  return d > 0 ? `${d}d ${h}h` : h > 0 ? `${h}h ${m}m` : `${m}m`;
}

const el = ref<HTMLDivElement | null>(null);
let term: Terminal;
let fit: FitAddon;
let tabId = "";
let offOut: (() => void) | undefined;
let offClose: (() => void) | undefined;
let ro: ResizeObserver | undefined;
let stopped = false;
let dark = true; // terminal scheme brightness, for the banner colours
// Live events that arrive before TermAttach answered are queued, so the banner and the buffered
// early output always come first and nothing is reordered.
let attached = false;
let pending: Array<() => void> = [];
const whenAttached = (f: () => void) => (attached ? f() : pending.push(f));

// The Rust app's stopped notice, verbatim: a red dashed rule, the reason, and the keys the tab
// answers to (Ctrl+D also closes, as an alias of Return).
function showStopped(why: string) {
  stopped = true;
  emit("state", "stopped");
  const red = "\x1b[31m", muted = "\x1b[90m", key = "\x1b[35m", rst = "\x1b[0m";
  const rule = red + "-".repeat(Math.max(1, (term.cols || 60) - 1)) + rst;
  const nl = term.buffer.active.cursorX > 0 ? "\r\n" : "";
  term.writeln(nl + rule);
  term.writeln(red + "Session stopped" + rst + (why ? " " + muted + "(" + why + ")" + rst : ""));
  term.writeln("    - Press " + key + "<Return>" + rst + " to exit tab");
  term.writeln("    - Press " + key + "R" + rst + " to restart session");
  term.writeln("    - Press " + key + "S" + rst + " to save terminal output to file");
}

// S on a stopped tab: the whole scrollback as plain text, through a native save dialog.
async function saveOutput() {
  const b = term.buffer.active;
  const lines: string[] = [];
  for (let i = 0; i < b.length; i++) lines.push(b.getLine(i)?.translateToString(true) ?? "");
  while (lines.length && !lines[lines.length - 1]) lines.pop();
  const ts = new Date().toISOString().slice(0, 19).replace(/[-:]/g, "").replace("T", "-");
  try {
    const path = await SaveTerminalOutput(`terminal-${ts}.log`, lines.join("\n") + "\n");
    if (path) term.writeln("\x1b[90m    Saved to " + path + "\x1b[0m");
  } catch (e) {
    term.writeln("\x1b[31m    " + String(e) + "\x1b[0m");
  }
}

// Zoom (Ctrl+wheel, Ctrl+=/-/0) and find (Ctrl+Shift+F), as advertised in the session banner.
let baseFont = 13;
const finding = vref(false);
const findText = vref("");
const findInput = ref<HTMLInputElement | null>(null);
let search: SearchAddon;
function zoom(delta: number) {
  const next = delta === 0 ? baseFont : Math.min(40, Math.max(6, (term.options.fontSize ?? baseFont) + delta));
  term.options.fontSize = next;
  fit.fit();
  if (tabId) void Resize(tabId, term.cols, term.rows);
}
const FIND_DECO = { matchBackground: "#5c4a00", activeMatchBackground: "#c08a00", matchOverviewRuler: "#c08a00", activeMatchColorOverviewRuler: "#ffd200" };
function findNext(back = false) {
  if (!findText.value) return;
  if (back) search.findPrevious(findText.value, { decorations: FIND_DECO });
  else search.findNext(findText.value, { decorations: FIND_DECO });
}
async function openFind() {
  finding.value = true;
  await new Promise((r) => setTimeout(r));
  findInput.value?.focus();
  findInput.value?.select();
}
function closeFind() {
  finding.value = false;
  search.clearDecorations();
  term.focus();
}

// wireEvents subscribes to this tab's output/close streams (re-run on restart with a new tabId).
function wireEvents() {
  attached = false;
  pending = [];
  offOut = Events.On("term:out:" + tabId, (ev: { data: unknown }) => {
    const bytes = b64(String(ev.data));
    whenAttached(() => term.write(bytes));
  });
  offClose = Events.On("term:close:" + tabId, (ev: { data: unknown }) => {
    const why = String(ev.data ?? "");
    whenAttached(() => showStopped(why));
  });
}

// attach replaces the "Connecting…" line with the session banner, writes the output that arrived
// before we were listening, then releases the queued live events.
async function attach() {
  let a: Attach | null = null;
  try { a = await TermAttach(tabId); } catch { /* older backend: no banner, nothing buffered */ }
  term.write("\x1b[1A\r\x1b[2K");
  if (a?.banner) term.write(renderBanner(a.banner, term.cols, dark));
  if (a?.early) term.write(b64(a.early));
  attached = true;
  emit("state", "connected");
  const queued = pending;
  pending = [];
  for (const f of queued) f();
  if (a?.closed) showStopped(a.closeMsg);
}

function startMon() {
  if (!props.monitor || !tabId) return;
  offMon?.();
  offMon = Events.On("monitor:" + tabId, (ev: { data: unknown }) => {
    const m = ev.data as Metrics;
    metrics.value = m;
    if (m.cpu >= 0) cpuHistory.value = [...cpuHistory.value, m.cpu].slice(-24);
  });
  void MonitorStart(tabId);
}

async function restart() {
  offOut?.();
  offClose?.();
  offMon?.();
  metrics.value = null;
  cpuHistory.value = [];
  stopped = false;
  term.reset();
  term.writeln("\x1b[90mReconnecting…\x1b[0m");
  emit("state", "connecting");
  try {
    tabId = await props.connect();
  } catch (e) {
    term.writeln("\r\n\x1b[31m" + String(e) + "\x1b[0m");
    emit("state", "stopped");
    stopped = true;
    return;
  }
  wireEvents();
  await attach();
  startMon();
  void Resize(tabId, term.cols, term.rows);
  term.focus();
}

function b64(b64: string): Uint8Array {
  const bin = atob(b64);
  const u = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) u[i] = bin.charCodeAt(i);
  return u;
}

// Terminal color schemes (xterm.js theme) selectable in Settings.
const SCHEMES: Record<string, Record<string, string>> = {
  xterm: { background: "#0b0e11", foreground: "#d4d4d4", cursor: "#9ece6a", selectionBackground: "#2a3f52" },
  dark: { background: "#1e1e1e", foreground: "#d4d4d4", cursor: "#ffffff", selectionBackground: "#264f78" },
  light: { background: "#ffffff", foreground: "#1e1e1e", cursor: "#000000", selectionBackground: "#add6ff" },
  "solarized-dark": { background: "#002b36", foreground: "#839496", cursor: "#93a1a1", selectionBackground: "#073642" },
  "solarized-light": { background: "#fdf6e3", foreground: "#657b83", cursor: "#586e75", selectionBackground: "#eee8d5" },
};

onMounted(async () => {
  const tp = { fontFamily: "DejaVu Sans Mono", fontSize: 13, cursorShape: "beam", cursorBlink: true, scrollbackLines: 10000, scheme: "xterm" };
  try { Object.assign(tp, (await GetSettings()).terminal); } catch { /* defaults */ }
  const cursorStyle = tp.cursorShape === "block" ? "block" : tp.cursorShape === "underline" ? "underline" : "bar";
  dark = !/light/.test(tp.scheme);
  baseFont = tp.fontSize || 13;
  term = new Terminal({
    fontFamily: `"${tp.fontFamily}","DejaVu Sans Mono",monospace`,
    fontSize: baseFont,
    cursorBlink: tp.cursorBlink,
    cursorStyle,
    scrollback: tp.scrollbackLines || 10000,
    theme: SCHEMES[tp.scheme] || SCHEMES.xterm,
    allowProposedApi: true, // search-match decorations
  });
  fit = new FitAddon();
  term.loadAddon(fit);
  search = new SearchAddon();
  term.loadAddon(search);
  // App-level terminal keys are handled here and never reach the remote.
  term.attachCustomKeyEventHandler((e) => {
    if (e.type !== "keydown" || !(e.ctrlKey || e.metaKey)) return true;
    if (e.shiftKey && e.code === "KeyF") { e.preventDefault(); void openFind(); return false; }
    if (e.shiftKey || e.altKey) return true;
    if (e.key === "=" || e.key === "+") { e.preventDefault(); zoom(1); return false; }
    if (e.key === "-") { e.preventDefault(); zoom(-1); return false; }
    if (e.key === "0") { e.preventDefault(); zoom(0); return false; }
    return true;
  });
  term.open(el.value!);
  el.value!.addEventListener("wheel", (e) => {
    if (!e.ctrlKey) return;
    e.preventDefault();
    zoom(e.deltaY < 0 ? 1 : -1);
  }, { passive: false, capture: true });
  fit.fit();
  term.writeln("\x1b[90mConnecting…\x1b[0m");
  try {
    tabId = await props.connect();
  } catch (e) {
    term.writeln("\r\n\x1b[31m" + String(e) + "\x1b[0m");
    emit("state", "stopped");
    return;
  }
  wireEvents();
  await attach();
  startMon();
  term.onData((d) => {
    if (stopped) {
      if (d === "\r" || d === "\x04") emit("close"); // Return or Ctrl+D closes the tab
      else if (d === "r" || d === "R") void restart();
      else if (d === "s" || d === "S") void saveOutput();
      return;
    }
    void Send(tabId, d);
  });
  void Resize(tabId, term.cols, term.rows);
  ro = new ResizeObserver(() => {
    fit.fit();
    if (tabId) void Resize(tabId, term.cols, term.rows);
  });
  ro.observe(el.value!);
  term.focus();
});

onBeforeUnmount(() => {
  ro?.disconnect();
  offOut?.();
  offClose?.();
  offMon?.();
  if (tabId && props.monitor) void MonitorStop(tabId);
  if (tabId) void Disconnect(tabId);
  term?.dispose();
});
</script>

<template>
  <div class="term-wrap">
    <div v-if="finding" class="findbar">
      <input
        ref="findInput"
        v-model="findText"
        class="find-in"
        placeholder="Find"
        @input="findNext()"
        @keydown.enter.prevent="findNext($event.shiftKey)"
        @keydown.esc.prevent="closeFind"
      />
      <button class="find-b" title="Previous (Shift+Enter)" @click="findNext(true)">▲</button>
      <button class="find-b" title="Next (Enter)" @click="findNext()">▼</button>
      <button class="find-b" title="Close (Esc)" @click="closeFind">✕</button>
    </div>
    <div ref="el" class="term-area"></div>
    <div v-if="monitor && metrics" class="mon">
      <span class="seg mon-host" :title="metrics.os">{{ metrics.host || "—" }}</span>
      <span class="seg" :class="lvl(metrics.cpu, 70, 90)" v-if="metrics.cpu >= 0" title="CPU">
        <i>CPU</i>{{ metrics.cpu.toFixed(0) }}%
        <svg v-if="cpuHistory.length > 1" class="spark" viewBox="0 0 38 11" preserveAspectRatio="none"><path :d="sparkPath(cpuHistory)" /></svg>
      </span>
      <span class="seg" :class="lvl((metrics.memUsed / metrics.memTotal) * 100, 80, 95)" v-if="metrics.memTotal" title="Memory"><i>MEM</i>{{ fmtSize(metrics.memUsed / 1024) }} / {{ fmtSize(metrics.memTotal / 1024) }}</span>
      <span class="seg" title="Network up"><i>↑</i>{{ fmtRate(metrics.netUp) }}</span>
      <span class="seg" title="Network down"><i>↓</i>{{ fmtRate(metrics.netDown) }}</span>
      <span class="seg" v-if="metrics.uptime" title="Uptime"><i>UP</i>{{ fmtUptime(metrics.uptime) }}</span>
      <span class="seg" :class="lvl(d.usedPct, 80, 90)" v-for="d in (metrics.disks || []).slice(0, 4)" :key="d.mount" :title="d.mount"><i>{{ d.mount }}</i>{{ d.usedPct }}%</span>
    </div>
  </div>
</template>

<style scoped>
.term-wrap { position: relative; display: flex; flex-direction: column; height: 100%; width: 100%; }
.findbar { position: absolute; top: 6px; right: 18px; z-index: 5; display: flex; gap: 2px; padding: 3px; background: #1b2129; border: 1px solid #3a4654; box-shadow: 0 4px 14px rgba(0,0,0,.5); }
.find-in { width: 220px; height: 24px; padding: 0 8px; font-size: 12px; color: #e6edf3; background: #0b0e11; border: 1px solid #3a4654; outline: none; }
.find-in:focus { border-color: #6aa0da; }
.find-b { width: 24px; height: 24px; font-size: 11px; color: #cbd5e1; background: #222a33; border: 1px solid #3a4654; cursor: pointer; }
.find-b:hover { border-color: #6aa0da; }
.term-area { flex: 1; min-height: 0; }
.mon { display: flex; align-items: center; height: 22px; padding: 0 2px; background: #11161c; border-top: 1px solid #2a3340; font-size: 11px; color: #9fb0c0; overflow: hidden; white-space: nowrap; }
.seg { display: inline-flex; align-items: center; gap: 4px; padding: 0 10px; height: 13px; border-right: 1px solid #2a3340; }
.seg:last-child { border-right: none; }
.seg i { font-style: normal; color: #6b7a8a; font-size: 10px; }
.mon-host { color: #cbd5e1; font-weight: 600; }
.seg.warn { color: #e0b341; }
.seg.danger { color: #e05561; }
.spark { width: 38px; height: 11px; margin-left: 2px; }
.spark path { fill: none; stroke: #6aa0da; stroke-width: 1; vector-effect: non-scaling-stroke; }
.seg.warn .spark path { stroke: #e0b341; }
.seg.danger .spark path { stroke: #e05561; }
</style>

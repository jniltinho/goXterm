<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Events } from "@wailsio/runtime";
import "@xterm/xterm/css/xterm.css";
import { ref as vref } from "vue";
import { Send, Resize, Disconnect, MonitorStart, MonitorStop, GetSettings, type Metrics } from "./api";

// connect returns the tabId once the terminal is open; the parent decides how (session/quick/demo).
// monitor shows the remote-monitoring bar (SSH only).
const props = defineProps<{ connect: () => Promise<string>; monitor?: boolean }>();
const emit = defineEmits<{ close: [] }>();

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

// wireEvents subscribes to this tab's output/close streams (re-run on restart with a new tabId).
function wireEvents() {
  offOut = Events.On("term:out:" + tabId, (ev: { data: unknown }) => term.write(b64(String(ev.data))));
  offClose = Events.On("term:close:" + tabId, (ev: { data: unknown }) => {
    stopped = true;
    const why = String(ev.data ?? "");
    const rule = "\x1b[90m" + "─".repeat(Math.min(term.cols || 60, 60)) + "\x1b[0m";
    term.writeln("\r\n" + rule);
    term.writeln("\x1b[31m  ✖ Session stopped\x1b[0m" + (why ? " \x1b[90m(" + why + ")\x1b[0m" : ""));
    term.writeln("\x1b[90m    - Press \x1b[0mReturn\x1b[90m to close this tab\x1b[0m");
    term.writeln("\x1b[90m    - Press \x1b[0mR\x1b[90m to restart the session\x1b[0m");
    term.writeln(rule);
  });
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
  try {
    tabId = await props.connect();
  } catch (e) {
    term.writeln("\r\n\x1b[31m" + String(e) + "\x1b[0m");
    stopped = true;
    return;
  }
  wireEvents();
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
  term = new Terminal({
    fontFamily: `"${tp.fontFamily}","DejaVu Sans Mono",monospace`,
    fontSize: tp.fontSize || 13,
    cursorBlink: tp.cursorBlink,
    cursorStyle,
    scrollback: tp.scrollbackLines || 10000,
    theme: SCHEMES[tp.scheme] || SCHEMES.xterm,
  });
  fit = new FitAddon();
  term.loadAddon(fit);
  term.open(el.value!);
  fit.fit();
  term.writeln("\x1b[90mConnecting…\x1b[0m");
  try {
    tabId = await props.connect();
  } catch (e) {
    term.writeln("\r\n\x1b[31m" + String(e) + "\x1b[0m");
    return;
  }
  wireEvents();
  startMon();
  term.onData((d) => {
    if (stopped) {
      if (d === "\r") emit("close");
      else if (d === "r" || d === "R") void restart();
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
.term-wrap { display: flex; flex-direction: column; height: 100%; width: 100%; }
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

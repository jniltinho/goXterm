<script setup lang="ts">
import { ref, watch, onMounted, onBeforeUnmount } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { Events } from "@wailsio/runtime";
import "@xterm/xterm/css/xterm.css";
import { ref as vref } from "vue";
import { Send, Resize, Disconnect, MonitorStart, MonitorStop, GetSettings, TermAttach, SaveTerminalOutput, ClipboardText, SetClipboardText, type Attach, type Metrics, type TermEnd } from "./api";
import { renderBanner } from "./banner";
import { askConfirm } from "./ask";
import { commandFor } from "./commands";
import { schemeName, schemeIsDark, xtermTheme } from "./schemes";

// connect returns the tabId once the terminal is open; the parent decides how (session/quick/demo).
// monitor shows the remote-monitoring bar (SSH only).
// zoomKey: the tab's URL (ssh://user@host:port, local://, …); the Ctrl+wheel font size is kept per key.
const props = defineProps<{ connect: () => Promise<string>; monitor?: boolean; title?: string; zoomKey?: string }>();
// backend: the backend tabId once connected (the side SFTP browser uses it); enter: the user pressed
// Return (debounced, for "Follow terminal folder"); cwd: the shell reported its folder (OSC 7).
const emit = defineEmits<{
  close: []; duplicate: []; "monitor-off": []; state: ["connecting" | "connected" | "stopped"]; backend: [string]; enter: []; cwd: [string];
}>();

// Context menu (Rust terminal_menu, MobaXterm-like): right click, or Ctrl+right click when right click
// pastes (Settings › General › Paste with right click).
const prefs = { rightClickPaste: true, copyOnSelect: true, pasteGuard: true, banner: true };
const cmenu = vref<{ x: number; y: number; sel: boolean; live: boolean } | null>(null);
function onContext(e: MouseEvent) {
  e.preventDefault();
  if (prefs.rightClickPaste && !e.ctrlKey) { void paste(); return; }
  const w = 210, h = 290; // keep the menu inside the window
  cmenu.value = {
    x: Math.min(e.clientX, window.innerWidth - w), y: Math.min(e.clientY, window.innerHeight - h),
    sel: term.hasSelection(), live: !stopped,
  };
}
function menuRun(f: () => void) { cmenu.value = null; f(); term.focus(); }
const copy = () => { const s = term.getSelection(); if (s) void SetClipboardText(s); };
async function paste() {
  const s = await ClipboardText();
  if (!s || stopped) return;
  const lines = s.replace(/\r?\n$/, "").split(/\r?\n/).length;
  if (prefs.pasteGuard && lines > 1 &&
      !(await askConfirm("Paste multiple lines", `Paste ${lines} lines into "${props.title ?? "terminal"}"?\n\n${s.slice(0, 600)}`, "Paste"))) return;
  term.paste(s);
}
let enterTimer: ReturnType<typeof setTimeout> | undefined;
const mi = (name: string) => `./icons/${name}.png`; // runtime path (public/), not a bundled import

const metrics = vref<Metrics | null>(null);
const cpuHistory = vref<number[]>([]);
let offMon: (() => void) | undefined;
// warning/danger colouring and a tiny CPU sparkline for the monitoring bar.
// Monitoring bar formats and thresholds: Rust monitor_bar.rs (rate in bits/s, 80 % warning, 95 % danger).
function lvl(pct: number) { return pct >= 95 ? "danger" : pct >= 80 ? "warn" : ""; }
const HISTORY = 60, SPARK_W = 48, SPARK_H = 14;
function sparkPath(h: number[]): string {
  const step = SPARK_W / (HISTORY - 1), start = HISTORY - h.length;
  return h.map((v, i) =>
    `${i === 0 ? "M" : "L"}${((start + i) * step).toFixed(1)},${(SPARK_H - 1 - (Math.max(0, Math.min(100, v)) / 100) * (SPARK_H - 2)).toFixed(1)}`,
  ).join(" ");
}
function fmtRate(bytesPerSec: number) {
  const b = bytesPerSec * 8; // the Go collector reports bytes/s; the bar shows bits/s like Rust
  return b >= 1e9 ? (b / 1e9).toFixed(2) + " Gb/s" : b >= 1e6 ? (b / 1e6).toFixed(2) + " Mb/s" : b >= 1e3 ? (b / 1e3).toFixed(1) + " kb/s" : Math.round(b) + " b/s";
}
function fmtMem(bytes: number) {
  const gib = bytes / 2 ** 30;
  return gib >= 1 ? gib.toFixed(2) + " GiB" : (bytes / 2 ** 20).toFixed(0) + " MiB";
}
function fmtUptime(s: number) {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  return d > 0 ? `${d} d ${h} h` : h > 0 ? `${h} h ${m} min` : `${m} min`;
}
// No sample for two intervals: values turn muted (stale), as in Rust.
const lastSample = vref(0);
const now = vref(Date.now());
let staleTimer: ReturnType<typeof setInterval> | undefined;
const stale = () => !!lastSample.value && now.value - lastSample.value > 2 * monInterval * 1000 + 500;
let monInterval = 2;

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
  offMon?.(); // the bar would otherwise keep the last (stale) metrics
  offMon = undefined;
  metrics.value = null;
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
// Per-viewer zoom per host (localStorage "rxt-zoom": {url: fontSize}); Ctrl+0 forgets it.
function zooms(): Record<string, number> {
  try { return JSON.parse(localStorage.getItem("rxt-zoom") || "{}"); } catch { return {}; }
}
function saveZoom(size: number) {
  if (!props.zoomKey) return;
  const z = zooms();
  if (size === baseFont) delete z[props.zoomKey]; else z[props.zoomKey] = size;
  try { localStorage.setItem("rxt-zoom", JSON.stringify(z)); } catch { /* private mode */ }
}
const finding = vref(false);
const findText = vref("");
const findInput = ref<HTMLInputElement | null>(null);
let search: SearchAddon;
function zoom(delta: number) {
  const next = delta === 0 ? baseFont : Math.min(40, Math.max(6, (term.options.fontSize ?? baseFont) + delta));
  term.options.fontSize = next;
  saveZoom(next);
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
    const d = ev.data as { msg?: string; end?: TermEnd | null } | string | null;
    const why = typeof d === "string" ? d : d?.msg ?? "";
    const end = typeof d === "object" && d ? d.end ?? null : null;
    whenAttached(() => ended(why, end));
  });
}

// attach replaces the "Connecting…" line with the session banner, writes the output that arrived
// before we were listening, then releases the queued live events.
async function attach() {
  let a: Attach | null = null;
  try { a = await TermAttach(tabId); } catch { /* older backend: no banner, nothing buffered */ }
  term.write("\x1b[1A\r\x1b[2K");
  if (a?.banner && prefs.banner) term.write(renderBanner(a.banner, term.cols, dark));
  if (a?.early) term.write(b64(a.early));
  attached = true;
  emit("state", "connected");
  const queued = pending;
  pending = [];
  for (const f of queued) f();
  retries = 0; // connected: the next drop starts the backoff again
  if (a?.closed) ended(a.closeMsg, a.end);
}

// ended handles the end of the session as the session's options say (the Rust engine): close the
// tab after its remote command; after a dropped connection, reconnect with backoff (2, 4 … 60 s).
let retries = 0;
let retryTimer: ReturnType<typeof setTimeout> | undefined;
function ended(why: string, end: TermEnd | null) {
  if (end?.closeTab) {
    emit("close");
    return;
  }
  showStopped(why);
  if (end?.reconnect) scheduleReconnect();
}
function scheduleReconnect() {
  const secs = Math.min(2 << Math.min(retries, 5), 60);
  retries++;
  term.writeln("\x1b[90m    Reconnecting automatically in " + secs + " s\x1b[0m");
  clearTimeout(retryTimer);
  retryTimer = setTimeout(() => void restart(true), secs * 1000);
}

function stopMon() {
  offMon?.();
  offMon = undefined;
  metrics.value = null;
  cpuHistory.value = [];
  if (tabId) void MonitorStop(tabId);
}
watch(() => props.monitor, (on) => { if (on) startMon(); else stopMon(); });

function startMon() {
  if (!props.monitor || !tabId || stopped) return;
  offMon?.();
  offMon = Events.On("monitor:" + tabId, (ev: { data: unknown }) => {
    const m = ev.data as Metrics;
    metrics.value = m;
    lastSample.value = Date.now();
    if (m.cpu >= 0) cpuHistory.value = [...cpuHistory.value, m.cpu].slice(-HISTORY);
  });
  void MonitorStart(tabId);
}

async function restart(auto = false) {
  clearTimeout(retryTimer);
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
    if (auto) scheduleReconnect(); // still unreachable: keep trying
    return;
  }
  emit("backend", tabId);
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

// Colour scheme: the configured one, or the UI theme's (follows a live Skin or OS change).
let schemeSetting = "";
function applyScheme() {
  if (!term) return; // settings arrived before the terminal was created
  const name = schemeName(schemeSetting);
  dark = schemeIsDark(name);
  term.options.theme = xtermTheme(name);
  if (el.value) el.value.style.background = term.options.theme.background ?? "";
}
function onSettings(e: Event) {
  schemeSetting = (e as CustomEvent<{ terminal?: { scheme?: string } }>).detail?.terminal?.scheme ?? schemeSetting;
  applyScheme();
}
const osScheme = matchMedia("(prefers-color-scheme: dark)");
window.addEventListener("rxt-settings", onSettings);
osScheme.addEventListener("change", applyScheme);

onMounted(async () => {
  const tp = { fontFamily: "", fontSize: 13, cursorShape: "beam", cursorBlink: true, scrollbackLines: 10000, scheme: "" };
  try {
    const s = await GetSettings();
    Object.assign(tp, s.terminal);
    prefs.rightClickPaste = s.rightClickPaste;
    prefs.copyOnSelect = s.copyOnSelect;
    monInterval = Math.min(30, Math.max(1, s.remoteMonitoringInterval || 2));
    prefs.pasteGuard = s.pasteGuard;
    prefs.banner = s.showSessionBanner !== false;
  } catch { /* defaults */ }
  const cursorStyle = tp.cursorShape === "block" ? "block" : tp.cursorShape === "underline" ? "underline" : "bar";
  schemeSetting = tp.scheme;
  dark = schemeIsDark(schemeName(schemeSetting));
  baseFont = tp.fontSize || 13;
  term = new Terminal({
    // "" / "monospace" = the Rust default (Cascadia Mono); DejaVu Sans Mono covers missing symbols.
    fontFamily: `"${!tp.fontFamily || tp.fontFamily === "monospace" ? "Cascadia Mono" : tp.fontFamily}","DejaVu Sans Mono",monospace`,
    fontSize: (props.zoomKey && zooms()[props.zoomKey]) || baseFont,
    cursorBlink: tp.cursorBlink,
    cursorStyle,
    scrollback: tp.scrollbackLines || 10000,
    theme: xtermTheme(schemeName(schemeSetting)),
    allowProposedApi: true, // search-match decorations
  });
  fit = new FitAddon();
  term.loadAddon(fit);
  search = new SearchAddon();
  term.loadAddon(search);
  // App-level terminal keys are handled here and never reach the remote.
  term.attachCustomKeyEventHandler((e) => {
    if (e.type !== "keydown") return true;
    // Settings › Shortcuts: terminal commands run here; app commands bubble to the window (App.vue).
    const cmd = commandFor(e);
    if (cmd) {
      e.preventDefault();
      if (cmd === "terminal.find") void openFind();
      else if (cmd === "terminal.copy") copy();
      else if (cmd === "terminal.paste") void paste();
      else if (cmd === "terminal.save_text") void saveOutput();
      else if (cmd === "tab.reconnect" && stopped) void restart();
      return false; // never typed into the remote shell
    }
    // Built-in terminal clipboard keys (Rust: Shift+Insert pastes; Ctrl+Shift+C/V copy and paste).
    if (e.shiftKey && !e.ctrlKey && !e.altKey && e.key === "Insert") { e.preventDefault(); void paste(); return false; }
    if ((e.ctrlKey || e.metaKey) && e.shiftKey && !e.altKey && (e.code === "KeyC" || e.code === "KeyV")) {
      e.preventDefault();
      if (e.code === "KeyC") copy(); else void paste();
      return false;
    }
    if (!(e.ctrlKey || e.metaKey)) return true;
    if (e.shiftKey || e.altKey) return true;
    if (e.key === "=" || e.key === "+") { e.preventDefault(); zoom(1); return false; }
    if (e.key === "-") { e.preventDefault(); zoom(-1); return false; }
    if (e.key === "0") { e.preventDefault(); zoom(0); return false; }
    return true;
  });
  term.open(el.value!);
  el.value!.style.background = term.options.theme?.background ?? "";
  staleTimer = setInterval(() => (now.value = Date.now()), 1000);
  term.onSelectionChange(() => { if (prefs.copyOnSelect && term.hasSelection()) copy(); });
  // OSC 7 (file://host/path): the shell's folder, for the SFTP browser's "Follow terminal folder".
  term.parser.registerOscHandler(7, (data) => {
    try {
      const p = decodeURIComponent(new URL(data).pathname);
      if (p) emit("cwd", p);
    } catch { /* not a file:// URL */ }
    return true;
  });
  // Ctrl+wheel zoom; touchpads send many small deltas, so one step per ~100 px of accumulated scroll.
  let wheelAcc = 0;
  el.value!.addEventListener("wheel", (e) => {
    if (!e.ctrlKey) return;
    e.preventDefault();
    e.stopPropagation();
    wheelAcc += e.deltaMode === 0 ? e.deltaY : e.deltaY * 100;
    if (Math.abs(wheelAcc) < 50) return;
    zoom(wheelAcc < 0 ? 1 : -1);
    wheelAcc = 0;
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
  emit("backend", tabId);
  wireEvents();
  await attach();
  startMon();
  // Keystrokes go out one call at a time, in order: concurrent Send calls are served by separate
  // goroutines and fast typing could arrive swapped ("conpty" → "cnopty"); input typed while a call is
  // in flight is batched into the next one.
  let pending = "";
  let sending = false;
  const pump = async () => {
    sending = true;
    while (pending) {
      const chunk = pending;
      pending = "";
      try { await Send(tabId, chunk); } catch { /* the tab has closed: the close event reports it */ }
    }
    sending = false;
  };
  const send = (d: string) => { pending += d; if (!sending) void pump(); };
  term.onData((d) => {
    if (stopped) {
      if (d === "\r" || d === "\x04") emit("close"); // Return or Ctrl+D closes the tab
      else if (d === "r" || d === "R") void restart();
      else if (d === "s" || d === "S") void saveOutput();
      return;
    }
    send(d);
    if (d.includes("\r")) {
      clearTimeout(enterTimer);
      enterTimer = setTimeout(() => emit("enter"), 500); // let a `cd` finish first
    }
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
  clearTimeout(retryTimer);
  ro?.disconnect();
  clearInterval(staleTimer);
  window.removeEventListener("rxt-settings", onSettings);
  osScheme.removeEventListener("change", applyScheme);
  clearTimeout(enterTimer);
  offOut?.();
  offClose?.();
  offMon?.();
  if (tabId && props.monitor) void MonitorStop(tabId);
  if (tabId) void Disconnect(tabId);
  term?.dispose();
});
</script>

<template>
  <div class="term-wrap" @contextmenu="onContext">
    <div v-if="cmenu" class="cm-mask" @mousedown.self="cmenu = null" @contextmenu.prevent.stop="cmenu = null" @keydown.esc="cmenu = null">
      <div class="cm" :style="{ left: cmenu.x + 'px', top: cmenu.y + 'px' }">
        <button class="cm-i" :disabled="!cmenu.sel" @click="menuRun(copy)"><img :src="mi('copy')" />Copy<span>Ctrl+Shift+C</span></button>
        <button class="cm-i" :disabled="!cmenu.live" @click="menuRun(paste)"><img :src="mi('paste')" />Paste<span>Ctrl+Shift+V</span></button>
        <button class="cm-i" @click="menuRun(() => term.selectAll())"><img :src="mi('selection')" />Select all</button>
        <button class="cm-i" @click="menuRun(openFind)"><img :src="mi('search')" />Find<span>Ctrl+Shift+F</span></button>
        <button class="cm-i" @click="menuRun(saveOutput)"><img :src="mi('save')" />Save terminal text</button>
        <div class="cm-sep"></div>
        <button class="cm-i" @click="menuRun(() => term.clear())"><img :src="mi('remove')" />Clear scrollback</button>
        <button class="cm-i" @click="menuRun(() => term.reset())"><img :src="mi('refresh')" />Reset terminal</button>
        <div class="cm-sep"></div>
        <button class="cm-i" @click="menuRun(() => emit('duplicate'))"><img :src="mi('copy')" />Duplicate tab</button>
        <button class="cm-i" :disabled="cmenu.live" @click="menuRun(restart)"><img :src="mi('restart')" />Reconnect</button>
        <button class="cm-i" @click="menuRun(() => emit('close'))"><img :src="mi('close')" />Close tab</button>
      </div>
    </div>
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
    <div v-if="monitor && metrics" class="mon" :class="stale() ? 'stale' : ''">
      <div class="mon-chips">
        <span class="seg" :title="'Host: ' + (metrics.host || '') + (metrics.os ? '\nOS: ' + metrics.os : '')"><img :src="mi('server-tower')" class="mi" />{{ metrics.host || "—" }}</span>
        <span class="seg" :class="metrics.cpu >= 0 ? lvl(metrics.cpu) : ''" :title="metrics.cpu >= 0 ? `CPU usage ${metrics.cpu.toFixed(0)} %` : 'CPU usage (needs two samples)'">
          <img :src="mi('chip')" class="mi" />{{ metrics.cpu >= 0 ? metrics.cpu.toFixed(0) + " %" : "…" }}
          <svg class="spark" :width="SPARK_W" :height="SPARK_H"><path v-if="cpuHistory.length > 1" :d="sparkPath(cpuHistory)" /></svg>
        </span>
        <span v-if="metrics.memTotal" class="seg" :class="lvl((metrics.memUsed / metrics.memTotal) * 100)" :title="`Memory: ${metrics.memUsed} of ${metrics.memTotal} bytes used`"><img :src="mi('ram')" class="mi" />{{ fmtMem(metrics.memUsed) }} / {{ fmtMem(metrics.memTotal) }}</span>
        <span class="seg" title="Upload"><img :src="mi('upload')" class="mi" />{{ fmtRate(metrics.netUp) }}</span>
        <span class="seg" title="Download"><img :src="mi('download')" class="mi" />{{ fmtRate(metrics.netDown) }}</span>
        <span v-if="metrics.uptime" class="seg" :title="'Up for ' + fmtUptime(metrics.uptime)"><img :src="mi('monitor-clock')" class="mi" />{{ fmtUptime(metrics.uptime) }}</span>
        <span v-for="d in metrics.disks || []" :key="d.mount" class="seg" :class="lvl(d.usedPct)" :title="`${d.mount}: ${fmtMem(d.usedKiB * 1024)} of ${fmtMem(d.sizeKiB * 1024)} used`"><img :src="mi('hard-disk')" class="mi" />{{ d.mount }}: {{ d.usedPct }} %</span>
      </div>
      <button class="mon-close" title="Close remote monitoring" @click="emit('monitor-off')"><img :src="mi('close')" /></button>
    </div>
  </div>
</template>

<style scoped>
.term-wrap { position: relative; display: flex; flex-direction: column; height: 100%; width: 100%; }
.findbar { position: absolute; top: 6px; right: 18px; z-index: 5; display: flex; gap: 2px; padding: 3px; background: var(--panel); border: 1px solid var(--border); }
.find-in { width: 220px; height: 22px; padding: 0 6px; font-size: 12px; color: var(--text); background: var(--input); border: 1px solid var(--border); outline: none; }
.find-in:focus { border-color: var(--focus); }
.find-b { width: 22px; height: 22px; font-size: 11px; color: var(--text); background: var(--panel); border: 1px solid var(--border); cursor: pointer; }
.find-b:hover { background: var(--hover); }
.term-area { flex: 1; min-height: 0; padding: 2px 4px; } /* background = the scheme's (set in code) */
.cm-mask { position: fixed; inset: 0; z-index: 80; }
.cm { position: fixed; min-width: 210px; padding: 2px; background: var(--panel); border: 1px solid var(--border); font-family: "Noto Sans", sans-serif; }
.cm-i { display: flex; align-items: center; gap: 6px; width: 100%; height: 22px; padding: 0 6px; font-size: 12px; color: var(--text); background: transparent; border: none; text-align: left; cursor: pointer; }
.cm-i img { width: 16px; height: 16px; }
.cm-i span { margin-left: auto; padding-left: 16px; color: var(--text-muted); }
.cm-i:hover:not(:disabled) { background: var(--hover); }
.cm-i:disabled { color: var(--text-disabled); cursor: default; }
.cm-i:disabled img { opacity: .45; }
.cm-sep { height: 1px; margin: 3px 0; background: var(--border-subtle); }
/* Remote monitoring bar (Rust monitor_bar.rs): 26 px, panel fill with a top rule, chips with a
   16 px icon and 11 px text divided by thin rules, a 48x14 CPU sparkline and a close button. */
.mon { display: flex; align-items: center; height: 26px; flex: none; background: var(--panel); border-top: 1px solid var(--border); color: var(--text); font-family: "Noto Sans", sans-serif; font-size: 11px; white-space: nowrap; }
.mon-chips { flex: 1; min-width: 0; display: flex; align-items: center; overflow-x: auto; scrollbar-width: none; padding-left: 2px; }
.seg { display: inline-flex; align-items: center; gap: 4px; padding: 0 6px; height: 23px; flex: none; position: relative; margin-right: 3px; }
.seg + .seg::before { content: ""; position: absolute; left: -2px; top: 3px; bottom: 2px; border-left: 1px solid var(--border-subtle); }
.mi { width: 16px; height: 16px; flex: none; }
.seg.warn { color: var(--warning); }
.seg.danger { color: var(--danger); }
.mon.stale .seg { color: var(--text-muted); }
.spark { flex: none; background: var(--surface); outline: 1px solid var(--border-subtle); outline-offset: -1px; }
.spark path { fill: none; stroke: var(--accent); stroke-width: 1; }
.mon.stale .spark path { stroke: var(--text-muted); }
.mon-close { width: 24px; height: 24px; flex: none; margin-right: 1px; display: flex; align-items: center; justify-content: center; background: transparent; border: none; cursor: pointer; }
.mon-close:hover { background: var(--hover); }
.mon-close img { width: 16px; height: 16px; }
</style>

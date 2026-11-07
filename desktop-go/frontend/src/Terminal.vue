<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount } from "vue";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { Events } from "@wailsio/runtime";
import "@xterm/xterm/css/xterm.css";
import { Send, Resize, Disconnect } from "./api";

// connect returns the tabId once the terminal is open; the parent decides how (session/quick/demo).
const props = defineProps<{ connect: () => Promise<string> }>();
const emit = defineEmits<{ close: [] }>();

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
    term.writeln("\r\n\x1b[31mSession stopped\x1b[0m" + (why ? " \x1b[90m(" + why + ")\x1b[0m" : ""));
    term.writeln("\x1b[90m  - Press \x1b[0mReturn\x1b[90m to close this tab\x1b[0m");
    term.writeln("\x1b[90m  - Press \x1b[0mR\x1b[90m to restart the session\x1b[0m");
  });
}

async function restart() {
  offOut?.();
  offClose?.();
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
  void Resize(tabId, term.cols, term.rows);
  term.focus();
}

function b64(b64: string): Uint8Array {
  const bin = atob(b64);
  const u = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) u[i] = bin.charCodeAt(i);
  return u;
}

onMounted(async () => {
  term = new Terminal({
    fontFamily: '"Cascadia Mono","DejaVu Sans Mono",monospace',
    fontSize: 13,
    cursorBlink: true,
    theme: { background: "#0b0e11", foreground: "#d4d4d4", cursor: "#9ece6a", selectionBackground: "#2a3f52" },
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
  if (tabId) void Disconnect(tabId);
  term?.dispose();
});
</script>

<template>
  <div ref="el" class="h-full w-full"></div>
</template>

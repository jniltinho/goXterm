// Command ids, labels and keyboard shortcuts — the Rust app's (crates/core/src/command.rs CommandId,
// crates/ui/src/settings.rs default_shortcuts/shortcut_allowed), so config.toml's [shortcuts] table
// means the same in both apps. Only Ctrl+Shift+key, Ctrl+Tab and bare F11/F12 are allowed: anything
// else would steal terminal input.
import { shallowRef } from "vue";

export const COMMANDS: [id: string, label: string][] = [
  ["session.new", "New session"], ["session.quick_connect", "Quick connect"], ["tab.close", "Close tab"],
  ["tab.close_others", "Close other tabs"], ["tab.next", "Next tab"], ["tab.prev", "Previous tab"],
  ["tab.rename", "Rename tab"], ["tab.duplicate", "Duplicate tab"], ["tab.reconnect", "Reconnect"],
  ["terminal.find", "Find in terminal"], ["terminal.copy", "Copy"], ["terminal.paste", "Paste"],
  ["terminal.save_text", "Save terminal text"], ["app.settings", "Settings"], ["app.toggle_sidebar", "Toggle sidebar"],
  ["app.toggle_fullscreen", "Toggle fullscreen"], ["multiexec.toggle", "MultiExec"], ["tunnels.open", "Tunnels"],
  ["app.help", "Help"], ["app.exit", "Exit"], ["session.import", "Import sessions"], ["session.export", "Export sessions"],
  ["app.about", "About goXterm"], ["vault.change_password", "Change master password"],
  ["view.theme_light", "Light theme"], ["view.theme_dark", "Dark theme"], ["view.theme_system", "System theme"],
  ["app.focus_menu", "Focus menu bar"], ["serial.send_break", "Serial: send break"], ["serial.set_lines", "Serial: toggle DTR"],
  ["serial.list_ports", "Serial: list ports"], ["wsl.list_distributions", "WSL: list distributions"],
  ["tab.monitoring.toggle", "Command"], ["settings.monitoring.interval", "Command"], ["session.find", "Find session"],
  ["tab.new_local", "New local terminal"],
];

export const DEFAULT_SHORTCUTS: Record<string, string> = {
  "session.new": "Ctrl+Shift+N", "tab.new_local": "Ctrl+Shift+T", "session.quick_connect": "Ctrl+Shift+K",
  "tab.close": "Ctrl+Shift+W", "tab.next": "Ctrl+Tab", "tab.prev": "Ctrl+Shift+Tab", "tab.duplicate": "Ctrl+Shift+D",
  "tab.reconnect": "Ctrl+Shift+R", "terminal.find": "Ctrl+Shift+F", "app.settings": "Ctrl+Shift+Comma",
  "app.toggle_sidebar": "Ctrl+Shift+B", "app.toggle_fullscreen": "F11", "multiexec.toggle": "Ctrl+Shift+M",
  "tunnels.open": "Ctrl+Shift+U", "app.focus_menu": "Ctrl+Shift+F10", "session.find": "Ctrl+Shift+S",
};

interface Shortcut { ctrl: boolean; shift: boolean; alt: boolean; key: string }

export function parseShortcut(text: string): Shortcut | null {
  const s: Shortcut = { ctrl: false, shift: false, alt: false, key: "" };
  for (const raw of text.split("+").map((p) => p.trim())) {
    const p = raw.toLowerCase();
    if (p === "ctrl" || p === "control") s.ctrl = true;
    else if (p === "shift") s.shift = true;
    else if (p === "alt") s.alt = true;
    else if (!s.key && raw) s.key = raw.length === 1 ? raw.toUpperCase() : raw[0].toUpperCase() + raw.slice(1);
    else return null;
  }
  return s.key ? s : null;
}

/** Rust shortcut_allowed: Ctrl+Shift+key, Ctrl+Tab or bare F11/F12. */
export function shortcutAllowed(s: Shortcut): boolean {
  if (s.alt) return false;
  if (s.ctrl && s.shift) return true;
  if (s.ctrl) return s.key === "Tab";
  if (s.shift) return false;
  return s.key === "F11" || s.key === "F12";
}

/** Text is empty, or a parseable, allowed shortcut. */
export const shortcutValid = (text: string) => !text.trim() || (() => { const s = parseShortcut(text); return !!s && shortcutAllowed(s); })();

/** Key name of an event in the shortcut vocabulary (layout-independent for letters and digits). */
function eventKey(e: KeyboardEvent): string {
  if (/^Key[A-Z]$/.test(e.code)) return e.code.slice(3);
  if (/^Digit\d$/.test(e.code)) return e.code.slice(5);
  const c = e.code === "Equal" ? "Equals" : e.code; // egui's name
  if (/^(F\d{1,2}|Tab|Comma|Period|Slash|Semicolon|Minus|Equals|Space|Enter|Escape|Backspace|Delete|Insert|Home|End|PageUp|PageDown|Arrow(Up|Down|Left|Right))$/.test(c)) return c;
  return e.key.length === 1 ? e.key.toUpperCase() : e.key;
}

/** Shortcuts in effect: the defaults overridden by Settings › Shortcuts. */
export const shortcutMap = shallowRef<Record<string, string>>({ ...DEFAULT_SHORTCUTS });

/** The command bound to a key event, if any. */
export function commandFor(e: KeyboardEvent): string | null {
  if (e.type !== "keydown") return null;
  const key = eventKey(e);
  for (const [id, text] of Object.entries(shortcutMap.value)) {
    const s = text && parseShortcut(text);
    if (s && shortcutAllowed(s) && s.ctrl === (e.ctrlKey || e.metaKey) && s.shift === e.shiftKey && s.alt === e.altKey && s.key === key) return id;
  }
  return null;
}

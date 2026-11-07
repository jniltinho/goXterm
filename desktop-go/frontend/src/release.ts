// Keyboard release for remote desktops (Settings › General › Keyboard release keys, Rust
// desktop_release): pressing the combination returns focus to the app, and keys stop going to the
// remote side. "Ctrl+Alt" fires when the last of its modifiers goes down; "Ctrl+Alt+F12" on F12.
import { GetSettings } from "./api";

type Combo = { ctrl: boolean; alt: boolean; shift: boolean; meta: boolean; key: string };

export function parseRelease(spec: string): Combo | null {
  const c: Combo = { ctrl: false, alt: false, shift: false, meta: false, key: "" };
  for (const raw of spec.split("+").map((p) => p.trim().toLowerCase()).filter(Boolean)) {
    if (raw === "ctrl" || raw === "control") c.ctrl = true;
    else if (raw === "alt") c.alt = true;
    else if (raw === "shift") c.shift = true;
    else if (raw === "super" || raw === "meta") c.meta = true;
    else if (c.key) return null;
    else c.key = raw;
  }
  return c.ctrl || c.alt || c.shift || c.meta ? c : null;
}

const MODIFIER_KEYS: Record<string, keyof Omit<Combo, "key">> = { control: "ctrl", alt: "alt", shift: "shift", meta: "meta" };

export function isRelease(e: Pick<KeyboardEvent, "key" | "ctrlKey" | "altKey" | "shiftKey" | "metaKey">, c: Combo | null): boolean {
  if (!c) return false;
  if (e.ctrlKey !== c.ctrl || e.altKey !== c.alt || e.shiftKey !== c.shift || e.metaKey !== c.meta) return false;
  const k = e.key.toLowerCase();
  return c.key ? k === c.key : MODIFIER_KEYS[k] !== undefined; // modifier-only: the last modifier pressed
}

let cached: Combo | null = parseRelease("Ctrl+Alt");
/** The configured combination (refreshed on each call; the default until Settings answer). */
export async function releaseCombo(): Promise<Combo | null> {
  try { cached = parseRelease((await GetSettings()).desktopRelease || "Ctrl+Alt") ?? cached; } catch { /* keep */ }
  return cached;
}

// Self-check of the parser and matcher (runs once at import; a broken rule fails loudly in dev).
{
  const ca = parseRelease("Ctrl+Alt");
  const ev = (key: string, ctrl: boolean, alt: boolean) => ({ key, ctrlKey: ctrl, altKey: alt, shiftKey: false, metaKey: false });
  console.assert(isRelease(ev("Alt", true, true), ca) && !isRelease(ev("a", true, true), ca) && !isRelease(ev("Alt", false, true), ca), "release: Ctrl+Alt");
  console.assert(isRelease(ev("F12", true, true), parseRelease("ctrl+alt+f12")) && parseRelease("F12") === null, "release: key combos");
}

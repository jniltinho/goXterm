// Session info box drawn into a terminal when it connects — a port of the Rust app's banner.rs
// render(): an ASCII box (`+`, `-`, `|`) with the title in the top border and a hint in the bottom
// border, coloured by role. The backend supplies the facts (api.ts Banner); this only lays them out.
import type { Banner, BannerLine } from "./api";

type Role = [number, number]; // SGR colour for (light scheme, dark scheme)
const BORDER: Role = [33, 93], TITLE: Role = [36, 96], TARGET: Role = [35, 95];
const YES: Role = [32, 92], NO: Role = [31, 91], MUTED: Role = [90, 90];
const INDENT = 4; // left margin of the box (MobaXterm-like), dropped on narrow terminals
const ELL = "…";

type Seg = [Role | null, string];
const len = (s: string) => [...s].length;

function segments(l: BannerLine): Seg[] {
  switch (l.kind) {
    case "head":
      return [[null, l.text], [TARGET, l.target ?? ""]];
    case "check": {
      const out: Seg[] = [[l.on ? YES : NO, l.on ? "✓" : "✗"], [null, " " + l.text]];
      if (l.note) out.push([MUTED, ` (${l.note})`]);
      return out;
    }
    default:
      return [[MUTED, l.text]];
  }
}

function fit(text: string, max: number): string {
  const c = [...text];
  if (c.length <= max) return text;
  return max <= 0 ? "" : c.slice(0, max - 1).join("") + ELL;
}

/** The box as terminal text (CRLF line ends, ending with a blank line), at most `cols` wide. */
export function renderBanner(b: Banner, cols: number, dark: boolean): string {
  if (cols < 6) return "";
  const sgr = (r: Role) => `\x1b[${dark ? r[1] : r[0]}m`;
  const border = sgr(BORDER), reset = "\x1b[0m";
  const lines = b.lines.map(segments);
  const width = (l: Seg[]) => l.reduce((n, [, t]) => n + len(t), 0);
  const content = Math.max(len(b.title) + 2, ...lines.map(width));
  const pad = cols >= 40 ? " ".repeat(INDENT) : "";
  const inner = Math.max(1, Math.min(content, cols - 4 - pad.length));

  let out = "";
  const rule = (left: string, right: string, label: string, role: Role) => {
    label = fit(label, inner - 2);
    out += pad + border + left + "-";
    let used = 1;
    if (label) {
      out += ` ${sgr(role)}\x1b[1m${label}${reset}${border} `;
      used += len(label) + 2;
    }
    out += "-".repeat(Math.max(0, inner + 2 - Math.min(used, inner + 2))) + right + reset + "\r\n";
  };

  rule("+", "+", b.title, TITLE);
  for (const l of lines) {
    out += `${pad}${border}|${reset} `;
    let left = inner;
    const full = width(l) <= inner;
    for (const [role, text] of l) {
      if (left === 0) break;
      // The segment that overflows ends with the ellipsis; later ones are dropped.
      const piece = full || len(text) < left ? text : [...text].slice(0, left - 1).join("") + ELL;
      left -= len(piece);
      out += role ? sgr(role) + piece + reset : piece;
    }
    out += " ".repeat(left) + ` ${border}|${reset}\r\n`;
  }
  // The hint is dropped rather than cut when it does not fit.
  rule("+", "+", len(b.hint) + 2 <= inner ? b.hint : "", MUTED);
  return out + "\r\n";
}

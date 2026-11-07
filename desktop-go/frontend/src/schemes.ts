// Terminal colour schemes: the Rust catalog (crates/core/src/theme.rs TerminalScheme::builtin), same
// names and colours, so config.toml's terminal.scheme means the same thing in both apps. An empty
// scheme follows the UI theme (rustxterm-light / rustxterm-dark), as the Rust app does.
import type { ITheme } from "@xterm/xterm";

type S = [fg: string, bg: string, cursor: string, sel: string, ansi: string[]];
const SOLARIZED = "073642 dc322f 859900 b58900 268bd2 d33682 2aa198 eee8d5 002b36 cb4b16 586e75 657b83 839496 6c71c4 93a1a1 fdf6e3";
const TANGO = "2e3436 cc0000 4e9a06 c4a000 3465a4 75507b 06989a d3d7cf 555753 ef2929 8ae234 fce94f 729fcf ad7fa8 34e2e2 eeeeec";
const s = (fg: string, bg: string, cur: string, sel: string, ansi: string): S =>
  ["#" + fg, "#" + bg, "#" + cur, "#" + sel, ansi.split(" ").map((c) => "#" + c)];

export const SCHEMES: Record<string, S> = {
  "rustxterm-light": s("1d2330", "ffffff", "1d2330", "cfe2fb", "000000 c62828 2e7d32 9a6700 1f6fd1 8e24aa 00838f bfbfbf 666666 e53935 43a047 c79100 4c9bf0 ab47bc 00acc1 ffffff"),
  "rustxterm-dark": s("dfe3ea", "14161a", "dfe3ea", "2f4d73", "1e2127 e06c75 98c379 d19a66 61afef c678dd 56b6c2 abb2bf 5c6370 e88388 a5d68a e5c07b 80c0ff d7a1e8 7fd3dc ffffff"),
  "Solarized Dark": s("839496", "002b36", "93a1a1", "073642", SOLARIZED),
  "Solarized Light": s("657b83", "fdf6e3", "586e75", "eee8d5", SOLARIZED),
  Dracula: s("f8f8f2", "282a36", "f8f8f2", "44475a", "21222c ff5555 50fa7b f1fa8c bd93f9 ff79c6 8be9fd f8f8f2 6272a4 ff6e6e 69ff94 ffffa5 d6acff ff92df a4ffff ffffff"),
  "Gruvbox Dark": s("ebdbb2", "282828", "ebdbb2", "504945", "282828 cc241d 98971a d79921 458588 b16286 689d6a a89984 928374 fb4934 b8bb26 fabd2f 83a598 d3869b 8ec07c ebdbb2"),
  "Gruvbox Light": s("3c3836", "fbf1c7", "3c3836", "d5c4a1", "fbf1c7 cc241d 98971a d79921 458588 b16286 689d6a 7c6f64 928374 9d0006 79740e b57614 076678 8f3f71 427b58 3c3836"),
  Nord: s("d8dee9", "2e3440", "d8dee9", "434c5e", "3b4252 bf616a a3be8c ebcb8b 81a1c1 b48ead 88c0d0 e5e9f0 4c566a bf616a a3be8c ebcb8b 81a1c1 b48ead 8fbcbb eceff4"),
  "One Dark": s("abb2bf", "282c34", "528bff", "3e4451", "282c34 e06c75 98c379 e5c07b 61afef c678dd 56b6c2 abb2bf 5c6370 e06c75 98c379 e5c07b 61afef c678dd 56b6c2 ffffff"),
  Monokai: s("f8f8f2", "272822", "f8f8f0", "49483e", "272822 f92672 a6e22e f4bf75 66d9ef ae81ff a1efe4 f8f8f2 75715e f92672 a6e22e f4bf75 66d9ef ae81ff a1efe4 f9f8f5"),
  "Tango Dark": s("d3d7cf", "2e3436", "d3d7cf", "555753", TANGO),
  "Tango Light": s("2e3436", "eeeeec", "2e3436", "babdb6", TANGO),
  xterm: s("e5e5e5", "000000", "e5e5e5", "4d4d4d", "000000 cd0000 00cd00 cdcd00 0000ee cd00cd 00cdcd e5e5e5 7f7f7f ff0000 00ff00 ffff00 5c5cff ff00ff 00ffff ffffff"),
};

/** Whether the UI currently shows the Dark skin (explicit, or System with a dark OS). */
export function uiDark(): boolean {
  const t = document.documentElement.dataset.theme;
  return t === "dark" || (t !== "light" && matchMedia("(prefers-color-scheme: dark)").matches);
}

/** The scheme name in effect: the configured one, or the UI theme's default when unset/unknown. */
export const schemeName = (name: string | undefined) =>
  name && SCHEMES[name] ? name : uiDark() ? "rustxterm-dark" : "rustxterm-light";

const KEYS = ["black", "red", "green", "yellow", "blue", "magenta", "cyan", "white"] as const;

/** xterm.js theme of a scheme. */
export function xtermTheme(name: string): ITheme {
  const [foreground, background, cursor, selectionBackground, ansi] = SCHEMES[name];
  const t: Record<string, string> = { foreground, background, cursor, cursorAccent: background, selectionBackground };
  KEYS.forEach((k, i) => {
    t[k] = ansi[i];
    t["bright" + k[0].toUpperCase() + k.slice(1)] = ansi[i + 8];
  });
  return t as ITheme;
}

/** Whether a scheme has a dark background (banner colours). */
export function schemeIsDark(name: string): boolean {
  const bg = SCHEMES[name][1];
  const [r, g, b] = [1, 3, 5].map((i) => parseInt(bg.slice(i, i + 2), 16));
  return 0.299 * r + 0.587 * g + 0.114 * b < 128;
}

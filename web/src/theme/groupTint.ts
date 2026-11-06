// SPDX-License-Identifier: AGPL-3.0-or-later

import { contrast } from "../lib/brand";
import { currentMark } from "./currentMark";
import type { Sidebar } from "./tokens";

// The group under a space's root (decision 93): a light block behind the folders under My files, and under a team space, that sets them
// apart from the sidebar. Its colour is the sidebar colour with an overlay laid over it, with the most overlay that keeps the rows' text,
// the current row's mark and the capped hint's caption at 4.5:1 or more, both at rest and on hover. The overlay is white when the
// sidebar's text is light, and black when it is dark (the tint and the hover invert together). The hover is the opposite overlay at
// GROUP_HOVER.share, drawn over the tint. The step is 1%, and the most is 14%.
export const GROUP_TINT = { max: 0.14, step: 0.01 } as const;
export const GROUP_HOVER = { share: 0.1 } as const;
const CONTRAST_MIN = 4.5;

interface Rgb {
  r: number;
  g: number;
  b: number;
}
const WHITE: Rgb = { r: 255, g: 255, b: 255 };
const BLACK: Rgb = { r: 0, g: 0, b: 0 };

export interface GroupColours {
  // tint is the group's background: a solid #rrggbb.
  tint: string;
  // hover is the hover of a row in the group, as CSS (an rgba drawn over the tint); null where no tint is drawn.
  hover: string | null;
}

function rgbOf(hex: string): Rgb {
  const n = parseInt(hex.slice(1), 16);
  return { r: (n >> 16) & 255, g: (n >> 8) & 255, b: n & 255 };
}

function hexOf(c: Rgb): string {
  const part = (v: number) => Math.round(v).toString(16).padStart(2, "0");
  return `#${part(c.r)}${part(c.g)}${part(c.b)}`.toUpperCase();
}

// laid draws an overlay of colour `top` at share `a` over `base`.
function laid(base: Rgb, top: Rgb, a: number): Rgb {
  return { r: top.r * a + base.r * (1 - a), g: top.g * a + base.g * (1 - a), b: top.b * a + base.b * (1 - a) };
}

function computeColours(side: Pick<Sidebar, "bg" | "text" | "active" | "caption">): GroupColours {
  const ink = currentMark(side).ink;
  // Light text: the tint is white and the hover black. Dark text: the tint is black and the hover white.
  const lightText = contrast(side.text, "#000000") >= contrast(side.text, "#FFFFFF");
  const tintOver = lightText ? WHITE : BLACK;
  const hoverOver = lightText ? BLACK : WHITE;
  const bg = rgbOf(side.bg);
  const steps = Math.round(GROUP_TINT.max / GROUP_TINT.step);
  // From the most overlay down: the first tint whose text, mark and caption read at rest and on its hover is the one.
  for (let k = steps; k >= 1; k--) {
    const tint = laid(bg, tintOver, k * GROUP_TINT.step);
    const shown = laid(tint, hoverOver, GROUP_HOVER.share);
    const tintHex = hexOf(tint);
    const shownHex = hexOf(shown);
    if (
      [side.text, ink, side.caption].every((c) => contrast(c, tintHex) >= CONTRAST_MIN && contrast(c, shownHex) >= CONTRAST_MIN)
    ) {
      const { r, g, b } = hoverOver;
      return { tint: tintHex, hover: `rgba(${r}, ${g}, ${b}, ${GROUP_HOVER.share})` };
    }
  }
  return { tint: side.bg, hover: null };
}

// memo keeps the colours of each sidebar the page has drawn: a brand's sidebar is the same for every render, and the loop above makes a
// few dozen contrast calls. The cache is cleared when it grows past the few brands a page can show.
const memo = new Map<string, GroupColours>();
const MEMO_MAX = 16;

// groupColours is the group's tint and hover for a sidebar (see GroupColours), memoised per sidebar colours.
export function groupColours(side: Pick<Sidebar, "bg" | "text" | "active" | "caption">): GroupColours {
  const key = [side.bg, side.text, side.active, side.caption].join("|");
  const hit = memo.get(key);
  if (hit) return hit;
  const colours = computeColours(side);
  if (memo.size >= MEMO_MAX) memo.clear();
  memo.set(key, colours);
  return colours;
}

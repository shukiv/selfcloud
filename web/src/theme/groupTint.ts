// SPDX-License-Identifier: AGPL-3.0-or-later

import { contrast } from "../lib/brand";
import { currentMark } from "./currentMark";
import type { Sidebar } from "./tokens";

// The group under a space's root (decision 93): a light block behind the folders under My files, and under a team space, that sets them
// apart from the sidebar. Its colour is the sidebar colour with an overlay laid over it, with the most overlay that keeps the rows' text
// and the current row's mark at 4.5:1 or more, both at rest and on hover. The overlay is white when the sidebar's text is light, and black
// when it is dark (the tint and the hover invert together). The hover is the opposite overlay at GROUP_HOVER.share, drawn over the tint.
// The step is 1%, and the most is 14%.
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

// groupColours is the group's tint (a solid #rrggbb) and its hover (a CSS rgba, drawn over the tint), for a sidebar. The hover is null
// where no tint is drawn: the rows then keep the sidebar's own hover.
export function groupColours(side: Pick<Sidebar, "bg" | "text" | "active">): { tint: string; hover: string | null } {
  const ink = currentMark(side).ink;
  // Light text: the tint is white and the hover black. Dark text: the tint is black and the hover white.
  const lightText = contrast(side.text, "#000000") >= contrast(side.text, "#FFFFFF");
  const tintOver = lightText ? WHITE : BLACK;
  const hoverOver = lightText ? BLACK : WHITE;
  const bg = rgbOf(side.bg);
  const steps = Math.round(GROUP_TINT.max / GROUP_TINT.step);
  // From the most overlay down: the first tint whose text and mark read at rest and on its hover is the one.
  for (let k = steps; k >= 1; k--) {
    const tint = laid(bg, tintOver, k * GROUP_TINT.step);
    const shown = laid(tint, hoverOver, GROUP_HOVER.share);
    const tintHex = hexOf(tint);
    const shownHex = hexOf(shown);
    if (
      contrast(side.text, tintHex) >= CONTRAST_MIN &&
      contrast(ink, tintHex) >= CONTRAST_MIN &&
      contrast(side.text, shownHex) >= CONTRAST_MIN &&
      contrast(ink, shownHex) >= CONTRAST_MIN
    ) {
      const { r, g, b } = hoverOver;
      return { tint: tintHex, hover: `rgba(${r}, ${g}, ${b}, ${GROUP_HOVER.share})` };
    }
  }
  return { tint: side.bg, hover: null };
}

// groupTint is the group's background for a sidebar (see groupColours).
export function groupTint(side: Pick<Sidebar, "bg" | "text" | "active">): string {
  return groupColours(side).tint;
}

// groupHover is the hover of a row in the group, as CSS, or null where no tint is drawn.
export function groupHover(side: Pick<Sidebar, "bg" | "text" | "active">): string | null {
  return groupColours(side).hover;
}

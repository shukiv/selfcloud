// SPDX-License-Identifier: AGPL-3.0-or-later

import { contrast } from "../lib/brand";
import { currentMark } from "./currentMark";
import type { Sidebar } from "./tokens";

// The tint of the sidebar's group under a space's root (decision 93): a light block behind the folders under My files, and under a
// team space, that sets them apart from the blue. It is white laid over the sidebar colour, with the most white that keeps the rows'
// text and the current row's mark at 4.5:1 or more; a brand whose blue or teal is too light for that gets less white, and one where
// no white reads gets no tint at all (the sidebar colour). The step is 1%, and the most is 14%.
export const GROUP_TINT = { max: 0.14, step: 0.01 } as const;
const CONTRAST_MIN = 4.5;

// mixWhite is the colour of white at a share (0 to 1) laid over a #rrggbb colour, as #RRGGBB.
function mixWhite(hex: string, share: number): string {
  const n = parseInt(hex.slice(1), 16);
  const mix = (c: number) => Math.round(255 * share + c * (1 - share));
  const part = (c: number) => mix(c).toString(16).padStart(2, "0");
  return `#${part((n >> 16) & 255)}${part((n >> 8) & 255)}${part(n & 255)}`.toUpperCase();
}

// groupTint is the group's background for a sidebar: a solid colour, so the CSS reads it as the one token.
export function groupTint(side: Pick<Sidebar, "bg" | "text" | "active">): string {
  const ink = currentMark(side).ink;
  const steps = Math.round(GROUP_TINT.max / GROUP_TINT.step);
  // From the most white down: the first tint that keeps both the text and the mark readable is the one.
  for (let k = steps; k >= 1; k--) {
    const tint = mixWhite(side.bg, k * GROUP_TINT.step);
    if (contrast(side.text, tint) >= CONTRAST_MIN && contrast(ink, tint) >= CONTRAST_MIN) return tint;
  }
  return side.bg;
}

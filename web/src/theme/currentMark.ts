// SPDX-License-Identifier: AGPL-3.0-or-later

import { contrast } from "../lib/brand";
import type { Sidebar } from "./tokens";

// The sidebar's mark colours (decision 69, the owner's option B). The current place itself is now filled inline with the active
// colour at every depth (decisions 72 and 239), so the folder tree and the menu no longer read a bar from here. What remains serves
// the group tint's row text (ink, theme/groupTint.ts) and the storage block's focus ring (bar, --sc-side-mark in index.css).
//
// The colours follow WCAG: the text needs 4.5:1 and the bar 3:1 against the sidebar. The text is also checked on the hover tint
// (SIDEBAR.hover, white at 10%, drawn over the sidebar). The text takes the sidebar's `active` colour when that reads on both;
// otherwise the sidebar's text colour when that does; otherwise the colour (white or black) that reads best on the weaker of the
// sidebar and its tint. The bar is `active` when it reaches 3:1 on the sidebar, else the text.
export const MARK_TEXT_MIN = 4.5;
export const MARK_BAR_MIN = 3;
// The hover tint of a row: SIDEBAR.hover (white at 10%).
const HOVER_WHITE = 0.1;

export interface CurrentMark {
  // ink is the text colour that reads on the sidebar and its hover tint (the group tint's rows).
  ink: string;
  // bar is the colour that reads at 3:1 on the sidebar (the storage block's focus ring).
  bar: string;
}

// hoverOver is the colour of the hover tint drawn over a sidebar colour (#rrggbb).
export function hoverOver(bg: string): string {
  const n = parseInt(bg.slice(1), 16);
  const mix = (c: number) => Math.round(255 * HOVER_WHITE + c * (1 - HOVER_WHITE));
  const hex = (c: number) => c.toString(16).padStart(2, "0");
  return `#${hex(mix((n >> 16) & 255))}${hex(mix((n >> 8) & 255))}${hex(mix(n & 255))}`.toUpperCase();
}

export function currentMark(side: Pick<Sidebar, "bg" | "text" | "active">): CurrentMark {
  const hover = hoverOver(side.bg);
  const reads = (c: string) => contrast(c, side.bg) >= MARK_TEXT_MIN && contrast(c, hover) >= MARK_TEXT_MIN;
  // Where no colour reads on both, the one that reads best on the weaker of the two (the sidebar or its tint).
  const weakest = (c: string) => Math.min(contrast(c, side.bg), contrast(c, hover));
  const ink = [side.active, side.text].find(reads) ?? [side.text, "#FFFFFF", "#000000"].reduce((best, c) => (weakest(c) > weakest(best) ? c : best));
  return {
    ink,
    bar: contrast(side.active, side.bg) >= MARK_BAR_MIN ? side.active : side.text,
  };
}

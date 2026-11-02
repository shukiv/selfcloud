// SPDX-License-Identifier: AGPL-3.0-or-later

import { contrast } from "../lib/brand";
import type { Sidebar } from "./tokens";

// The current place in the sidebar (decision 69, the owner's option B): no background on the row, a 3 px bar at its start edge, and
// the row's text and icon in the same colour. This is the one definition the folder tree, the menu (its theme and its stylesheet)
// and the folded rail read, so they look alike.
//
// The colours follow WCAG: the text needs 4.5:1 and the bar 3:1 against the sidebar. The text is also checked on the hover tint
// (SIDEBAR.hover, white at 10%, drawn over the sidebar), because the current row keeps its mark colour when it is hovered. The text
// takes the sidebar's `active` colour when that reads on both; otherwise the sidebar's text colour when that does; otherwise the
// colour (white or black) that reads best on the weaker of the sidebar and its tint. The bar is `active` when it reaches 3:1 on the
// sidebar, else the text.
export const MARK_TEXT_MIN = 4.5;
export const MARK_BAR_MIN = 3;
// The hover tint of a row: SIDEBAR.hover (white at 10%).
const HOVER_WHITE = 0.1;

export interface CurrentMark {
  // ink is the current row's text and icon.
  ink: string;
  // bar is the 3 px bar at the start edge.
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

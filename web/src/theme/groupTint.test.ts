// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { contrast } from "../lib/brand";
import { currentMark } from "./currentMark";
import { GROUP_HOVER, GROUP_TINT, groupHover, groupTint } from "./groupTint";
import { SIDEBAR } from "./tokens";

// Decision 93: the group under a space's root is drawn in a light tint of the sidebar. Its colour is the sidebar colour with the most
// white that keeps the rows' text, and the current row's mark, at 4.5:1 or more; the tint is never stronger than GROUP_TINT.max.

const TEAL = "#0F766E";

function white(hex: string) {
  return { r: parseInt(hex.slice(1, 3), 16), g: parseInt(hex.slice(3, 5), 16), b: parseInt(hex.slice(5, 7), 16) };
}

describe("the sidebar's group tint (decision 93)", () => {
  it("is white at 14% over the default blue, and the text reads 4.5:1 or more on it", () => {
    const tint = groupTint(SIDEBAR);
    expect(tint).toBe("#28589E");
    expect(contrast(SIDEBAR.text, tint)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(SIDEBAR).ink, tint)).toBeGreaterThanOrEqual(4.5);
  });

  it("is lighter than the sidebar it sits on", () => {
    const tint = groupTint(SIDEBAR);
    const bg = white(SIDEBAR.bg);
    const t = white(tint);
    expect(t.r + t.g + t.b).toBeGreaterThan(bg.r + bg.g + bg.b);
  });

  it("on the e2e teal, takes the most white that keeps the text and the mark at 4.5:1, and no more than the maximum", () => {
    const side = { ...SIDEBAR, bg: TEAL, text: "#FFFFFF" };
    const tint = groupTint(side);
    expect(tint).not.toBe(TEAL);
    expect(contrast(side.text, tint)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(side).ink, tint)).toBeGreaterThanOrEqual(4.5);
    // One step more white than the result would fail the text or the mark: the search stops at the first failure from the top.
    const t = white(tint);
    const b = white(TEAL);
    const used = (t.r - b.r) / (255 - b.r);
    expect(used).toBeLessThanOrEqual(GROUP_TINT.max + 0.005);
    expect(used).toBeGreaterThan(0);
  });

  it("is the sidebar colour itself where no tint keeps the text readable", () => {
    const grey = "#777777";
    expect(groupTint({ bg: grey, text: grey, active: grey })).toBe(grey);
  });
});

// Round 2 (decision 93): a row inside the tinted group keeps its text and its mark at 4.5:1 on hover too. The hover is an overlay drawn
// over the tint, in the opposite direction to the tint: black for a sidebar with light text, white for dark text.

// over draws an rgba overlay (as the CSS string groupHover returns) on a #rrggbb colour, and returns the colour the row shows, as #rrggbb.
function over(hex: string, rgba: string): string {
  const base = white(hex);
  const m = rgba.match(/[\d.]+/g)?.map(Number) ?? [0, 0, 0, 1];
  const a = m[3] ?? 1;
  const part = (c: number, o: number) => Math.round(o * a + c * (1 - a)).toString(16).padStart(2, "0");
  return `#${part(base.r, m[0] ?? 0)}${part(base.g, m[1] ?? 0)}${part(base.b, m[2] ?? 0)}`.toUpperCase();
}

describe("the hover inside the sidebar's tinted group (decision 93, round 2)", () => {
  it("is black at the hover share over the default blue's tint, and the text and the mark read 4.5:1 or more on it", () => {
    const hover = groupHover(SIDEBAR);
    expect(hover).toBe(`rgba(0, 0, 0, ${GROUP_HOVER.share})`);
    const shown = over(groupTint(SIDEBAR), hover ?? "");
    expect(contrast(SIDEBAR.text, shown)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(SIDEBAR).ink, shown)).toBeGreaterThanOrEqual(4.5);
  });

  it("is black at the hover share over the e2e teal's tint, and the text and the mark read 4.5:1 or more on it", () => {
    const side = { ...SIDEBAR, bg: TEAL, text: "#FFFFFF" };
    const hover = groupHover(side);
    expect(hover).toBe(`rgba(0, 0, 0, ${GROUP_HOVER.share})`);
    const shown = over(groupTint(side), hover ?? "");
    expect(contrast(side.text, shown)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(side).ink, shown)).toBeGreaterThanOrEqual(4.5);
  });

  it("inverts with a dark sidebar text: the tint is darker and the hover lighter, and both read 4.5:1 or more", () => {
    const side = { bg: "#FECC00", text: "#0D2142", active: "#0D2142" };
    const tint = groupTint(side);
    const hover = groupHover(side);
    expect(hover).toBe(`rgba(255, 255, 255, ${GROUP_HOVER.share})`);
    expect(white(tint).r + white(tint).g + white(tint).b).toBeLessThan(white(side.bg).r + white(side.bg).g + white(side.bg).b);
    const shown = over(tint, hover ?? "");
    expect(contrast(side.text, tint)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(side.text, shown)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(side).ink, shown)).toBeGreaterThanOrEqual(4.5);
  });

  it("is no hover at all where no tint is drawn (the sidebar's own hover then applies)", () => {
    const grey = "#777777";
    expect(groupHover({ bg: grey, text: grey, active: grey })).toBeNull();
  });
});


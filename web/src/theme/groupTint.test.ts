// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { contrast } from "../lib/brand";
import { currentMark } from "./currentMark";
import { GROUP_HOVER, GROUP_TINT, groupColours } from "./groupTint";
import { SIDEBAR } from "./tokens";

// Decision 93: the group under a space's root is drawn with a tint of the sidebar, and a row inside it takes a hover over the tint. The
// rows' text, the current row's mark and the capped hint's caption read 4.5:1 or more on the tint and on the hover, at rest and in
// every state the group draws. The tint is the most overlay that keeps them so, at most GROUP_TINT.max.

const TEAL = "#0F766E";

function white(hex: string) {
  return { r: parseInt(hex.slice(1, 3), 16), g: parseInt(hex.slice(3, 5), 16), b: parseInt(hex.slice(5, 7), 16) };
}

// over draws an rgba overlay (the CSS string the group's hover is) on a #rrggbb colour, and returns the colour the row shows, as #rrggbb.
function over(hex: string, rgba: string): string {
  const base = white(hex);
  const m = rgba.match(/[\d.]+/g)?.map(Number) ?? [0, 0, 0, 1];
  const a = m[3] ?? 1;
  const part = (c: number, o: number) => Math.round(o * a + c * (1 - a)).toString(16).padStart(2, "0");
  return `#${part(base.r, m[0] ?? 0)}${part(base.g, m[1] ?? 0)}${part(base.b, m[2] ?? 0)}`.toUpperCase();
}

describe("the sidebar's group tint (decision 93)", () => {
  it("on the default blue, the tint is at most GROUP_TINT.max, and the text and the mark read 4.5:1 or more on it", () => {
    const { tint } = groupColours(SIDEBAR);
    expect(tint).not.toBe(SIDEBAR.bg);
    expect(contrast(SIDEBAR.text, tint)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(SIDEBAR).ink, tint)).toBeGreaterThanOrEqual(4.5);
    const t = white(tint);
    const b = white(SIDEBAR.bg);
    expect((t.r - b.r) / (255 - b.r)).toBeLessThanOrEqual(GROUP_TINT.max + 0.005);
  });

  it("is lighter than the sidebar it sits on, with light text", () => {
    const { tint } = groupColours(SIDEBAR);
    const bg = white(SIDEBAR.bg);
    const t = white(tint);
    expect(t.r + t.g + t.b).toBeGreaterThan(bg.r + bg.g + bg.b);
  });

  it("on the e2e teal, the most white that keeps the text and the mark at 4.5:1", () => {
    const side = { ...SIDEBAR, bg: TEAL, text: "#FFFFFF", caption: "#FFFFFF" };
    const { tint } = groupColours(side);
    expect(tint).not.toBe(TEAL);
    expect(contrast(side.text, tint)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(side).ink, tint)).toBeGreaterThanOrEqual(4.5);
    const t = white(tint);
    const b = white(TEAL);
    const used = (t.r - b.r) / (255 - b.r);
    expect(used).toBeLessThanOrEqual(GROUP_TINT.max + 0.005);
    expect(used).toBeGreaterThan(0);
  });

  it("is the sidebar colour itself, with no hover, where no tint keeps the text readable", () => {
    const grey = "#777777";
    const { tint, hover } = groupColours({ bg: grey, text: grey, active: grey, caption: grey });
    expect(tint).toBe(grey);
    expect(hover).toBeNull();
  });
});

describe("the hover inside the sidebar's tinted group (decision 93)", () => {
  it("is black at the hover share over the default blue's tint, and the text and the mark read 4.5:1 or more on it", () => {
    const { tint, hover } = groupColours(SIDEBAR);
    expect(hover).toBe(`rgba(0, 0, 0, ${GROUP_HOVER.share})`);
    const shown = over(tint, hover ?? "");
    expect(contrast(SIDEBAR.text, shown)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(SIDEBAR).ink, shown)).toBeGreaterThanOrEqual(4.5);
  });

  it("is black at the hover share over the e2e teal's tint, and the text and the mark read 4.5:1 or more on it", () => {
    const side = { ...SIDEBAR, bg: TEAL, text: "#FFFFFF", caption: "#FFFFFF" };
    const { tint, hover } = groupColours(side);
    expect(hover).toBe(`rgba(0, 0, 0, ${GROUP_HOVER.share})`);
    const shown = over(tint, hover ?? "");
    expect(contrast(side.text, shown)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(side).ink, shown)).toBeGreaterThanOrEqual(4.5);
  });

  it("inverts with a dark sidebar text: the tint is darker and the hover lighter, and both read 4.5:1 or more", () => {
    const side = { bg: "#FECC00", text: "#0D2142", active: "#0D2142", caption: "#0D2142" };
    const { tint, hover } = groupColours(side);
    expect(hover).toBe(`rgba(255, 255, 255, ${GROUP_HOVER.share})`);
    const t = white(tint);
    const b = white(side.bg);
    expect(t.r + t.g + t.b).toBeLessThan(b.r + b.g + b.b);
    const shown = over(tint, hover ?? "");
    expect(contrast(side.text, tint)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(side.text, shown)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(currentMark(side).ink, shown)).toBeGreaterThanOrEqual(4.5);
  });
});

describe("the capped hint's caption in the tinted group (decision 93)", () => {
  // The capped hint is drawn in the sidebar's caption colour on the group's tint. The loop checked the text and the mark only, so a caption
  // could read 3.85:1 on the default blue's 14% tint, and the hint would fail WCAG.
  it("on the default blue reads 4.5:1 or more on the tint, and on the hover over it", () => {
    const { tint, hover } = groupColours(SIDEBAR);
    expect(contrast(SIDEBAR.caption, tint)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(SIDEBAR.caption, over(tint, hover ?? ""))).toBeGreaterThanOrEqual(4.5);
  });

  it("on the e2e teal reads 4.5:1 or more on the tint, and on the hover over it", () => {
    // lib/brand.ts gives a caption that misses 4.5:1 on the sidebar the text colour (resolveBrand), so the teal's caption is white.
    const side = { ...SIDEBAR, bg: TEAL, text: "#FFFFFF", caption: "#FFFFFF" };
    const { tint, hover } = groupColours(side);
    expect(contrast(side.caption, tint)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(side.caption, over(tint, hover ?? ""))).toBeGreaterThanOrEqual(4.5);
  });
});

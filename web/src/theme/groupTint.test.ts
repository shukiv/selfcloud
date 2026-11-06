// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { contrast } from "../lib/brand";
import { currentMark } from "./currentMark";
import { GROUP_TINT, groupTint } from "./groupTint";
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

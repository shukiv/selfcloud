// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { contrast } from "../lib/brand";
import { SIDEBAR } from "./tokens";
import { currentMark, hoverOver } from "./currentMark";

// Decision 69 (option B): the current place's text and bar follow the sidebar's contrast rule.
describe("the sidebar's current-place mark", () => {
  it("is the sidebar's yellow for the text and the bar on the default blue (6.7:1 and 3:1 or more)", () => {
    expect(contrast(SIDEBAR.active, SIDEBAR.bg)).toBeGreaterThanOrEqual(4.5);
    expect(currentMark(SIDEBAR)).toEqual({ ink: SIDEBAR.active, bar: SIDEBAR.active });
  });

  it("keeps the yellow bar but takes the text colour for the text when the text reaches 3:1 only", () => {
    // A light blue on the default blue: about 3.5:1, so the bar reads and the text does not.
    const side = { ...SIDEBAR, active: "#5B9BE6" };
    const ratio = contrast(side.active, side.bg);
    expect(ratio).toBeGreaterThanOrEqual(3);
    expect(ratio).toBeLessThan(4.5);
    expect(currentMark(side)).toEqual({ ink: SIDEBAR.text, bar: side.active });
  });

  it("takes the text colour for both the text and the bar when neither reaches its contrast", () => {
    const side = { ...SIDEBAR, active: "#1E6BC4" };
    expect(contrast(side.active, side.bg)).toBeLessThan(3);
    expect(currentMark(side)).toEqual({ ink: SIDEBAR.text, bar: SIDEBAR.text });
  });
});

// Decision 69: the current row keeps its mark colour on hover, so the text is checked on the hover tint too.
describe("the mark on the hover tint", () => {
  it("is the yellow on the default blue, light and dark (5.2:1 on the tint)", () => {
    const mark = currentMark(SIDEBAR);
    expect(mark.ink).toBe(SIDEBAR.active);
    expect(contrast(mark.ink, hoverOver(SIDEBAR.bg))).toBeGreaterThanOrEqual(4.5);
  });

  // A sidebar where the yellow reads on the sidebar but not on its hover tint: the mark takes the white text, which reads on both.
  it("takes the text colour when the yellow fails on the hover tint, though it reads on the sidebar", () => {
    const side = { ...SIDEBAR, bg: "#0030F0", text: "#FFFFFF" };
    expect(contrast(SIDEBAR.active, side.bg)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(SIDEBAR.active, hoverOver(side.bg))).toBeLessThan(4.5);
    const mark = currentMark(side);
    expect(mark.ink).toBe("#FFFFFF");
    expect(contrast(mark.ink, side.bg)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(mark.ink, hoverOver(side.bg))).toBeGreaterThanOrEqual(4.5);
  });

  // The known limit: on the e2e teal the white text reads 4.48:1 on the tint, and no colour of the palette (white, black, yellow)
  // reaches 4.5:1 on both the teal and its tint. The mark keeps the best of them (white), and the number is recorded, not rounded up.
  it("keeps white, the best reading on the teal tint (4.48:1, under 4.5:1), since no palette colour reaches 4.5:1 there", () => {
    const side = { ...SIDEBAR, bg: "#0F766E", text: "#FFFFFF" };
    expect(contrast("#FFFFFF", hoverOver(side.bg))).toBeGreaterThan(4.47);
    expect(contrast("#FFFFFF", hoverOver(side.bg))).toBeLessThan(4.5);
    expect(currentMark(side).ink).toBe("#FFFFFF");
  });

  it("blends the tint over the sidebar: white at 10%", () => {
    expect(hoverOver("#000000")).toBe("#1A1A1A");
    expect(hoverOver("#053D8E")).toBe("#1E5099");
  });
});

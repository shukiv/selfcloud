// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { TAP_GAP_MS, tapOpens } from "./tap";

// A finger's double tap is two taps close together. The first opens a folder; the second must not open a row of the
// folder that has just appeared under it.
describe("tapOpens: a tap that follows an open is part of the same double tap", () => {
  it("opens for the first tap", () => {
    expect(tapOpens(-Infinity, 0)).toBe(true);
  });

  it("does not open for a second tap within the gap, however soon the new rows appear", () => {
    expect(tapOpens(1000, 1000)).toBe(false);
    expect(tapOpens(1000, 1000 + TAP_GAP_MS - 1)).toBe(false);
  });

  it("opens again once the gap has passed", () => {
    expect(tapOpens(1000, 1000 + TAP_GAP_MS)).toBe(true);
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { COUNT_MIN_W, panelFixedW } from "./layout";

// The selection bar's fixed part (decision 201): Clear + the count + More + the bar's padding + the three gaps between
// those four. The count's measured width is reserved (decision 242), with COUNT_MIN_W as the floor when nothing is measured.
describe("panelFixedW", () => {
  it("keeps its result when no count width is given", () => {
    expect(panelFixedW(false)).toBe(2 * 40 + COUNT_MIN_W + 2 * 4 + 3 * 4);
    expect(panelFixedW(true)).toBe(2 * 44 + COUNT_MIN_W + 2 * 4 + 3 * 4);
    expect(panelFixedW(false, COUNT_MIN_W)).toBe(panelFixedW(false));
    expect(panelFixedW(true, COUNT_MIN_W)).toBe(panelFixedW(true));
  });

  it("reserves the count's measured width above the floor", () => {
    expect(panelFixedW(false, 90)).toBe(panelFixedW(false) + 42);
    expect(panelFixedW(true, 90)).toBe(panelFixedW(true) + 42);
  });
});

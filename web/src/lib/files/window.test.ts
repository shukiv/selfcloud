// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { WINDOW_FIRST_ROWS, WINDOW_MIN_ROWS, WINDOW_OVERSCAN, spacers, sameRange, windowRange, withPinned } from "./window";

// The windowing rule (decision 134): more than 150 loaded rows in My files' list are drawn as a slice around the screen, the
// rows on screen plus 10 each side, with two spacers that hold the place of the rest. Geometry is in pixels, in the window's
// coordinates: bodyTop is where the first row starts, viewTop and viewBottom the visible part of the list below the pinned
// header and above the window's bottom.

const ROW = 40;

describe("the threshold and the overscan are the named constants of decision 134", () => {
  it("windowing starts above 150 loaded rows, and the overscan is 10", () => {
    expect(WINDOW_MIN_ROWS).toBe(150);
    expect(WINDOW_OVERSCAN).toBe(10);
  });
});

describe("windowRange: under the threshold every row is drawn (no change for a small folder)", () => {
  it("150 rows: all of them, whatever the scroll", () => {
    const r = windowRange({ count: 150, rowH: ROW, bodyTop: 0, viewTop: 6000, viewBottom: 6800 });
    expect(r).toEqual({ start: 0, end: 150 });
  });

  it("0 rows: nothing", () => {
    expect(windowRange({ count: 0, rowH: ROW, bodyTop: 0, viewTop: 0, viewBottom: 800 })).toEqual({ start: 0, end: 0 });
  });
});

describe("windowRange: the slice is the rows on screen plus the overscan", () => {
  it("151 rows at the top: the rows on screen and 10 below them", () => {
    // 800 px of view at 40 px a row: rows 0 to 19 are on screen, so 0 to 29 are drawn.
    const r = windowRange({ count: 151, rowH: ROW, bodyTop: 100, viewTop: 100, viewBottom: 900 });
    expect(r).toEqual({ start: 0, end: 20 + WINDOW_OVERSCAN });
  });

  it("in the middle of 1,000 rows: the screen's rows with 10 either side", () => {
    // The first row on screen is 500 (the body starts 500 rows above the view's top).
    const bodyTop = 100 - 500 * ROW;
    const r = windowRange({ count: 1000, rowH: ROW, bodyTop, viewTop: 100, viewBottom: 900 });
    // Rows 500 to 519 are on screen (800 px = 20 rows).
    expect(r).toEqual({ start: 500 - 10, end: 520 + 10 });
  });

  it("the slice is never longer than the screen and two overscans", () => {
    const bodyTop = 100 - 300 * ROW;
    const r = windowRange({ count: 1000, rowH: ROW, bodyTop, viewTop: 100, viewBottom: 1100 });
    // 1,000 px of view at 40 px a row is 25 rows, plus one for a partly shown row, plus the overscan each side.
    expect(r.end - r.start).toBeLessThanOrEqual(25 + 1 + 2 * WINDOW_OVERSCAN);
  });

  it("at the far end the range stops at the last row", () => {
    const bodyTop = 100 - 999 * ROW;
    const r = windowRange({ count: 1000, rowH: ROW, bodyTop, viewTop: 100, viewBottom: 900 });
    expect(r.end).toBe(1000);
    expect(r.start).toBe(999 - WINDOW_OVERSCAN);
  });

  it("past the end of the list (the list has shrunk under the scroll) it still returns a range inside the list", () => {
    const bodyTop = 100 - 5000 * ROW;
    const r = windowRange({ count: 300, rowH: ROW, bodyTop, viewTop: 100, viewBottom: 900 });
    expect(r.end).toBe(300);
    expect(r.start).toBeGreaterThanOrEqual(0);
    expect(r.start).toBeLessThanOrEqual(r.end);
  });

  it("above the list (the list is below the view) it starts at the first row", () => {
    const r = windowRange({ count: 500, rowH: ROW, bodyTop: 2000, viewTop: 100, viewBottom: 900 });
    expect(r.start).toBe(0);
    expect(r.end).toBeLessThanOrEqual(WINDOW_OVERSCAN + 1);
  });
});

describe("windowRange: before the row height is measured", () => {
  it("draws the first rows only, so the first frame is cheap", () => {
    const r = windowRange({ count: 1000, rowH: 0, bodyTop: 0, viewTop: 0, viewBottom: 800 });
    expect(r).toEqual({ start: 0, end: WINDOW_FIRST_ROWS });
  });

  it("with a short list it still draws every row", () => {
    expect(windowRange({ count: 20, rowH: 0, bodyTop: 0, viewTop: 0, viewBottom: 800 })).toEqual({ start: 0, end: 20 });
  });
});

describe("withPinned: the row being renamed is always in the range", () => {
  it("a pinned row inside the range changes nothing", () => {
    expect(withPinned({ start: 0, end: 30 }, 12, 1000)).toEqual({ start: 0, end: 30 });
  });

  it("a pinned row below the range extends it to the row", () => {
    expect(withPinned({ start: 0, end: 30 }, 400, 1000)).toEqual({ start: 0, end: 401 });
  });

  it("a pinned row above the range extends it up to the row", () => {
    expect(withPinned({ start: 500, end: 530 }, 200, 1000)).toEqual({ start: 200, end: 530 });
  });

  it("no pinned row (-1, or a row not in the list) changes nothing", () => {
    expect(withPinned({ start: 5, end: 9 }, -1, 1000)).toEqual({ start: 5, end: 9 });
    expect(withPinned({ start: 5, end: 9 }, 1000, 1000)).toEqual({ start: 5, end: 9 });
  });
});

describe("spacers: the two spacer rows hold the place of the rows left out", () => {
  it("the top and bottom heights, with the drawn rows, add up to the whole list", () => {
    const range = { start: 490, end: 530 };
    const s = spacers(range, 1000, ROW);
    expect(s).toEqual({ top: 490 * ROW, bottom: 470 * ROW });
    expect(s.top + (range.end - range.start) * ROW + s.bottom).toBe(1000 * ROW);
  });

  it("the first range has no top spacer, the last has no bottom one", () => {
    expect(spacers({ start: 0, end: 30 }, 1000, ROW).top).toBe(0);
    expect(spacers({ start: 970, end: 1000 }, 1000, ROW).bottom).toBe(0);
  });

  it("an unmeasured row height gives spacers of no height (the first frame, before the measure)", () => {
    expect(spacers({ start: 0, end: 60 }, 1000, 0)).toEqual({ top: 0, bottom: 0 });
  });
});

describe("sameRange: the scroll handler changes state only when the range does", () => {
  it("equal ranges are the same, different ones are not", () => {
    expect(sameRange({ start: 1, end: 9 }, { start: 1, end: 9 })).toBe(true);
    expect(sameRange({ start: 1, end: 9 }, { start: 1, end: 10 })).toBe(false);
    expect(sameRange({ start: 0, end: 9 }, { start: 1, end: 9 })).toBe(false);
  });
});

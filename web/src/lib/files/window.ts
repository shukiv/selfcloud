// SPDX-License-Identifier: AGPL-3.0-or-later

// The slice of My files' list that is drawn (decision 183): the rows on the screen, with a margin of rows either side, and the
// two spacer rows that hold the place of the rest. The rule is pure: the page measures the geometry, and these functions say
// which rows to draw. A list of 150 rows or fewer draws every row, as it did before the windowing.

// WINDOW_MIN_ROWS is the most loaded rows a list draws in full. Above it, only a slice is drawn.
export const WINDOW_MIN_ROWS = 150;
// WINDOW_OVERSCAN is how many rows are drawn above and below the screen, so a short scroll shows no gap.
export const WINDOW_OVERSCAN = 10;
// WINDOW_FIRST_ROWS is how many rows are drawn before a row's height is measured: the first frame, and nothing more.
export const WINDOW_FIRST_ROWS = 60;

// WindowRange is the rows drawn: from start (inclusive) to end (exclusive), in the list's order.
export interface WindowRange {
  start: number;
  end: number;
}

// WindowGeometry is what the slice is worked out from, in pixels in the window's coordinates. count is the loaded rows; rowH is
// one row's height (0 until it is measured); bodyTop is where the first row starts; viewTop and viewBottom are the visible part
// of the list, under the pinned header and above the window's bottom.
export interface WindowGeometry {
  count: number;
  rowH: number;
  bodyTop: number;
  viewTop: number;
  viewBottom: number;
}

function clamp(n: number, lo: number, hi: number): number {
  return Math.min(hi, Math.max(lo, n));
}

// windowRange is the rows to draw for a geometry: the rows the view shows, widened by the overscan, inside the list.
export function windowRange(g: WindowGeometry): WindowRange {
  const { count } = g;
  if (count <= WINDOW_MIN_ROWS) return { start: 0, end: count };
  if (!(g.rowH > 0)) return { start: 0, end: Math.min(count, WINDOW_FIRST_ROWS) };
  const first = clamp(Math.floor((g.viewTop - g.bodyTop) / g.rowH), 0, count);
  const last = clamp(Math.ceil((g.viewBottom - g.bodyTop) / g.rowH), 0, count);
  const start = Math.max(0, first - WINDOW_OVERSCAN);
  const end = Math.min(count, Math.max(last, first) + WINDOW_OVERSCAN);
  return { start, end };
}

// withPinned widens a range to take in the pinned row, the one being renamed, which stays drawn whatever the scroll. A pinned row
// that is not in the list (-1, or past the end) changes nothing.
export function withPinned(r: WindowRange, pinned: number, count: number): WindowRange {
  if (!(pinned >= 0 && pinned < count)) return r;
  return { start: Math.min(r.start, pinned), end: Math.min(count, Math.max(r.end, pinned + 1)) };
}

// spacers are the heights of the two spacer rows: the rows above the range, and the rows below it. With the drawn rows they add up
// to every loaded row. An unmeasured row height (0) gives spacers of no height.
export function spacers(r: WindowRange, count: number, rowH: number): { top: number; bottom: number } {
  return { top: r.start * rowH, bottom: (count - r.end) * rowH };
}

// sameRange says two ranges draw the same rows, so a scroll that changes nothing changes no state.
export function sameRange(a: WindowRange, b: WindowRange): boolean {
  return a.start === b.start && a.end === b.end;
}

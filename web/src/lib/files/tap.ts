// SPDX-License-Identifier: AGPL-3.0-or-later

// TAP_GAP_MS is how soon after a finger's open a tap may come and still be part of the same double tap. A double tap is
// two taps about a fifth of a second apart; the rows of a folder just opened can be on screen by then, so the second
// tap would open one of them unless it is taken as part of the first.
export const TAP_GAP_MS = 400;

// tapOpens says whether a tap at time now may open an item, given when the last tap opened one.
export function tapOpens(lastOpenAt: number, now: number): boolean {
  return now - lastOpenAt >= TAP_GAP_MS;
}

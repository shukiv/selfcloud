// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Snippet } from "./api";

// Part is a piece of a snippet: its text, and whether it is a match.
export interface Part {
  text: string;
  hit: boolean;
}

// snippetParts cuts a snippet into its plain pieces. The server's ranges are UTF-8 byte offsets, and a JavaScript string is
// counted in UTF-16 units, so the text is cut by bytes and each piece is decoded on its own. A range that is out of bounds,
// empty or overlapping an earlier one is dropped, so a bad answer shows its text without a highlight.
export function snippetParts(s: Snippet): Part[] {
  const bytes = new TextEncoder().encode(s.text);
  const dec = new TextDecoder("utf-8");
  const spans = normalise(s.ranges, bytes.length);
  const out: Part[] = [];
  let at = 0;
  for (const [a, b] of spans) {
    if (a > at) out.push({ text: dec.decode(bytes.subarray(at, a)), hit: false });
    out.push({ text: dec.decode(bytes.subarray(a, b)), hit: true });
    at = b;
  }
  if (at < bytes.length) out.push({ text: dec.decode(bytes.subarray(at)), hit: false });
  return out.filter((p) => p.text !== "");
}

function normalise(ranges: [number, number][], size: number): [number, number][] {
  const ok = (Array.isArray(ranges) ? ranges : [])
    .filter((r) => Array.isArray(r) && Number.isInteger(r[0]) && Number.isInteger(r[1]) && r[0] >= 0 && r[0] < r[1] && r[1] <= size)
    .sort((x, y) => x[0] - y[0]);
  const out: [number, number][] = [];
  for (const r of ok) {
    const last = out[out.length - 1];
    if (last && r[0] < last[1]) continue;
    out.push([r[0], r[1]]);
  }
  return out;
}

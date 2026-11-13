// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// Slice fu3b (decision 77): the list's column header is pinned in the list's own table, by a sticky rule that reads the
// place the Listing sets (--sc-list-top). The frame check that samples the header and the rows is e2e/fu3b.mjs.
const indexCss = readFileSync(join(__dirname, "../../index.css"), "utf8");

// The rule is the shared CSS group of decision 114 (slice tl1): My files (.files-list) and every preset list (.item-list) pin
// their header the same way.
const RULE = /\.files-list \.ant-table-thead > tr > th,\s*\.item-list \.ant-table-thead > tr > th\s*\{([^}]*)\}/;

describe("the pinned header of the file list (fu3b)", () => {
  it("pins the header cells of its own table under the toolbar, at the place the Listing sets", () => {
    const rule = indexCss.match(RULE);
    expect(rule, "the rule that pins the header cells").not.toBeNull();
    expect(rule?.[1]).toMatch(/position:\s*sticky;/);
    expect(rule?.[1]).toMatch(/top:\s*var\(--sc-list-top\);/);
  });

  // The header keeps antd's tint, the theme's token (colorFillAlter), over the list's solid background, so rows do not show
  // through the pinned header (fu3b).
  it("takes the header's tint from the theme's token, over a solid background", () => {
    const rule = indexCss.match(RULE);
    expect(rule?.[1]).toMatch(/background:\s*linear-gradient\(var\(--sc-list-head-tint\),\s*var\(--sc-list-head-tint\)\),\s*var\(--sc-list-head\);/);
  });
});

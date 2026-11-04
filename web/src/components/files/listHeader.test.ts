// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

// Slice fu3b (decision 77, item 1): the list's column header is pinned in the list's own table, by a sticky rule that reads the
// place the Listing sets (--sc-list-top). The frame check that samples the header and the rows is e2e/fu3b.mjs.
const indexCss = readFileSync(join(__dirname, "../../index.css"), "utf8");

describe("the pinned header of the file list (fu3b, item 1)", () => {
  it("pins the header cells of its own table under the toolbar, at the place the Listing sets", () => {
    const rule = indexCss.match(/\.files-list \.ant-table-thead > tr > th\s*\{([^}]*)\}/);
    expect(rule, "the rule that pins the header cells").not.toBeNull();
    expect(rule?.[1]).toMatch(/position:\s*sticky;/);
    expect(rule?.[1]).toMatch(/top:\s*var\(--sc-list-top\);/);
  });
});

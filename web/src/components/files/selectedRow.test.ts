// SPDX-License-Identifier: AGPL-3.0-or-later

// A selected row in the file list is a light accent band with no edge line above or below it (ic2, decision 72: the owner's
// note on the dev run, which reverses the edge of decision 70). The colour is the list's variable, so light and dark both take it.
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const indexCss = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");

describe("file list selected row (decision 72)", () => {
  const start = indexCss.indexOf('.files-list .ant-table-tbody > tr[aria-selected="true"] > td,');
  const block = indexCss.slice(start, indexCss.indexOf("}", start) + 1);

  it("paints the selected row with the list's accent band colour", () => {
    expect(start).toBeGreaterThanOrEqual(0);
    expect(block).toMatch(/background:\s*var\(--sc-row-selected\);/);
  });

  it("draws no edge line on the selected row", () => {
    expect(block).not.toMatch(/box-shadow/);
    expect(block).not.toMatch(/border/);
    expect(block).not.toMatch(/outline/);
  });
});

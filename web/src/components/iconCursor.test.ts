// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";

// A disabled icon button passes its pointer events to its wrapper (IconButton.tsx), so the button itself no longer shows the
// browser cursor for it. The wrapper must show antd's not-allowed cursor (ic2 item 10, from the ic1 review).
const indexCss = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");

describe("disabled icon button cursor (decision 71)", () => {
  it("shows the not-allowed cursor on the wrapper of a disabled icon button", () => {
    const start = indexCss.indexOf(".icon-btn-wrap:has(> .icon-btn:disabled)");
    expect(start).toBeGreaterThanOrEqual(0);
    const block = indexCss.slice(start, indexCss.indexOf("}", start) + 1);
    expect(block).toMatch(/cursor:\s*not-allowed;/);
  });
});

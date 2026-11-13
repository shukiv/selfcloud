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

  // A busy row's icon is aria-disabled, not disabled, so it keeps the focus (decision 114). antd gives its hover background to a
  // text button that is not :disabled, so the aria-disabled icon is kept from the pointer as a disabled one is: it passes the
  // pointer to its wrapper, the wrapper shows not-allowed, and the icon has the disabled colour. The e2e
  // reads the background under the pointer in Chrome.
  it("treats an aria-disabled icon button as a disabled one: the pointer to the wrapper, not-allowed, the disabled colour", () => {
    const blockOf = (selector: string) => {
      const start = indexCss.indexOf(selector);
      expect(start, selector).toBeGreaterThanOrEqual(0);
      return indexCss.slice(start, indexCss.indexOf("}", start) + 1);
    };
    expect(blockOf('.icon-btn-wrap > .icon-btn[aria-disabled="true"]')).toMatch(/pointer-events:\s*none;/);
    expect(blockOf('.icon-btn-wrap:has(> .icon-btn[aria-disabled="true"])')).toMatch(/cursor:\s*not-allowed;/);
    expect(blockOf('.icon-btn.ant-btn.ant-btn[aria-disabled="true"]')).toMatch(/color:\s*var\(--ant-color-text-disabled\);/);
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist drawer's shadow is not cut at the edges of the viewer (ic2, decision 72). The drawer sits inside the playlist's box,
// which clips its painting; the clip keeps a margin as big as the shadow's blur, and a closed drawer (off to the side, with no shadow)
// is hidden, so the margin shows nothing of it.
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";

const indexCss = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");
const rule = (selector: string) => {
  const start = indexCss.indexOf(`${selector} {`);
  if (start < 0) throw new Error(`no rule ${selector}`);
  return indexCss.slice(start, indexCss.indexOf("}", start) + 1);
};

describe("playlist drawer shadow (decision 72)", () => {
  it("the playlist clips with a margin at least as big as the drawer's shadow blur", () => {
    const shadow = rule(".media-drawer").match(/box-shadow:\s*0 0 (\d+)px/);
    expect(shadow).not.toBeNull();
    const margin = rule(".media-playlist").match(/overflow-clip-margin:\s*(\d+)px/);
    expect(margin).not.toBeNull();
    expect(Number(margin?.[1])).toBeGreaterThanOrEqual(Number(shadow?.[1]));
  });

  it("a closed drawer is moved past the clip margin, so the margin shows none of it, and is not hidden (hidden would take the focus away)", () => {
    expect(rule(".media-drawer--closed")).toMatch(/translateX\(calc\(100% \+ 24px\)\)/);
    expect(rule(".media-drawer--closed")).not.toMatch(/visibility/);
  });
});

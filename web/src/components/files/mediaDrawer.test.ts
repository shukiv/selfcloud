// SPDX-License-Identifier: AGPL-3.0-or-later

// The playlist drawer's shadow is not cut at the edges of the viewer (ic2, decision 72). The open drawer's screen takes no clip, so the
// shadow is not cut; a closed drawer (off to the side, with no shadow) stays clipped by the screen and is never hidden (focus).
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
  // Safari and iOS Safari do not support overflow-clip-margin (caniuse, read 2026-11-03: not supported through 27.2), so the shadow
  // cannot rely on a clip margin there. The open drawer's screen takes no clip at all, and the shadow is inside nothing that clips it.
  it("no rule relies on overflow-clip-margin, which Safari does not support", () => {
    expect(indexCss).not.toMatch(/overflow-clip-margin/);
  });

  it("the screen does not clip an open drawer, so its shadow is not cut at the screen's edges (the clip is kept when closed)", () => {
    // The rule that takes the clip off: its own block, after the screen's clip (the 600 px rule for the drawer's padding comes earlier).
    const at = indexCss.indexOf(".media-screen--drawer {\n  overflow: visible;");
    expect(at).toBeGreaterThan(0);
    expect(at).toBeGreaterThan(indexCss.indexOf("\n.media-screen {"));
    expect(rule(".media-screen")).toMatch(/overflow:\s*clip/);
  });

  it("a closed drawer is moved past the screen's edge, and is not hidden (hidden would take the focus away)", () => {
    expect(rule(".media-drawer--closed")).toMatch(/translateX\(calc\(100% \+ 24px\)\)/);
    expect(rule(".media-drawer--closed")).not.toMatch(/visibility/);
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { contrast } from "../lib/brand";
import { antdTokens } from "../theme/theme";
import { PALETTE, SIDEBAR } from "../theme/tokens";
import { menuHeaderInks, type MenuHeaderMode } from "./menuHeader";

// Decision 95: a menu's header line (the account's "Signed in as", a New menu's read-only reason) is text on the menu's ground, not a
// disabled item. Its inks read 4.5:1 or more on the ground, in light and in dark, with the palette the app builds.

const MODES: MenuHeaderMode[] = ["light", "dark"];

describe("a menu header's colours (decision 95)", () => {
  for (const mode of MODES) {
    it(`in ${mode}, the label and the email read 4.5:1 or more on the menu's ground`, () => {
      const ink = menuHeaderInks(mode);
      expect(contrast(ink.label, ink.ground)).toBeGreaterThanOrEqual(4.5);
      expect(contrast(ink.email, ink.ground)).toBeGreaterThanOrEqual(4.5);
    });

    it(`in ${mode}, the ground is the popup's own (colorBgElevated), not the page's`, () => {
      const token = antdTokens(mode, PALETTE[mode], SIDEBAR).token ?? {};
      expect(menuHeaderInks(mode).ground).toBe(token.colorBgElevated);
    });
  }
});

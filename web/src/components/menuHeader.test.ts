// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { contrast } from "../lib/brand";
import { splitParts } from "../lib/signin";
import { antdTokens } from "../theme/theme";
import { PALETTE, SIDEBAR } from "../theme/tokens";
import he from "../i18n/he.json";
import en from "../i18n/en.json";
import { menuHeaderInks, trailingLabel } from "./menuHeader";

// Decision 95: a menu's header (the account's "Signed in as", a New menu's read-only reason) is text on the menu's ground, not a disabled
// item. Its inks read 4.5:1 or more on the ground, in light and in dark, with the palette the app builds (antdTokens), which is what the
// header renders from (menuHeaderInks, through antd's computed token). The header's colours are set from menuHeaderInks, so a change of
// the ink in menuHeaderInks changes what is drawn, and this test reads the same values.

const MODES = ["light", "dark"] as const;

describe("a menu header's colours (decision 95)", () => {
  for (const mode of MODES) {
    it(`in ${mode}, the label and the email read 4.5:1 or more on the menu's ground`, () => {
      const token = antdTokens(mode, PALETTE[mode], SIDEBAR).token ?? {};
      const ink = menuHeaderInks(token);
      const ground = String(token.colorBgElevated);
      expect(contrast(ink.label, ground)).toBeGreaterThanOrEqual(4.5);
      expect(contrast(ink.email, ground)).toBeGreaterThanOrEqual(4.5);
    });

    it(`in ${mode}, the inks are the secondary and the primary text, on the popup's own ground (colorBgElevated)`, () => {
      const token = antdTokens(mode, PALETTE[mode], SIDEBAR).token ?? {};
      const ink = menuHeaderInks(token);
      expect(token.colorBgElevated).toBeDefined();
      expect(ink.label).toBe(token.colorTextSecondary);
      expect(ink.email).toBe(token.colorText);
    });
  }
});

describe("the Signed in as label (decision 95)", () => {
  it("is the template's words before the name, with the Hebrew hyphen left off the end", () => {
    const hebrew = trailingLabel(splitParts(he["user.signedInAs"], { name: "mateo@example.com" }));
    expect(hebrew.endsWith("‑")).toBe(false);
    expect(hebrew.endsWith("-")).toBe(false);
    expect(hebrew.length).toBeGreaterThan(0);
  });

  it("is 'Signed in as' in English", () => {
    expect(trailingLabel(splitParts(en["user.signedInAs"], { name: "mateo@example.com" }))).toBe("Signed in as");
  });
});

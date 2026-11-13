// SPDX-License-Identifier: AGPL-3.0-or-later

// The menu row height is one value per pointer (40 px, 44 px on a coarse pointer) in three places: the TypeScript constant the
// menus use, the CSS custom property the dropdown's submenu titles use, and the menu theme's item height. This test keeps the
// constant and the stylesheet's custom property the same, and says the submenu title reads the property .
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { MENU_ROW_HEIGHT } from "./newMenu";

const css = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");

describe("the menu row height is one value per pointer", () => {
  it("the constant is 40 px on a fine pointer and 44 px on a coarse one", () => {
    expect(MENU_ROW_HEIGHT).toEqual({ fine: 40, coarse: 44 });
  });

  it("the stylesheet's --menu-row-height is the same 40 px, and 44 px on a coarse pointer", () => {
    const fine = /:root\s*\{[^}]*--menu-row-height:\s*(\d+)px/.exec(css);
    const coarse = /@media \(pointer: coarse\)\s*\{\s*:root\s*\{[^}]*--menu-row-height:\s*(\d+)px/.exec(css);
    expect(Number(fine?.[1])).toBe(MENU_ROW_HEIGHT.fine);
    expect(Number(coarse?.[1])).toBe(MENU_ROW_HEIGHT.coarse);
  });

  it("a dropdown submenu title takes the row height from the property, not from a literal", () => {
    expect(/\.ant-dropdown-menu-submenu-title\s*\{[^}]*block-size:\s*var\(--menu-row-height\)/.test(css)).toBe(true);
  });
});

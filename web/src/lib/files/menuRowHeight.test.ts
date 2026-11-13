// SPDX-License-Identifier: AGPL-3.0-or-later

// The New menu's row height is one value per pointer (40 px, 44 px on a coarse pointer) in the TypeScript constant the menu
// uses and the menu theme's item height. Each menu sizes its own rows: the New menu's wrappers take the constant, and the
// file menu's plain rows take 44 px on a finger. A submenu title fills the row it is in, so it is as tall as that menu's plain
// rows and never takes another menu's height. This test keeps the constant and says the stylesheet's title fills its row.
import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { MENU_ROW_HEIGHT } from "./newMenu";

const css = readFileSync(join(process.cwd(), "src", "index.css"), "utf8");

describe("the menu row height comes from each menu's rows", () => {
  it("the New menu's constant is 40 px on a fine pointer and 44 px on a coarse one", () => {
    expect(MENU_ROW_HEIGHT).toEqual({ fine: 40, coarse: 44 });
  });

  it("a dropdown submenu title fills the row it is in, and takes no fixed height", () => {
    expect(/\.ant-dropdown-menu-submenu-title\s*\{[^}]*block-size:\s*100%/.test(css)).toBe(true);
    expect(/\.ant-dropdown-menu-submenu-title\s*\{[^}]*block-size:\s*(var\(--menu-row-height\)|\d+px)/.test(css)).toBe(false);
  });
});

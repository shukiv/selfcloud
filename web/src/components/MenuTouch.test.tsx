// SPDX-License-Identifier: AGPL-3.0-or-later

// The top bar's language and theme menus size their rows for a finger (mob3): 44 px on a coarse pointer, the mouse's height
// otherwise. The row's height is set on each item, so the check reads it from the item the menu draws.
import type { ReactNode } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import { setLang } from "../lib/i18n";
import { coarsePointer } from "../test/viewport";
import LangSwitcher from "./LangSwitcher";
import ThemeMenu from "./ThemeMenu";

let restore: () => void = () => undefined;
beforeEach(async () => {
  window.localStorage.clear();
  await setLang("en");
});
afterEach(() => {
  restore();
  restore = () => undefined;
  window.localStorage.clear();
});

function wrap(ui: ReactNode) {
  return render(
    <ThemeProvider>
      <Direction>{ui}</Direction>
    </ThemeProvider>,
  );
}

describe("the top bar menus' rows are a finger's size (mob3)", () => {
  it("the language menu's rows are 44 px on a coarse pointer", async () => {
    restore = coarsePointer();
    wrap(<LangSwitcher />);
    fireEvent.click(screen.getByTestId("language"));
    const item = await screen.findByRole("menuitem", { name: "English" });
    expect(item.style.height).toBe("44px");
  });

  it("the language menu's rows keep their height on a mouse", async () => {
    wrap(<LangSwitcher />);
    fireEvent.click(screen.getByTestId("language"));
    const item = await screen.findByRole("menuitem", { name: "English" });
    expect(item.style.height).toBe("");
  });

  it("the theme menu's rows are 44 px on a coarse pointer", async () => {
    restore = coarsePointer();
    wrap(<ThemeMenu />);
    fireEvent.click(screen.getByTestId("theme"));
    const item = await screen.findByRole("menuitem", { name: "Dark" });
    expect(item.style.height).toBe("44px");
  });

  it("the theme menu's rows keep their height on a mouse", async () => {
    wrap(<ThemeMenu />);
    fireEvent.click(screen.getByTestId("theme"));
    const item = await screen.findByRole("menuitem", { name: "Dark" });
    expect(item.style.height).toBe("");
  });
});
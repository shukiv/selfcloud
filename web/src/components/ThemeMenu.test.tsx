// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import ThemeMenu from "./ThemeMenu";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import { setLang } from "../lib/i18n";

function renderMenu() {
  return render(
    <ThemeProvider>
      <Direction>
        <ThemeMenu />
      </Direction>
    </ThemeProvider>,
  );
}

describe("ThemeMenu (decision 68, the top bar's theme)", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
  });

  // The name and the tooltip say the current choice, so the icon says what it is set to.
  it("is one icon button named for the current choice, with no words on the bar", () => {
    renderMenu();
    const button = screen.getByRole("button", { name: "Theme: System" });
    expect(button.textContent).toBe("");
    expect(screen.queryByRole("menuitem", { name: "Dark" })).toBeNull();
  });

  it("opens System, Light and Dark, and marks the current choice", async () => {
    renderMenu();
    fireEvent.click(screen.getByRole("button", { name: "Theme: System" }));
    for (const name of ["System", "Light", "Dark"]) expect(await screen.findByRole("menuitem", { name })).toBeTruthy();
    const system = screen.getByRole("menuitem", { name: "System" });
    expect(system.className).toMatch(/selected/);
    expect(screen.getByRole("menuitem", { name: "Dark" }).className).not.toMatch(/selected/);
  });

  it("sets the theme and saves it", async () => {
    renderMenu();
    fireEvent.click(screen.getByRole("button", { name: "Theme: System" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Dark" }));
    await waitFor(() => expect(document.documentElement.dataset.theme).toBe("dark"));
    expect(screen.getByRole("button", { name: "Theme: Dark" })).toBeTruthy();
    expect(window.localStorage.getItem("selfcloud.theme") ?? "").toMatch(/dark/);
  });
});

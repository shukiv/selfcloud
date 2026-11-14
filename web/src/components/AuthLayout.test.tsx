// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import AuthLayout from "./AuthLayout";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import { setLang } from "../lib/i18n";

// The share variant (pb1): the open-link page asks for it, and the sign-in pages do not.
describe("AuthLayout share variant (pb1)", () => {
  it("adds auth-layout--share with variant=share, and no class without it", () => {
    const { container, rerender } = render(
      <ThemeProvider>
        <Direction>
          <AuthLayout variant="share">
            <p>form</p>
          </AuthLayout>
        </Direction>
      </ThemeProvider>,
    );
    expect(container.querySelector("main.auth-layout--share")).toBeTruthy();
    rerender(
      <ThemeProvider>
        <Direction>
          <AuthLayout>
            <p>form</p>
          </AuthLayout>
        </Direction>
      </ThemeProvider>,
    );
    expect(container.querySelector("main.auth-layout--share")).toBeNull();
    expect(container.querySelector("main.auth-layout")).toBeTruthy();
  });
});

// The sign-in pages use the shell's icon controls (ic2, decision 71): the language and the theme are icon buttons with a name,
// not the antd segmented switch with its words.
describe("AuthLayout controls (decision 71)", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
  });

  it("shows the language and the theme as icon buttons, and no segmented switch", () => {
    const { container } = render(
      <ThemeProvider>
        <Direction>
          <AuthLayout>
            <p>form</p>
          </AuthLayout>
        </Direction>
      </ThemeProvider>,
    );
    expect(container.querySelector(".ant-segmented")).toBeNull();
    const theme = screen.getByRole("button", { name: "Theme: System" });
    expect(theme.textContent).toBe("");
    const language = screen.getByRole("button", { name: /Language/ });
    expect(language.textContent).toBe("");
  });
});

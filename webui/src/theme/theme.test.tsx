// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, screen, fireEvent } from "@testing-library/react";
import { theme as antdTheme } from "antd";
import { describe, expect, it, beforeEach } from "vitest";
import { ThemeProvider, antdTokens, useTheme } from "./theme";
import { DARK, LIGHT, SIDEBAR, RADIUS } from "./tokens";

function Probe() {
  const { mode, pref, toggle, setPref } = useTheme();
  return (
    <div>
      <span data-testid="mode">{mode}</span>
      <span data-testid="pref">{pref}</span>
      <button onClick={toggle}>toggle</button>
      <button onClick={() => setPref("system")}>system</button>
    </div>
  );
}

describe("antdTokens", () => {
  it("uses the dark algorithm only in dark mode", () => {
    expect(antdTokens("dark", DARK, SIDEBAR).algorithm).toBe(antdTheme.darkAlgorithm);
    expect(antdTokens("light", LIGHT, SIDEBAR).algorithm).toBe(antdTheme.defaultAlgorithm);
  });

  it("takes the primary colour from the palette and keeps the radius", () => {
    const t = antdTokens("light", LIGHT, SIDEBAR).token!;
    expect(t.colorPrimary).toBe(LIGHT.accent);
    expect(t.borderRadius).toBe(RADIUS);
  });

  it("paints the sidebar menu from the sidebar colours", () => {
    const menu = antdTokens("light", LIGHT, { ...SIDEBAR, bg: "#123456" }).components!.Menu!;
    expect(menu.darkItemBg).toBe("#123456");
    expect(menu.darkItemSelectedBg).toBe(SIDEBAR.active);
  });
});

describe("ThemeProvider", () => {
  beforeEach(() => {
    window.localStorage.clear();
    document.documentElement.removeAttribute("data-theme");
    document.documentElement.style.removeProperty("--link-colour");
  });

  // A plain link takes the antd colorLink token of the mode in effect (guard: the --link-colour write in ThemeProvider).
  it.each([
    ["light", LIGHT],
    ["dark", DARK],
  ] as const)("a plain link is painted in the %s link colour", (mode, palette) => {
    window.localStorage.setItem("selfcloud.theme", mode);
    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );
    const want = antdTokens(mode, palette, SIDEBAR).token!.colorLink as string;
    expect(document.documentElement.style.getPropertyValue("--link-colour").toUpperCase()).toBe(want.toUpperCase());
  });

  it("starts on the system choice and writes the choice it is given", () => {
    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );
    expect(screen.getByTestId("pref").textContent).toBe("system");
    fireEvent.click(screen.getByText("toggle"));
    expect(screen.getByTestId("pref").textContent).toBe("dark");
    expect(window.localStorage.getItem("selfcloud.theme")).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    act(() => {
      fireEvent.click(screen.getByText("system"));
    });
    expect(window.localStorage.getItem("selfcloud.theme")).toBe("system");
  });

  it("keeps a choice saved by an earlier visit", () => {
    window.localStorage.setItem("selfcloud.theme", "light");
    render(
      <ThemeProvider>
        <Probe />
      </ThemeProvider>,
    );
    expect(screen.getByTestId("pref").textContent).toBe("light");
    expect(screen.getByTestId("mode").textContent).toBe("light");
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { readdirSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import { render } from "@testing-library/react";
import { theme as antdTheme } from "antd";
import { describe, expect, it } from "vitest";
import { antdVars } from "./antdVars";
import { ThemeProvider, antdTokens } from "./theme";
import { DARK, LIGHT, SIDEBAR } from "./tokens";

// Every source file of the app, without the tests.
function sources(dir: string): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) out.push(...sources(path));
    else if (/\.(tsx?|css)$/.test(name) && !/\.test\.tsx?$/.test(name)) out.push(path);
  }
  return out;
}

// The antd variables the app's CSS and inline styles read, as var(--ant-...).
function usedNames(): string[] {
  const names = new Set<string>();
  for (const file of sources(join(process.cwd(), "src"))) {
    for (const m of readFileSync(file, "utf8").matchAll(/var\((--ant-[a-z0-9-]+)/g)) names.add(m[1]);
  }
  return [...names].sort();
}

describe("antd variables", () => {
  // antd's own CSS variables are off (no cssVar in the ConfigProvider), so a var(--ant-...) that nothing defines paints
  // nothing: secondary text took the main text colour and the split lines were missing (pending 334-335).
  it("defines every antd variable the app uses, in both modes", () => {
    const used = usedNames();
    expect(used.length).toBeGreaterThan(5);
    for (const [mode, palette] of [
      ["light", LIGHT],
      ["dark", DARK],
    ] as const) {
      const token = antdTheme.getDesignToken(antdTokens(mode, palette, SIDEBAR));
      const vars = antdVars(token as unknown as Record<string, unknown>);
      const missing = used.filter((n) => !vars[n]);
      expect(missing, `${mode}: not defined`).toEqual([]);
      expect(vars["--ant-color-text-secondary"]).toBe(token.colorTextSecondary);
      expect(vars["--ant-control-item-bg-hover"]).toBe(token.controlItemBgHover);
    }
  });

  it.each([
    ["light", LIGHT],
    ["dark", DARK],
  ] as const)("writes them on the root element in the %s mode", (mode, palette) => {
    window.localStorage.setItem("selfcloud.theme", mode);
    document.documentElement.style.removeProperty("--ant-color-text-secondary");
    document.documentElement.style.removeProperty("--ant-color-split");
    const { unmount } = render(
      <ThemeProvider>
        <span />
      </ThemeProvider>,
    );
    const root = document.documentElement.style;
    const want = antdTheme.getDesignToken(antdTokens(mode, palette, SIDEBAR));
    expect(root.getPropertyValue("--ant-color-text-secondary")).toBe(want.colorTextSecondary);
    expect(root.getPropertyValue("--ant-color-split")).toBe(want.colorSplit);
    unmount();
    window.localStorage.clear();
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { PALETTE } from "../../theme/tokens";
import { COLOURS, colourVar, isColour } from "./palette";

// The server's palette (internal/store/nodes.go, Colors), in display order.
const SERVER = ["red", "orange", "yellow", "green", "teal", "blue", "indigo", "purple", "pink", "brown", "slate", "grey"];

// swatches reads each colour's value from index.css: the first block sets the light values, the block for the dark
// theme sets the dark ones. A colour missing from either block is an error the test names.
function swatches(): { light: Record<string, string>; dark: Record<string, string> } {
  // vitest runs from web/, where the stylesheet is at src/index.css.
  const css = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");
  const block = (selector: string): Record<string, string> => {
    const start = css.indexOf(`${selector} {`);
    if (start < 0) throw new Error(`index.css has no block ${selector}`);
    const body = css.slice(start, css.indexOf("}", start));
    const out: Record<string, string> = {};
    for (const m of body.matchAll(/--colour-([a-z]+):\s*(#[0-9a-fA-F]{6})\s*;/g)) out[m[1]] = m[2];
    return out;
  };
  return { light: block(":root"), dark: block(':root[data-theme="dark"]') };
}

// contrast is the WCAG contrast ratio of two hex colours.
function contrast(a: string, b: string): number {
  const lum = (hex: string) => {
    const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255).map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
    return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
  };
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

describe("folder colour names in every catalogue", () => {
  // check-keys reads only literal keys; colour.<name> is built at run time, so each language is checked here.
  it("has colour.<name> in all seven catalogues", () => {
    for (const lang of ["en", "es", "fr", "he", "pt", "ru", "ar"]) {
      const cat = JSON.parse(readFileSync(resolve(process.cwd(), `src/i18n/${lang}.json`), "utf8")) as Record<string, string>;
      for (const c of COLOURS) expect(cat[`colour.${c}`], `${lang} colour.${c}`).toBeTruthy();
    }
  });
});

describe("folder colours", () => {
  it("are the twelve names the server stores, in its order", () => {
    expect([...COLOURS]).toEqual(SERVER);
  });

  it("accept only a palette name", () => {
    expect(isColour("teal")).toBe(true);
    expect(isColour("#ff0000")).toBe(false);
    expect(isColour("Teal")).toBe(false);
    expect(isColour("")).toBe(false);
    expect(isColour(undefined)).toBe(false);
  });

  it("are read from the stylesheet as a CSS variable of the same name", () => {
    expect(colourVar("red")).toBe("var(--colour-red)");
  });

  it("have a value in both themes, and each keeps 3:1 against its page (a non-text graphic)", () => {
    const { light, dark } = swatches();
    for (const c of SERVER) {
      expect(light[c], `light value of ${c}`).toBeTruthy();
      expect(dark[c], `dark value of ${c}`).toBeTruthy();
      expect(contrast(light[c], "#ffffff"), `light ${c} on white`).toBeGreaterThanOrEqual(3);
      expect(contrast(dark[c], PALETTE.dark.card), `dark ${c} on the card`).toBeGreaterThanOrEqual(3);
    }
  });
});

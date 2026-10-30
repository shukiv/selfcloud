// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { formatBytes } from "./format";

const LANGS = ["en", "es", "fr", "pt", "ru", "ar", "he"];

describe("formatBytes", () => {
  it("writes bytes in full in English, singular and plural", () => {
    expect(formatBytes(1, "en")).toBe("1 byte");
    expect(formatBytes(9, "en")).toBe("9 bytes");
    expect(formatBytes(2048, "en")).toBe("2 kB");
  });

  it("writes the byte unit in full, with its own plural, in every language", () => {
    for (const lang of LANGS) {
      for (const n of [1, 9]) {
        const full = new Intl.NumberFormat(lang, { style: "unit", unit: "byte", unitDisplay: "long" }).format(n);
        expect(formatBytes(n, lang), `${lang} ${n}`).toBe(full);
      }
    }
  });

  it("keeps the short unit for the larger sizes", () => {
    const short = new Intl.NumberFormat("en", { style: "unit", unit: "kilobyte", unitDisplay: "short", maximumFractionDigits: 1 }).format(1.5);
    expect(formatBytes(1500, "en")).toBe(short);
  });
});

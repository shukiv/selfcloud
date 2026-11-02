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
        // Hebrew and Arabic wrap the size in isolate marks (decision 67); the words inside stay the same.
        const marked = lang === "he" || lang === "ar" ? `\u2068${full}\u2069` : full;
        expect(formatBytes(n, lang), `${lang} ${n}`).toBe(marked);
      }
    }
  });

  it("keeps the short unit for the larger sizes", () => {
    const short = new Intl.NumberFormat("en", { style: "unit", unit: "kilobyte", unitDisplay: "short", maximumFractionDigits: 1 }).format(1.5);
    expect(formatBytes(1500, "en")).toBe(short);
  });

  // In Hebrew and Arabic a size sits inside a right-to-left sentence. Its own direction must hold (decision 67): the
  // isolate marks U+2068 and U+2069 wrap the whole size, and the other languages get no marks at all.
  it("isolates the size in a right-to-left language, so the unit stays after the digits", () => {
    expect(formatBytes(3_100_000, "he")).toBe("\u2068" + "3.1 MB" + "\u2069");
    expect(formatBytes(3_100_000, "ar")).toBe("\u2068" + "3.1 م.ب" + "\u2069");
  });

  // Hebrew's data writes exactly one of a short Latin unit unit-first ("MB", a right-to-left mark, "1"); inside the isolate that
  // drew as "MB1". A size is always its digits, then the unit.
  it("writes one kB, MB or GB digits first in Hebrew", () => {
    expect(formatBytes(1_000, "he")).toBe("⁨" + "1 kB" + "⁩");
    expect(formatBytes(1_000_000, "he")).toBe("⁨" + "1 MB" + "⁩");
    expect(formatBytes(1_000_000_000, "he")).toBe("⁨" + "1 GB" + "⁩");
    expect(formatBytes(1_000_000, "ar")).toBe("⁨" + "1 م.ب" + "⁩");
  });

  it("adds no direction marks in English, Spanish or Russian", () => {
    for (const lang of ["en", "es", "fr", "pt", "ru"]) {
      expect(formatBytes(3_100_000, lang), lang).not.toMatch(/[\u2066-\u2069]/);
    }
    expect(formatBytes(3_100_000, "en")).toBe("3.1 MB");
  });
});

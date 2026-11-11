// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { formatSeconds, formatSizeBytes } from "./format";

const MIB = 1024 ** 2;

describe("sizes in a sentence", () => {
  it("writes the number first, then the unit, in every language", () => {
    for (const lang of ["en", "es", "fr", "pt", "ru", "ar", "he"]) {
      const one = formatSizeBytes(MIB, lang);
      const many = formatSizeBytes(256 * MIB, lang);
      expect(one.replace(/[‎‏]/g, ""), lang).toMatch(/^1\s/);
      expect(many, lang).toMatch(/^256\s/);
    }
  });

  it("writes the Hebrew single megabyte as 1 MB, not MB1", () => {
    expect(formatSizeBytes(MIB, "he")).toBe("1 MB");
    expect(formatSizeBytes(1024 ** 4, "he")).toBe("1 TB");
  });

  it("keeps the English forms", () => {
    expect(formatSizeBytes(256 * MIB, "en")).toBe("256 MB");
    expect(formatSizeBytes(16 * 1024 ** 4, "en")).toBe("16 TB");
    expect(formatSeconds(30 * 86400, "en")).toBe("30 days");
  });
});

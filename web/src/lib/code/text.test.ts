// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { LANGUAGES } from "../i18n";
import { LONG_LINE, wraps } from "./lines";
import { CODE_TEXT, codeText, phrasesOf } from "./text";

describe("the code viewer's strings", () => {
  it("every language has every string, and none is empty", () => {
    const keys = Object.keys(CODE_TEXT.en).sort();
    for (const l of LANGUAGES) {
      expect(Object.keys(CODE_TEXT[l]).sort(), l).toEqual(keys);
      for (const [key, text] of Object.entries(CODE_TEXT[l])) expect(text.trim(), `${l} ${key}`).not.toBe("");
    }
  });

  it("gives CodeMirror the phrases of the read-only find panel and the go-to-line dialog", () => {
    expect(Object.keys(phrasesOf(codeText("he"))).sort()).toEqual(
      ["Find", "next", "previous", "all", "match case", "regexp", "by word", "close", "current match", "on line", "Go to line", "go"].sort(),
    );
    expect(phrasesOf(codeText("he")).Find).toBe("חיפוש");
  });
});

describe("the long-line rule", () => {
  it("wraps unless a line is longer than 2,000 characters", () => {
    expect(LONG_LINE).toBe(2000);
    expect(wraps("")).toBe(true);
    expect(wraps("a".repeat(2000))).toBe(true);
    expect(wraps("a".repeat(2001))).toBe(false);
    expect(wraps(`short\n${"a".repeat(2001)}\nshort`)).toBe(false);
    expect(wraps(`${"a".repeat(2000)}\r\n${"b".repeat(2000)}\r${"c".repeat(2000)}`)).toBe(true);
  });
});

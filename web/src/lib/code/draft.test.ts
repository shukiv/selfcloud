// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { decodeText } from "./encoding";
import { encodeDraft, lineBreakOf, mixedBreaks, normalizeText } from "./edit";

const bytes = (s: string) => new TextEncoder().encode(s);

// roundTrip reads a file's bytes as the editor does, then writes the text back as a save does.
function roundTrip(original: Uint8Array): Uint8Array {
  const d = decodeText(original, false);
  return encodeDraft(normalizeText(d.text), d.bom, lineBreakOf(d.text));
}

describe("mixedBreaks", () => {
  it("is false for one style of break, and for no break", () => {
    expect(mixedBreaks("a\nb\n")).toBe(false);
    expect(mixedBreaks("a\r\nb\r\n")).toBe(false);
    expect(mixedBreaks("a\rb\r")).toBe(false);
    expect(mixedBreaks("a b")).toBe(false);
  });

  it("is true when two styles of break are in the file", () => {
    expect(mixedBreaks("a\r\nb\nc\r\n")).toBe(true);
    expect(mixedBreaks("a\nb\rc")).toBe(true);
    expect(mixedBreaks("a\r\nb\rc")).toBe(true);
  });
});

describe("the save round trip keeps the file's bytes (design 4.8)", () => {
  it.each([
    ["LF", "a\nb\n"],
    ["CRLF", "a\r\nb\r\n"],
    ["CR only", "a\rb\r"],
    ["a byte order mark and CRLF", "﻿a\r\nb\r\n"],
    ["no final newline", "a\nb"],
    ["trailing whitespace kept", "a  \t\nb \n"],
    ["an empty file", ""],
  ])("%s comes back byte for byte", (_name, text) => {
    const original = bytes(text);
    expect([...roundTrip(original)]).toEqual([...original]);
  });

  it("a file with mixed breaks is written in its first style on every line", () => {
    expect(new TextDecoder().decode(roundTrip(bytes("a\r\nb\nc\r\n")))).toBe("a\r\nb\r\nc\r\n");
  });
});

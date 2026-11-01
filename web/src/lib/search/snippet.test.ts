// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { snippetParts } from "./snippet";

const bytes = (s: string) => new TextEncoder().encode(s).length;

describe("snippetParts", () => {
  it("marks the matches by byte offsets, so Hebrew is cut where the server cut it", () => {
    // "דוח " is three letters of two bytes and a space: 7 bytes, but 4 characters. The match starts at byte 7, not character 4.
    const text = "דוח שנתי 2026";
    const start = bytes("דוח ");
    expect(start).toBe(7);
    expect(start).not.toBe("דוח ".length);
    const parts = snippetParts({ text, ranges: [[start, start + bytes("שנתי")]] });
    expect(parts).toEqual([
      { text: "דוח ", hit: false },
      { text: "שנתי", hit: true },
      { text: " 2026", hit: false },
    ]);
  });

  it("handles a Hebrew range whose byte offset is far from its character offset", () => {
    const text = "הקובץ הזה: דוח שנתי של הבית";
    const at = bytes("הקובץ הזה: ");
    const parts = snippetParts({ text, ranges: [[at, at + bytes("דוח")]] });
    expect(parts.filter((p) => p.hit).map((p) => p.text)).toEqual(["דוח"]);
    expect(parts.map((p) => p.text).join("")).toBe(text);
  });

  it("drops a range that is out of bounds, empty, backwards or overlapping, and keeps the text", () => {
    const text = "alpha beta gamma";
    const parts = snippetParts({
      text,
      ranges: [
        [0, 99],
        [5, 5],
        [9, 4],
        [6, 10],
        [8, 12],
        [11, 16],
      ],
    });
    // "alpha beta gamma": beta is 6 to 10, gamma 11 to 16. The others are bad, or overlap beta.
    expect(parts.filter((p) => p.hit).map((p) => p.text)).toEqual(["beta", "gamma"]);
    expect(parts.map((p) => p.text).join("")).toBe(text);
  });

  it("returns the text as one piece when there is no range", () => {
    expect(snippetParts({ text: "plain", ranges: [] })).toEqual([{ text: "plain", hit: false }]);
    expect(snippetParts({ text: "", ranges: [] })).toEqual([]);
  });

  it("keeps markup as text: the parts are text, never parsed", () => {
    const text = "<img src=x onerror=alert(1)> report";
    const parts = snippetParts({ text, ranges: [[0, bytes("<img")]] });
    expect(parts.map((p) => p.text).join("")).toBe(text);
    expect(parts[0]).toEqual({ text: "<img", hit: true });
  });
});

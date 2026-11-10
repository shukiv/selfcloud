// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { translateLint } from "./lint";

// translateLint replaces the words of every problem the JSON parser reports with the one translated message (LOW-7). The place
// of each problem stays as the parser found it.
describe("translateLint", () => {
  it("gives the translated message at the reported position, with no engine text", () => {
    const found = [{ from: 4, to: 5, severity: "error", message: "Unexpected token 'x'" }];
    expect(translateLint(found, "Not valid JSON here.")).toEqual([
      { from: 4, to: 5, severity: "error", message: "Not valid JSON here." },
    ]);
  });

  it("translates every problem, and keeps each one's severity and place", () => {
    const found = [
      { from: 0, to: 1, severity: "error", message: "Expected ']'" },
      { from: 9, to: 12, severity: "error", message: "Unexpected end of input" },
    ];
    const got = translateLint(found, "Not valid JSON here.");
    expect(got.map((d) => d.message)).toEqual(["Not valid JSON here.", "Not valid JSON here."]);
    expect(got.map((d) => [d.from, d.to, d.severity])).toEqual([
      [0, 1, "error"],
      [9, 12, "error"],
    ]);
  });

  it("leaves no problems as no problems", () => {
    expect(translateLint([], "Not valid JSON here.")).toEqual([]);
  });
});

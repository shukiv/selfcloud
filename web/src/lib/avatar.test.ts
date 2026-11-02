// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { accountLabel, initialOf } from "./avatar";

describe("initialOf (decision 68, the account avatar)", () => {
  it("is the first letter of the name", () => {
    expect(initialOf("Olivia Tan", "olivia@example.com")).toBe("O");
  });
  it("is the first letter of the email when there is no name", () => {
    expect(initialOf("", "shuki@example.com")).toBe("S");
    expect(initialOf(null, "shuki@example.com")).toBe("S");
  });
  it("is empty when neither is set", () => {
    expect(initialOf(undefined, "")).toBe("");
    expect(initialOf("   ", null)).toBe("");
  });
  it("takes a whole code point, so a Hebrew letter is one", () => {
    expect(initialOf("שירה", "x@example.com")).toBe("ש");
  });

  // Review fix (decision 68): the letter is the first grapheme, uppercased for display where that is one character.
  it("uppercases a Latin or Cyrillic first letter, and keeps a letter whose capital is two characters (German sharp s)", () => {
    expect(initialOf("olivia", "")).toBe("O");
    expect(initialOf("жанна", "")).toBe("Ж");
    expect(initialOf("ßa", "")).toBe("ß");
  });
  it("keeps a non-Latin first letter as it is (Hebrew, Arabic)", () => {
    expect(initialOf("שירה", "")).toBe("ש");
    expect(initialOf("عائشة", "")).toBe("ع");
  });
  it("takes a whole emoji as the first character", () => {
    expect(initialOf("😀 Olivia", "")).toBe("😀");
    expect(initialOf("👩🏽‍💻 Dev", "")).toBe("👩🏽‍💻");
  });
  it("keeps a combining mark with its letter, and uppercases the letter", () => {
    expect(initialOf("e\u0301tienne", "")).toBe("E\u0301");
  });
  it("takes the email's first letter when the name is blank, and ignores spaces around the name", () => {
    expect(initialOf("  ", "  zoe@example.com")).toBe("Z");
    expect(initialOf("  Ada  ", "x@example.com")).toBe("A");
  });
});

// accountLabel is the account button's name and tooltip: the name, else the email, else "Account menu" (decision 68).
describe("accountLabel (decision 68, the account button's name)", () => {
  it("is the name when there is one, the email when there is no name, and the fallback when neither", () => {
    expect(accountLabel("Olivia Tan", "olivia@example.com", "Account menu")).toBe("Olivia Tan");
    expect(accountLabel("", "olivia@example.com", "Account menu")).toBe("olivia@example.com");
    expect(accountLabel("   ", "  ", "Account menu")).toBe("Account menu");
    expect(accountLabel(undefined, null, "Account menu")).toBe("Account menu");
  });
});

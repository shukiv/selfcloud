// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { readGlobalKey, SHORTCUT_GROUPS, type GlobalKeyFields } from "./shortcuts";
import en from "../i18n/shortcuts/en.json";

// A key press as the browser reports it: key is what the layout typed, code is the key's position.
function press(extra: Partial<GlobalKeyFields> = {}): GlobalKeyFields {
  return { key: "", code: "", ctrlKey: false, metaKey: false, altKey: false, ...extra };
}
const ctx = { search: true, shortcutsOpen: false };

describe("the global key reader (ks1)", () => {
  it("'/' asks to focus the search box", () => {
    expect(readGlobalKey(press({ key: "/" }), false, ctx)).toBe("search");
  });

  it("'/' with Ctrl or Meta is the list's toggle, not the search key", () => {
    expect(readGlobalKey(press({ key: "/", ctrlKey: true }), false, ctx)).toBe("shortcutsToggle");
    expect(readGlobalKey(press({ key: "/", metaKey: true }), false, ctx)).toBe("shortcutsToggle");
  });

  it("'/' with Alt alone is nothing (AltGr's slash types a character)", () => {
    expect(readGlobalKey(press({ key: "/", altKey: true }), false, ctx)).toBeNull();
    expect(readGlobalKey(press({ key: "/", ctrlKey: true, altKey: true }), false, ctx)).toBeNull();
  });

  it("Ctrl with the Slash position opens the list even when the layout types another character", () => {
    expect(readGlobalKey(press({ key: "ץ", code: "Slash", ctrlKey: true }), false, ctx)).toBe("shortcutsToggle");
    expect(readGlobalKey(press({ key: "-", code: "Slash", metaKey: true }), false, ctx)).toBe("shortcutsToggle");
  });

  it("'?' asks to show the list", () => {
    expect(readGlobalKey(press({ key: "?" }), false, ctx)).toBe("shortcuts");
  });

  it("a key the reader does not answer is nothing", () => {
    expect(readGlobalKey(press({ key: "a" }), false, ctx)).toBeNull();
    expect(readGlobalKey(press({ key: "Enter" }), false, ctx)).toBeNull();
    expect(readGlobalKey(press({ key: "/", code: "Slash" }), false, { search: false, shortcutsOpen: false })).toBeNull();
  });

  it("a blocked key (a text field or an open overlay) does nothing", () => {
    expect(readGlobalKey(press({ key: "/" }), true, ctx)).toBeNull();
    expect(readGlobalKey(press({ key: "?" }), true, ctx)).toBeNull();
    expect(readGlobalKey(press({ key: "/", ctrlKey: true }), true, ctx)).toBeNull();
  });

  it("Ctrl+/ still closes the list while the list itself is the open overlay", () => {
    expect(readGlobalKey(press({ key: "/", ctrlKey: true }), true, { search: true, shortcutsOpen: true })).toBe("shortcutsToggle");
    // '?' does not toggle: while the list is open it is left alone.
    expect(readGlobalKey(press({ key: "?" }), true, { search: true, shortcutsOpen: true })).toBeNull();
  });

  it("an admin has no search box, so '/' is nothing, but the list still opens", () => {
    const admin = { search: false, shortcutsOpen: false };
    expect(readGlobalKey(press({ key: "/" }), false, admin)).toBeNull();
    expect(readGlobalKey(press({ key: "?" }), false, admin)).toBe("shortcuts");
    expect(readGlobalKey(press({ key: "/", ctrlKey: true }), false, admin)).toBe("shortcutsToggle");
  });
});

describe("the shortcut table (ks1)", () => {
  it("has the six groups in order", () => {
    expect(SHORTCUT_GROUPS.map((g) => g.title)).toEqual([
      "shortcuts.group.general",
      "shortcuts.group.move",
      "shortcuts.group.selection",
      "shortcuts.group.files",
      "shortcuts.group.new",
      "shortcuts.group.media",
    ]);
  });

  it("names no row twice (the same keys with the same label)", () => {
    const seen = new Set<string>();
    for (const g of SHORTCUT_GROUPS) {
      for (const r of g.rows) {
        const sig = JSON.stringify([r.keys, r.then ?? null, r.label]);
        expect(seen.has(sig), `${g.title}: ${r.label}`).toBe(false);
        seen.add(sig);
      }
    }
  });

  it("gives every row at least one key and a label", () => {
    for (const g of SHORTCUT_GROUPS) {
      expect(g.rows.length).toBeGreaterThan(0);
      for (const r of g.rows) {
        expect(r.keys.length).toBeGreaterThan(0);
        for (const alt of r.keys) expect(alt.length).toBeGreaterThan(0);
        expect(r.label).toMatch(/^shortcuts\./);
      }
    }
  });

  it("uses only catalogue keys that the English namespace has", () => {
    const strings = en as Record<string, string>;
    const want: string[] = ["shortcuts.title", "shortcuts.or", "shortcuts.then"];
    for (const g of SHORTCUT_GROUPS) {
      want.push(g.title);
      if (g.note) want.push(g.note);
      for (const r of g.rows) want.push(r.label);
    }
    for (const key of want) expect(strings[key], key).toBeTruthy();
  });

  it("keeps the Mac-only trash row apart from the plain Delete row", () => {
    const files = SHORTCUT_GROUPS.find((g) => g.title === "shortcuts.group.files");
    const trash = files?.rows.filter((r) => r.label === "shortcuts.trash") ?? [];
    expect(trash.length).toBe(2);
    expect(trash.some((r) => r.macOnly)).toBe(true);
    expect(trash.some((r) => !r.macOnly)).toBe(true);
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// The Media page's catalogue is a namespace of its own (lib/media/i18n.ts), read outside the main one, so the checks of
// check-keys.mjs do not reach it. These tests do: every language has the keys of English, and every literal key the page
// uses has a text. The sidebar's one new key (nav.media) is in the main catalogues, checked here too. The tests run from
// the web directory.
const root = resolve(process.cwd(), "src");
const read = (path: string) => JSON.parse(readFileSync(path, "utf8")) as Record<string, string>;
const en = read(join(root, "i18n", "media", "en.json"));

describe("the media catalogue", () => {
  const langs = ["es", "fr", "pt", "ru", "ar", "he"];

  it.each(langs)("%s has exactly the keys English has", (l) => {
    const other = read(join(root, "i18n", "media", `${l}.json`));
    expect(Object.keys(other).sort()).toEqual(Object.keys(en).sort());
  });

  it.each(langs)("%s has a text for every key, and none is empty", (l) => {
    const other = read(join(root, "i18n", "media", `${l}.json`));
    for (const [k, v] of Object.entries(other)) expect(v.trim(), k).not.toBe("");
  });

  it("uses no plural forms", () => {
    expect(Object.keys(en).filter((k) => /_(one|few|many|other|zero|two)$/.test(k))).toEqual([]);
  });

  it("every literal media key the page uses has a text", () => {
    const text = readFileSync(join(root, "pages", "MediaPage.tsx"), "utf8");
    const used = new Set<string>();
    for (const m of text.matchAll(/["'`](media\.[A-Za-z.]+)["'`]/g)) used.add(m[1]!);
    expect(used.size).toBeGreaterThan(3);
    expect([...used].filter((k) => !(k in en))).toEqual([]);
  });

  it("the sidebar's Media entry is in every main catalogue", () => {
    for (const l of ["en", ...langs]) {
      const main = read(join(root, "i18n", `${l}.json`));
      expect(typeof main["nav.media"], l).toBe("string");
      expect(main["nav.media"]!.trim(), l).not.toBe("");
    }
  });
});

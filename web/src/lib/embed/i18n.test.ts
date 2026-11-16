// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// The frame views' catalogue is a namespace of its own (lib/embed/i18n.ts), read outside the main one, so the
// checks of check-keys.mjs do not reach it. These tests do: every language has the keys of English, none empty,
// and every literal key the views use has a text. The main catalogues gain nothing (the Russian one is at its
// budget). The tests run from the web directory.
const root = resolve(process.cwd(), "src");
const read = (path: string) => JSON.parse(readFileSync(path, "utf8")) as Record<string, string>;
const en = read(join(root, "i18n", "embed", "en.json"));

describe("the embed catalogue", () => {
  const langs = ["es", "fr", "pt", "ru", "ar", "he"];

  it.each(langs)("%s has exactly the keys English has", (l) => {
    const other = read(join(root, "i18n", "embed", `${l}.json`));
    expect(Object.keys(other).sort()).toEqual(Object.keys(en).sort());
  });

  it.each(langs)("%s has a text for every key, and none is empty", (l) => {
    const other = read(join(root, "i18n", "embed", `${l}.json`));
    for (const [k, v] of Object.entries(other)) expect(v.trim(), k).not.toBe("");
  });

  it("uses no plural forms", () => {
    expect(Object.keys(en).filter((k) => /_(one|few|many|other|zero|two)$/.test(k))).toEqual([]);
  });

  it("no title ends in a full stop", () => {
    for (const l of ["en", ...langs]) {
      const cat = read(join(root, "i18n", "embed", `${l}.json`));
      for (const [k, v] of Object.entries(cat)) {
        if (k.endsWith(".title")) expect(v.endsWith("."), `${l} ${k}`).toBe(false);
      }
    }
  });

  it("every literal embed key the views use has a text", () => {
    const used = new Set<string>();
    const files = [
      join(root, "components", "FrameEnded.tsx"),
      join(root, "components", "FrameBlocked.tsx"),
      join(root, "pages", "manage", "EmbeddingPage.tsx"),
    ];
    for (const path of files) {
      const text = readFileSync(path, "utf8");
      for (const m of text.matchAll(/["'`](embed\.[A-Za-z.]+)["'`]/g)) used.add(m[1]!);
    }
    expect(used.size).toBeGreaterThan(3);
    expect([...used].filter((k) => !(k in en))).toEqual([]);
  });

  it("the main catalogues gain no embed key", () => {
    for (const l of ["en", ...langs]) {
      const main = read(join(root, "i18n", `${l}.json`));
      expect(Object.keys(main).filter((k) => k.startsWith("embed.")), l).toEqual([]);
    }
  });
});

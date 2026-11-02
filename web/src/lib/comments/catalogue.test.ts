// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// The comments catalogue is read outside the main one, so the checks of check-keys.mjs do not reach it. These tests do:
// every language has the same keys as English, and every literal key the comment panels use has a text.
// The tests run from the web directory, as the palette test does.
const root = resolve(process.cwd(), "src");
const read = (path: string) => JSON.parse(readFileSync(path, "utf8")) as Record<string, string>;
const en = read(join(root, "i18n", "comments", "en.json"));
const common = read(join(root, "i18n", "en.json"));

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return sourceFiles(p);
    return /\.tsx?$/.test(e.name) && !/\.test\./.test(e.name) ? [p] : [];
  });
}

describe("the comments catalogue", () => {
  const langs = ["es", "fr", "pt", "ru", "ar", "he"];

  it.each(langs)("%s has exactly the keys English has", (l) => {
    const other = read(join(root, "i18n", "comments", `${l}.json`));
    expect(Object.keys(other).sort()).toEqual(Object.keys(en).sort());
  });

  it.each(langs)("%s has a text for every key, and none is empty", (l) => {
    const other = read(join(root, "i18n", "comments", `${l}.json`));
    for (const [k, v] of Object.entries(other)) expect(v.trim(), k).not.toBe("");
  });

  it("uses no plural forms, which the seven languages would need in full", () => {
    expect(Object.keys(en).filter((k) => /_(one|few|many|other|zero|two)$/.test(k))).toEqual([]);
  });

  it("every literal key the panels use is in this catalogue or in the main one", () => {
    const dirs = [join(root, "components", "comments"), join(root, "components", "files")];
    const used = new Set<string>();
    for (const dir of dirs) {
      for (const file of sourceFiles(dir)) {
        const text = readFileSync(file, "utf8");
        for (const m of text.matchAll(/\b(?:t|ct)\(\s*["']([a-zA-Z]+\.[A-Za-z.]+)["']/g)) used.add(m[1]);
      }
    }
    expect(used.size).toBeGreaterThan(20);
    const missing = [...used].filter((k) => !(k in en) && !(k in common));
    expect(missing).toEqual([]);
  });

  it("no key of this catalogue is left unused by the panels", () => {
    const used = new Set<string>();
    for (const file of sourceFiles(join(root, "components", "comments"))) {
      const text = readFileSync(file, "utf8");
      for (const m of text.matchAll(/\b(?:t|ct)\(\s*["']([a-zA-Z]+\.[A-Za-z.]+)["']/g)) used.add(m[1]);
      // A key may be named in a failure helper as a plain string.
      for (const m of text.matchAll(/["']((?:comments|activity|viewers|settings)\.[A-Za-z]+)["']/g)) used.add(m[1]);
    }
    // The rules of the body name their texts from logic.ts, which returns the key to show.
    for (const m of readFileSync(join(root, "lib", "comments", "logic.ts"), "utf8").matchAll(/["']((?:comments|activity|viewers|settings)\.[A-Za-z]+)["']/g)) used.add(m[1]);
    const unused = Object.keys(en).filter((k) => !used.has(k));
    expect(unused).toEqual([]);
  });
});

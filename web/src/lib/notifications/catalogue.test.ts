// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// The notifications catalogue is a namespace of its own, read outside the main one, so the checks of check-keys.mjs do
// not reach it. These tests do: every language has the keys of English, and every literal key the bell, the page and the
// logic use has a text. The tests run from the web directory.
const root = resolve(process.cwd(), "src");
const read = (path: string) => JSON.parse(readFileSync(path, "utf8")) as Record<string, string>;
const en = read(join(root, "i18n", "notifications", "en.json"));

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return sourceFiles(p);
    return /\.tsx?$/.test(e.name) && !/\.test\./.test(e.name) ? [p] : [];
  });
}

describe("the notifications catalogue", () => {
  const langs = ["es", "fr", "pt", "ru", "ar", "he"];

  it.each(langs)("%s has exactly the keys English has", (l) => {
    const other = read(join(root, "i18n", "notifications", `${l}.json`));
    expect(Object.keys(other).sort()).toEqual(Object.keys(en).sort());
  });

  it.each(langs)("%s has a text for every key, and none is empty", (l) => {
    const other = read(join(root, "i18n", "notifications", `${l}.json`));
    for (const [k, v] of Object.entries(other)) expect(v.trim(), k).not.toBe("");
  });

  it("uses no plural forms", () => {
    expect(Object.keys(en).filter((k) => /_(one|few|many|other|zero|two)$/.test(k))).toEqual([]);
  });

  it("every literal notifications key the bell, the page and the logic use has a text", () => {
    const files = [
      ...sourceFiles(join(root, "components", "notifications")),
      ...sourceFiles(join(root, "lib", "notifications")),
      join(root, "pages", "NotificationsPage.tsx"),
    ];
    const used = new Set<string>();
    for (const f of files) {
      for (const m of readFileSync(f, "utf8").matchAll(/["'`](notifications\.[A-Za-z]+)["'`]/g)) used.add(m[1]!);
    }
    expect(used.size).toBeGreaterThan(5);
    expect([...used].filter((k) => !(k in en))).toEqual([]);
  });
});

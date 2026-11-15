// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync, readdirSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";
import { refText } from "./i18n";

// The activity log's sentences are a namespace of their own (lib/audit/i18n.ts), read outside the main one, so the checks of
// check-keys.mjs do not reach them on their own. These tests do: every language has the keys of English, none is empty, no
// plural forms, no audit key is left in a main catalogue, and every literal audit key the source uses has a text. The tests
// run from the web directory, as the media catalogue's do.
const root = resolve(process.cwd(), "src");
const read = (path: string) => JSON.parse(readFileSync(path, "utf8")) as Record<string, string>;
const en = read(join(root, "i18n", "audit", "en.json"));

function sourceFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name === "i18n" ? [] : sourceFiles(p);
    return /\.tsx?$/.test(e.name) && !/\.test\./.test(e.name) ? [p] : [];
  });
}

describe("the audit catalogue", () => {
  const langs = ["es", "fr", "pt", "ru", "ar", "he"];

  it.each(langs)("%s has exactly the keys English has", (l) => {
    const other = read(join(root, "i18n", "audit", `${l}.json`));
    expect(Object.keys(other).sort()).toEqual(Object.keys(en).sort());
  });

  it.each(langs)("%s has a text for every key, and none is empty", (l) => {
    const other = read(join(root, "i18n", "audit", `${l}.json`));
    for (const [k, v] of Object.entries(other)) expect(v.trim(), k).not.toBe("");
  });

  it("uses no plural forms", () => {
    expect(Object.keys(en).filter((k) => /_(one|few|many|other|zero|two)$/.test(k))).toEqual([]);
  });

  it("no main catalogue holds an audit key", () => {
    for (const l of ["en", ...langs]) {
      const main = read(join(root, "i18n", `${l}.json`));
      expect(Object.keys(main).filter((k) => k.startsWith("audit.")), l).toEqual([]);
    }
  });

  it("every literal audit key the source uses has a text", () => {
    const used = new Set<string>();
    for (const file of sourceFiles(root)) {
      const text = readFileSync(file, "utf8");
      for (const m of text.matchAll(/["'`](audit\.[A-Za-z0-9_.]+)["'`]/g)) used.add(m[1]!);
    }
    expect(used.size).toBeGreaterThan(100);
    expect([...used].filter((k) => !(k in en))).toEqual([]);
  });
});

describe("refText", () => {
  const t = (key: string) => `main:${key}`;
  const at = (key: string) => `audit:${key}`;

  it("gives the text of a text ref unchanged", () => {
    expect(refText({ text: "Ann" }, t, at)).toBe("Ann");
  });

  it("reads an audit key from the audit catalogue", () => {
    expect(refText({ key: "audit.someone" }, t, at)).toBe("audit:audit.someone");
  });

  it("reads any other key from the main catalogue", () => {
    expect(refText({ key: "people.role.owner" }, t, at)).toBe("main:people.role.owner");
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

// An empty table's title is a phrase with no full stop at its end, as the approved mockup's "No labels yet" (decision 114).
// The keys are read from the source: each preset Listing's empty prop names its title with t()
// (a filtered list may choose between two), and each key's text in the seven catalogues must not end in "." or in the
// ideographic full stop "。" (U+3002).
const src = join(process.cwd(), "src");
const LANGS = ["en", "es", "fr", "pt", "ru", "ar", "he"];

function titleKeys(): Map<string, string> {
  const keys = new Map<string, string>();
  const files = (readdirSync(src, { recursive: true }) as string[]).filter((f) => f.endsWith(".tsx") && !f.endsWith(".test.tsx"));
  for (const file of files) {
    const text = readFileSync(join(src, file), "utf8");
    for (const block of text.matchAll(/empty=\{\{([\s\S]*?)\}\}/g)) {
      const title = /title:\s*([^,\n]*)/.exec(block[1]);
      if (!title) continue;
      for (const k of title[1].matchAll(/\bt\("([^"]+)"\)/g)) keys.set(k[1], file);
    }
  }
  return keys;
}

describe("empty table titles", () => {
  const keys = titleKeys();

  it("are found in the source: the five admin lists' titles at least", () => {
    expect([...keys.keys()]).toEqual(expect.arrayContaining(["tenants.empty", "tenants.noMatch", "tenant.users.empty", "keys.empty", "audit.empty"]));
  });

  it("are found in the source: the three lists on the one table in tl5 (decision 138)", () => {
    expect([...keys.keys()]).toEqual(expect.arrayContaining(["requests.emptyTitle", "notifications.emptyTitle", "activity.emptyTitle"]));
  });

  it.each(LANGS)("end with no full stop in %s", (lang) => {
    // The command-line strings (devices.empty) are a catalogue of their own (lib/cli/cliText.ts), read with the main one.
    const catalogue = {
      ...(JSON.parse(readFileSync(join(src, "i18n", `${lang}.json`), "utf8")) as Record<string, string>),
      ...(JSON.parse(readFileSync(join(src, "i18n", "cli", `${lang}.json`), "utf8")) as Record<string, string>),
      // The drive lists' empty titles are a catalogue of their own too (lib/drive/driveText.ts, tl4).
      ...(JSON.parse(readFileSync(join(src, "i18n", "drive", `${lang}.json`), "utf8")) as Record<string, string>),
      // The three lists of tl5 read their titles from their own catalogues too (decision 138).
      ...(JSON.parse(readFileSync(join(src, "i18n", "requests", `${lang}.json`), "utf8")) as Record<string, string>),
      ...(JSON.parse(readFileSync(join(src, "i18n", "notifications", `${lang}.json`), "utf8")) as Record<string, string>),
      ...(JSON.parse(readFileSync(join(src, "i18n", "comments", `${lang}.json`), "utf8")) as Record<string, string>),
    };
    const stopped = [...keys.keys()].filter((k) => {
      const v = catalogue[k];
      expect(v, `${k} in ${lang}`).toBeTypeOf("string");
      return /[.。]$/.test(v.trim());
    });
    expect(stopped.map((k) => `${k}: ${catalogue[k]}`)).toEqual([]);
  });
});

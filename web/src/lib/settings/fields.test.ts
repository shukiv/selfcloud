// SPDX-License-Identifier: AGPL-3.0-or-later

import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";
import en from "../../i18n/admin/en.json";
import { fieldKeys, SECTIONS } from "./fields";

const text = en as Record<string, string>;

describe("the settings page's fields", () => {
  const all = SECTIONS.flatMap((s) => fieldKeys(s));

  it("draws every setting the server lists, once, and no other", () => {
    const go = readFileSync(resolve(__dirname, "../../../../internal/config/settings.go"), "utf8");
    const block = go.slice(go.indexOf("var settingDefs"), go.indexOf("// factKeys"));
    const served = [...block.matchAll(/\{key: "([a-z_.]+)"/g)].map((m) => m[1]).sort();
    expect(served.length).toBeGreaterThan(20);
    expect([...all].sort()).toEqual(served);
    expect(new Set(all).size).toBe(all.length);
  });

  it("has English text for every label, help line, note, group and section", () => {
    const missing: string[] = [];
    const need = (k: string) => {
      if (typeof text[k] !== "string" || text[k] === "") missing.push(k);
    };
    for (const s of SECTIONS) {
      need(`settings.section.${s.id}`);
      need(`settings.section.${s.id}.intro`);
      for (const g of s.groups) {
        if (g.title !== null) need(g.title);
        for (const f of g.fields) {
          need(`settings.field.${f.key}.label`);
          need(`settings.field.${f.key}.help`);
          for (const n of f.notes ?? []) need(n);
        }
      }
    }
    need("settings.section.about");
    need("settings.section.about.intro");
    expect(missing).toEqual([]);
  });

  it("states a range with a floor below its cap", () => {
    for (const s of SECTIONS) for (const g of s.groups) for (const f of g.fields) if (f.range) expect(f.range.min).toBeLessThan(f.range.max);
  });
});

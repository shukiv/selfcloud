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

  it("states each range as the server holds it: the same floor, cap and zero", () => {
    const go = readFileSync(resolve(__dirname, "../../../../internal/config/settings.go"), "utf8");
    const block = go.slice(go.indexOf("var settingDefs"), go.indexOf("// factKeys"));
    const unit = { minute: 60e9, hour: 3600e9, day: 86400e9, KiB: 1024, MiB: 1024 ** 2, GiB: 1024 ** 3, TiB: 1024 ** 4 };
    const evaluate = (expr: string): number => {
      const text = expr.replace(/int64\(/g, "(").trim();
      if (!/^[\w\s*()+]+$/.test(text)) throw new Error(`cannot read ${expr}`);
      return Function(...Object.keys(unit), `return (${text});`)(...Object.values(unit)) as number;
    };
    const specs = new Map(SECTIONS.flatMap((s) => s.groups.flatMap((g) => g.fields)).map((f) => [f.key, f]));
    let compared = 0;
    for (const m of block.matchAll(/\{key: "([a-z_.]+)", typ: Type(\w+)([^}\n]*)\}/g)) {
      const [, key, typ, rest] = m;
      const min = /min: ([^,}]+?)(?:,|$)/.exec(rest)?.[1];
      const max = /max: ([^,}]+?)(?:,|$)/.exec(rest)?.[1];
      const zero = /zero: true/.test(rest);
      const spec = specs.get(key);
      expect(spec, key).toBeDefined();
      if (min === undefined || max === undefined) {
        expect(spec?.range, `${key} has no range`).toBeUndefined();
        continue;
      }
      const scale = typ === "Duration" ? 1e9 : 1;
      expect(spec?.range, key).toEqual({ min: evaluate(min) / scale, max: evaluate(max) / scale, ...(zero ? { zero: true } : {}) });
      compared++;
    }
    expect(compared).toBeGreaterThan(15);
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

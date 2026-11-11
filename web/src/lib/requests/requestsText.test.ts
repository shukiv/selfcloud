// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import en from "../../i18n/requests/en.json";
import ar from "../../i18n/requests/ar.json";
import es from "../../i18n/requests/es.json";
import fr from "../../i18n/requests/fr.json";
import he from "../../i18n/requests/he.json";
import pt from "../../i18n/requests/pt.json";
import ru from "../../i18n/requests/ru.json";

// The access requests page's own catalogue (lib/requests/text.ts, tl5, decision 138): the column titles and the empty table's title,
// which the page loads so the main catalogues do not grow. Every language has the keys English has, each with text.
describe("the access requests catalogue", () => {
  const all = { ar, es, fr, he, pt, ru };

  it("names the column titles and the empty title", () => {
    expect(Object.keys(en).sort()).toEqual(
      ["requests.colAsks", "requests.colItem", "requests.colMessage", "requests.colWho", "requests.emptyTitle"].sort(),
    );
  });

  it("gives no empty title a full stop", () => {
    expect(en["requests.emptyTitle"]).not.toMatch(/[.。]$/);
  });

  for (const [lang, cat] of Object.entries(all)) {
    it(`${lang} has the same keys as English, each with text`, () => {
      expect(Object.keys(cat).sort()).toEqual(Object.keys(en).sort());
      for (const v of Object.values(cat)) expect(v.trim()).not.toBe("");
    });
  }
});

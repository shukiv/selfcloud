// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import en from "../../i18n/drive/en.json";
import ar from "../../i18n/drive/ar.json";
import es from "../../i18n/drive/es.json";
import fr from "../../i18n/drive/fr.json";
import he from "../../i18n/drive/he.json";
import pt from "../../i18n/drive/pt.json";
import ru from "../../i18n/drive/ru.json";

// The drive lists' own catalogue (lib/drive/driveText.ts): every language has the keys English has, and each value is text.
describe("the drive lists' catalogues", () => {
  const all = { ar, es, fr, he, pt, ru };
  it("names the new column titles", () => {
    expect(Object.keys(en).sort()).toEqual(
      [
        "drive.col.deletedOn",
        "drive.col.location",
        "drive.col.opened",
        "drive.col.originalLocation",
        "drive.col.sharedBy",
        "drive.col.sharedOn",
        "drive.col.starredOn",
        "drive.col.yourRole",
        "drive.empty.recent",
        "drive.empty.search",
        "drive.empty.searchPage",
        "drive.empty.shared",
        "drive.empty.starred",
        "drive.empty.trash",
      ].sort(),
    );
  });
  for (const [lang, cat] of Object.entries(all)) {
    it(`${lang} has the same keys as English, each with text`, () => {
      expect(Object.keys(cat).sort()).toEqual(Object.keys(en).sort());
      for (const v of Object.values(cat)) expect(v.trim()).not.toBe("");
    });
  }
});

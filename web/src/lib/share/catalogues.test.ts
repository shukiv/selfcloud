// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page's strings are their own catalogue (decision 82): web/src/i18n/share/<lang>.json, loaded with the share
// page, so the main catalogues (which every page loads) do not grow with them. These tests hold the catalogues to the same shape as
// the search catalogue: every language has every key and nothing more, no text is empty, each placeholder is the one the English
// text uses, and the main catalogues no longer carry the moved keys.

import { describe, expect, it } from "vitest";
import en from "../../i18n/share/en.json";
import ar from "../../i18n/share/ar.json";
import es from "../../i18n/share/es.json";
import fr from "../../i18n/share/fr.json";
import he from "../../i18n/share/he.json";
import pt from "../../i18n/share/pt.json";
import ru from "../../i18n/share/ru.json";
import mainEn from "../../i18n/en.json";
import mainAr from "../../i18n/ar.json";
import mainEs from "../../i18n/es.json";
import mainFr from "../../i18n/fr.json";
import mainHe from "../../i18n/he.json";
import mainPt from "../../i18n/pt.json";
import mainRu from "../../i18n/ru.json";

const SHARE: Record<string, Record<string, string>> = { en, ar, es, fr, he, pt, ru };
const MAIN: Record<string, Record<string, string>> = { en: mainEn, ar: mainAr, es: mainEs, fr: mainFr, he: mainHe, pt: mainPt, ru: mainRu };

// The placeholders a sentence takes, as the English text names them, such as {count}.
const placeholders = (s: string) => [...s.matchAll(/\{([a-z]+)\}/g)].map((m) => m[1]).sort();

describe("the share catalogues", () => {
  it("every language has every share key, and nothing more", () => {
    const keys = Object.keys(en).sort();
    for (const [lang, cat] of Object.entries(SHARE)) {
      expect(Object.keys(cat).sort(), lang).toEqual(keys);
    }
  });

  it("every share key is a shareTarget key, as the page reads them", () => {
    for (const key of Object.keys(en)) expect(key.startsWith("shareTarget."), key).toBe(true);
  });

  it("no text is empty, and each placeholder is the one the English text uses", () => {
    for (const [lang, cat] of Object.entries(SHARE)) {
      for (const key of Object.keys(en)) {
        const text = cat[key];
        expect(text.trim(), `${lang} ${key}`).not.toBe("");
        expect(placeholders(text), `${lang} ${key}`).toEqual(placeholders(en[key as keyof typeof en]));
      }
    }
  });

  it("the main catalogues no longer carry the share keys, so the main chunk does not grow with them", () => {
    for (const [lang, cat] of Object.entries(MAIN)) {
      expect(Object.keys(cat).filter((k) => k.startsWith("shareTarget.")), lang).toEqual([]);
    }
  });

  it("the keys the files page needs for the save mode of the chooser stay in the main catalogues", () => {
    for (const [lang, cat] of Object.entries(MAIN)) {
      expect(cat["files.pick.save"], lang).toBeTruthy();
      expect(cat["files.pick.submitSave"], lang).toBeTruthy();
    }
  });
});

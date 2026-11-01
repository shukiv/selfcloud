// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import en from "../../i18n/search/en.json";
import ar from "../../i18n/search/ar.json";
import es from "../../i18n/search/es.json";
import fr from "../../i18n/search/fr.json";
import he from "../../i18n/search/he.json";
import pt from "../../i18n/search/pt.json";
import ru from "../../i18n/search/ru.json";
import mainEn from "../../i18n/en.json";
import mainAr from "../../i18n/ar.json";
import mainEs from "../../i18n/es.json";
import mainFr from "../../i18n/fr.json";
import mainHe from "../../i18n/he.json";
import mainPt from "../../i18n/pt.json";
import mainRu from "../../i18n/ru.json";

const SEARCH: Record<string, Record<string, string>> = { en, ar, es, fr, he, pt, ru };
const MAIN: Record<string, Record<string, string>> = { en: mainEn, ar: mainAr, es: mainEs, fr: mainFr, he: mainHe, pt: mainPt, ru: mainRu };

// The placeholders a sentence takes, as the English text names them: {label}, {value}, {name}, {date}.
const placeholders = (s: string) => [...s.matchAll(/\{([a-z]+)\}/g)].map((m) => m[1]).sort();

describe("the search catalogues", () => {
  it("every language has every search key, and nothing more", () => {
    const keys = Object.keys(en).sort();
    for (const [lang, cat] of Object.entries(SEARCH)) {
      expect(Object.keys(cat).sort(), lang).toEqual(keys);
    }
  });

  it("no text is empty, and each placeholder is the one the English text uses", () => {
    for (const [lang, cat] of Object.entries(SEARCH)) {
      for (const key of Object.keys(en)) {
        const text = cat[key];
        expect(text.trim(), `${lang} ${key}`).not.toBe("");
        expect(placeholders(text), `${lang} ${key}`).toEqual(placeholders(en[key as keyof typeof en]));
      }
    }
  });

  it("the shell's two strings are in every main catalogue, so the box shows before the page loads", () => {
    for (const [lang, cat] of Object.entries(MAIN)) {
      expect(cat["shell.search.placeholder"], lang).toBeTruthy();
      expect(cat["shell.search.open"], lang).toBeTruthy();
    }
  });

  it("the placeholder of the box is the one the mockup uses, in English", () => {
    expect(mainEn["shell.search.placeholder"]).toBe("Search names and text in files");
    expect(en["search.placeholder"]).toBe("Search names and text in files");
  });
});

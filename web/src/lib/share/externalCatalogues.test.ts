// SPDX-License-Identifier: AGPL-3.0-or-later

// The words of sharing with people in other accounts are their own catalogue (decision 187): web/src/i18n/external/<lang>.json,
// loaded with the pages that show them, so the main catalogues (which every page loads) do not grow with them. These tests hold
// the catalogues to the same shape as the share catalogue: every language has every key and nothing more, no text is empty, each
// placeholder is the one the English text uses, and no key is in both the main and the external catalogue. The audit words of
// external sharing stay in the main catalogues with the rest of the audit family, which the shared audit machinery reads.

import { describe, expect, it } from "vitest";
import en from "../../i18n/external/en.json";
import ar from "../../i18n/external/ar.json";
import es from "../../i18n/external/es.json";
import fr from "../../i18n/external/fr.json";
import he from "../../i18n/external/he.json";
import pt from "../../i18n/external/pt.json";
import ru from "../../i18n/external/ru.json";
import mainEn from "../../i18n/en.json";
import mainAr from "../../i18n/ar.json";
import mainEs from "../../i18n/es.json";
import mainFr from "../../i18n/fr.json";
import mainHe from "../../i18n/he.json";
import mainPt from "../../i18n/pt.json";
import mainRu from "../../i18n/ru.json";

const EXTERNAL: Record<string, Record<string, string>> = { en, ar, es, fr, he, pt, ru };
const MAIN: Record<string, Record<string, string>> = { en: mainEn, ar: mainAr, es: mainEs, fr: mainFr, he: mainHe, pt: mainPt, ru: mainRu };

// The placeholders a sentence takes, as the English text names them, such as {email}.
const placeholders = (s: string) => [...s.matchAll(/\{([a-z]+)\}/g)].map((m) => m[1]).sort();

describe("the external-sharing catalogues", () => {
  it("every language has every external key, and nothing more", () => {
    const keys = Object.keys(en).sort();
    for (const [lang, cat] of Object.entries(EXTERNAL)) {
      expect(Object.keys(cat).sort(), lang).toEqual(keys);
    }
  });

  it("no text is empty, and each placeholder is the one the English text uses", () => {
    for (const [lang, cat] of Object.entries(EXTERNAL)) {
      for (const key of Object.keys(en)) {
        const text = cat[key];
        expect(text.trim(), `${lang} ${key}`).not.toBe("");
        expect(placeholders(text), `${lang} ${key}`).toEqual(placeholders(en[key as keyof typeof en]));
      }
    }
  });

  it("no key is in both the main and the external catalogue, so the main chunk does not grow with them", () => {
    for (const [lang, cat] of Object.entries(MAIN)) {
      expect(Object.keys(en).filter((k) => k in cat), lang).toEqual([]);
    }
  });

  it("the audit words of external sharing stay in the main catalogues with the audit family", () => {
    for (const [lang, cat] of Object.entries(MAIN)) {
      expect(cat["audit.user.email_proved"], lang).toBeTruthy();
      expect(cat["audit.permission.external.set"], lang).toBeTruthy();
      expect(cat["audit.permission.external.remove"], lang).toBeTruthy();
    }
  });
});

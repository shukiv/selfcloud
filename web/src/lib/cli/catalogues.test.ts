// SPDX-License-Identifier: AGPL-3.0-or-later

// The command-line sign-in strings (decision 125, slice cli4) are their own catalogue, web/src/i18n/cli/<lang>.json, loaded with
// the approval page and Signed-in devices, so the main catalogues (which every page loads) do not grow with them: the Russian one
// is at its budget. These tests hold the catalogues to the shape of the share and admin catalogues: every language has every key
// and nothing more, no text is empty, each placeholder is the one the English text uses, and the main catalogues no longer carry
// the moved keys. The account menu's entry (user.devices) is not a moved key and stays in the main catalogues.
//
import { describe, expect, it } from "vitest";
import cliEn from "../../i18n/cli/en.json";
import cliAr from "../../i18n/cli/ar.json";
import cliEs from "../../i18n/cli/es.json";
import cliFr from "../../i18n/cli/fr.json";
import cliHe from "../../i18n/cli/he.json";
import cliPt from "../../i18n/cli/pt.json";
import cliRu from "../../i18n/cli/ru.json";
import mainEn from "../../i18n/en.json";
import mainAr from "../../i18n/ar.json";
import mainEs from "../../i18n/es.json";
import mainFr from "../../i18n/fr.json";
import mainHe from "../../i18n/he.json";
import mainPt from "../../i18n/pt.json";
import mainRu from "../../i18n/ru.json";

const LANGS = ["en", "ar", "es", "fr", "he", "pt", "ru"] as const;
const MAIN: Record<string, Record<string, string>> = { en: mainEn, ar: mainAr, es: mainEs, fr: mainFr, he: mainHe, pt: mainPt, ru: mainRu };

const CLI: Record<string, Record<string, string>> = { en: cliEn, ar: cliAr, es: cliEs, fr: cliFr, he: cliHe, pt: cliPt, ru: cliRu };
const cliCatalogues = (): Record<string, Record<string, string>> => CLI;


const isMoved = (key: string) => key.startsWith("cliApprove.") || key.startsWith("devices.");

// The placeholders a sentence takes, as the English text names them, such as {name}.
const placeholders = (s: string) => [...s.matchAll(/\{([a-z]+)\}/g)].map((m) => m[1]).sort();

describe("the cli catalogues", () => {
  it("every language has every cli key, and nothing more", () => {
    const CLI = cliCatalogues();
    expect(Object.keys(CLI).sort(), "the languages with a cli catalogue").toEqual([...LANGS].sort());
    const keys = Object.keys(CLI.en).sort();
    for (const [lang, cat] of Object.entries(CLI)) {
      expect(Object.keys(cat).sort(), lang).toEqual(keys);
    }
  });

  it("the catalogue holds the 33 keys the approval page and Signed-in devices read, and each is a cliApprove or devices key", () => {
    const en = cliCatalogues().en;
    expect(Object.keys(en)).toHaveLength(33);
    for (const key of Object.keys(en)) expect(isMoved(key), key).toBe(true);
  });

  it("no text is empty, and each placeholder is the one the English text uses", () => {
    const CLI = cliCatalogues();
    const en = CLI.en;
    expect(en, "the English cli catalogue").toBeDefined();
    for (const [lang, cat] of Object.entries(CLI)) {
      for (const key of Object.keys(en)) {
        const text = cat[key];
        expect(typeof text, `${lang} ${key}`).toBe("string");
        expect(text.trim(), `${lang} ${key}`).not.toBe("");
        expect(placeholders(text), `${lang} ${key}`).toEqual(placeholders(en[key]));
      }
    }
  });

  it("the main catalogues no longer carry a cliApprove or devices key, so the main chunk does not grow with them", () => {
    for (const [lang, cat] of Object.entries(MAIN)) {
      expect(Object.keys(cat).filter(isMoved), lang).toEqual([]);
    }
  });

  it("the account menu's entry (user.devices) is not a moved key and stays in every main catalogue", () => {
    for (const [lang, cat] of Object.entries(MAIN)) {
      expect(cat["user.devices"], lang).toBeTruthy();
    }
  });
});

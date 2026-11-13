// SPDX-License-Identifier: AGPL-3.0-or-later

// The share page's own strings (decision 82). They are a catalogue of their own, loaded with the share page, so the main
// catalogues (which every page loads) do not grow with them: the budget of the language chunks is close. The keys keep their names
// (shareTarget.*), and they are read with the main translator once loaded. English is loaded with each language, as the fallback.
// ShareHold shows no text, so it does not load this catalogue.
import { addCatalogue, type Lang } from "../i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/share/en.json"),
  es: () => import("../../i18n/share/es.json"),
  fr: () => import("../../i18n/share/fr.json"),
  pt: () => import("../../i18n/share/pt.json"),
  ru: () => import("../../i18n/share/ru.json"),
  ar: () => import("../../i18n/share/ar.json"),
  he: () => import("../../i18n/share/he.json"),
};

const loaded = new Set<Lang>();

// loadShareText makes the share page's strings of l (and of English) available. It is idempotent.
export async function loadShareText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}

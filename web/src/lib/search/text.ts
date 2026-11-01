// SPDX-License-Identifier: AGPL-3.0-or-later

// The search page's own strings. They are a separate catalogue, loaded when the page opens, so the main catalogues (which
// every page loads) do not grow with them. English is loaded with each language, as the fallback.
import { addCatalogue, type Lang } from "../i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/search/en.json"),
  es: () => import("../../i18n/search/es.json"),
  fr: () => import("../../i18n/search/fr.json"),
  pt: () => import("../../i18n/search/pt.json"),
  ru: () => import("../../i18n/search/ru.json"),
  ar: () => import("../../i18n/search/ar.json"),
  he: () => import("../../i18n/search/he.json"),
};

const loaded = new Set<Lang>();

// loadSearchText makes the search strings of l (and of English) available. It is idempotent.
export async function loadSearchText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}

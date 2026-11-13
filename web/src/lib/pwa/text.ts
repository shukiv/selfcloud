// SPDX-License-Identifier: AGPL-3.0-or-later

// The install card's own strings (decision 171): the title, the one line, the two buttons and the link under them. They are
// a catalogue of their own, loaded only when the card is about to show, so the main catalogues (which every page loads) do
// not grow: the Russian one is at its size budget. The pattern is the access requests page's (lib/requests/text.ts). The
// keys are read with the main translator once loaded; English loads with each language, as the fallback. The close icon's
// name is the main catalogue's pwa.close, which is already there.
import { addCatalogue, type Lang } from "../i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/pwa/en.json"),
  es: () => import("../../i18n/pwa/es.json"),
  fr: () => import("../../i18n/pwa/fr.json"),
  pt: () => import("../../i18n/pwa/pt.json"),
  ru: () => import("../../i18n/pwa/ru.json"),
  ar: () => import("../../i18n/pwa/ar.json"),
  he: () => import("../../i18n/pwa/he.json"),
};

const loaded = new Set<Lang>();

// loadInstallText makes the card's strings of l (and of English) available. It is idempotent.
export async function loadInstallText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}
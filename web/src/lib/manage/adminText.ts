// SPDX-License-Identifier: AGPL-3.0-or-later

// The admin lists' own strings: the descriptions of their empty tables, and a tenant person's "Remove from account…" (decision
// 114). They are a catalogue of their own,
// loaded with the admin pages, so the main catalogues (which every page loads) do not grow with them: the Russian one is at
// its budget. The keys are read with the main translator once loaded. English is loaded with each language, as the fallback.
import { useEffect, useReducer } from "react";
import { addCatalogue, useLang, type Lang } from "../i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/admin/en.json"),
  es: () => import("../../i18n/admin/es.json"),
  fr: () => import("../../i18n/admin/fr.json"),
  pt: () => import("../../i18n/admin/pt.json"),
  ru: () => import("../../i18n/admin/ru.json"),
  ar: () => import("../../i18n/admin/ar.json"),
  he: () => import("../../i18n/admin/he.json"),
};

const loaded = new Set<Lang>();

// loadAdminText makes the admin strings of l (and of English) available. It is idempotent.
export async function loadAdminText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}

// useAdminText loads the admin strings of the current language and says whether they are there. The component re-renders
// when they arrive. Until then, or if the load fails, a caller leaves out what they say: an empty table keeps its title.
export function useAdminText(): boolean {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const ready = loaded.has(l) && loaded.has("en");
  useEffect(() => {
    if (ready) return;
    let live = true;
    loadAdminText(l).then(
      () => live && bump(),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [l, ready]);
  return ready;
}

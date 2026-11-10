// SPDX-License-Identifier: AGPL-3.0-or-later

// The command-line sign-in strings (decision 125, slice cli4): the approval page's and Signed-in devices' own. They are a catalogue
// of their own, loaded with those pages, so the main catalogues (which every page loads) do not grow with them: the Russian one is
// at its budget. The keys keep their names (cliApprove.*, devices.*), and they are read with the main translator once loaded.
// English is loaded with each language, as the fallback.
import { useEffect, useReducer } from "react";
import { addCatalogue, useLang, type Lang } from "../i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/cli/en.json"),
  es: () => import("../../i18n/cli/es.json"),
  fr: () => import("../../i18n/cli/fr.json"),
  pt: () => import("../../i18n/cli/pt.json"),
  ru: () => import("../../i18n/cli/ru.json"),
  ar: () => import("../../i18n/cli/ar.json"),
  he: () => import("../../i18n/cli/he.json"),
};

const loaded = new Set<Lang>();

// loadCliText makes the command-line strings of l (and of English) available. It is idempotent.
export async function loadCliText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}

// useCliText loads the command-line strings of the current language and says whether they are there. The component re-renders
// when they arrive. Until then a page shows a spinner, never a key; if the load fails, the spinner stays.
export function useCliText(): boolean {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const ready = loaded.has(l) && loaded.has("en");
  useEffect(() => {
    if (ready) return;
    let live = true;
    loadCliText(l).then(
      () => live && bump(),
      () => undefined,
    );
    return () => {
      live = false;
    };
  }, [l, ready]);
  return ready;
}

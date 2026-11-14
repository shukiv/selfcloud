// SPDX-License-Identifier: AGPL-3.0-or-later

// The keyboard shortcuts list's own strings (ks1, decision 174): the group titles and the row names. They are a catalogue of
// their own, loaded with the dialog, so the main catalogues (which every page loads) do not grow. The pattern is the access
// requests page's (lib/requests/text.ts). The keys are read with the main translator once loaded; English loads with each
// language, as the fallback.
import { useCallback, useEffect, useReducer, useState } from "react";
import { addCatalogue, useLang, type Lang } from "./i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../i18n/shortcuts/en.json"),
  es: () => import("../i18n/shortcuts/es.json"),
  fr: () => import("../i18n/shortcuts/fr.json"),
  pt: () => import("../i18n/shortcuts/pt.json"),
  ru: () => import("../i18n/shortcuts/ru.json"),
  ar: () => import("../i18n/shortcuts/ar.json"),
  he: () => import("../i18n/shortcuts/he.json"),
};

const loaded = new Set<Lang>();

// loadShortcutsText makes the list's strings of l (and of English) available. It is idempotent.
export async function loadShortcutsText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}

export interface ShortcutsText {
  // ready: the strings are there, so the list may draw its rows.
  ready: boolean;
  // failed: the strings did not load; the dialog shows an error with Try again and no list.
  failed: boolean;
  retry: () => void;
}

// useShortcutsText loads the list's strings for the current language. A failed load is shown, and retry loads them again.
export function useShortcutsText(): ShortcutsText {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [attempt, setAttempt] = useState(0);
  const [failedKey, setFailedKey] = useState<string | null>(null);
  const ready = loaded.has(l) && loaded.has("en");
  const key = `${l}:${attempt}`;
  useEffect(() => {
    if (ready) return;
    let live = true;
    loadShortcutsText(l).then(
      () => live && bump(),
      () => live && setFailedKey(key),
    );
    return () => {
      live = false;
    };
    // key changes with attempt and language, so each try reads again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [l, ready, attempt]);
  const retry = useCallback(() => setAttempt((n) => n + 1), []);
  return { ready, failed: !ready && failedKey === key, retry };
}

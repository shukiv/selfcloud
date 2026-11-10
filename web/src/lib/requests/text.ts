// SPDX-License-Identifier: AGPL-3.0-or-later

// The access requests page's own strings: the column titles and the empty table's title (tl5, decision 138). They are a catalogue
// of their own, loaded with the page, so the main catalogues (which every page loads) do not grow. The pattern is the drive lists'
// (lib/drive/driveText.ts). The keys are read with the main translator once loaded; English loads with each language, as the fallback.
import { useCallback, useEffect, useReducer, useState } from "react";
import { addCatalogue, useLang, type Lang } from "../i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/requests/en.json"),
  es: () => import("../../i18n/requests/es.json"),
  fr: () => import("../../i18n/requests/fr.json"),
  pt: () => import("../../i18n/requests/pt.json"),
  ru: () => import("../../i18n/requests/ru.json"),
  ar: () => import("../../i18n/requests/ar.json"),
  he: () => import("../../i18n/requests/he.json"),
};

const loaded = new Set<Lang>();

// loadRequestsText makes the access requests page's strings of l (and of English) available. It is idempotent.
export async function loadRequestsText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}

export interface RequestsText {
  // ready: the strings are there, so the table may draw its titles.
  ready: boolean;
  // failed: the strings did not load; the page shows an error with Try again and no table.
  failed: boolean;
  retry: () => void;
}

// useRequestsText loads the page's strings for the current language. A failed load is shown, and retry loads them again.
export function useRequestsText(): RequestsText {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [attempt, setAttempt] = useState(0);
  const [failedKey, setFailedKey] = useState<string | null>(null);
  const ready = loaded.has(l) && loaded.has("en");
  const key = `${l}:${attempt}`;
  useEffect(() => {
    if (ready) return;
    let live = true;
    loadRequestsText(l).then(
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

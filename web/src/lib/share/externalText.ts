// SPDX-License-Identifier: AGPL-3.0-or-later

// The words of sharing with people in other accounts (decision 187). They are a catalogue of their own, loaded with the pages
// that show them — the share dialog's outside parts, the Shared with me "From other accounts" section, the /x view, the prove
// page and the account's external-sharing switch — so the main catalogues (which every page loads) do not grow with them: the
// Russian one is at its budget. The keys keep their names, and they are read with the main translator once loaded. English is
// loaded with each language, as the fallback.
import { useCallback, useEffect, useReducer, useState } from "react";
import { addCatalogue, useLang, type Lang } from "../i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/external/en.json"),
  es: () => import("../../i18n/external/es.json"),
  fr: () => import("../../i18n/external/fr.json"),
  pt: () => import("../../i18n/external/pt.json"),
  ru: () => import("../../i18n/external/ru.json"),
  ar: () => import("../../i18n/external/ar.json"),
  he: () => import("../../i18n/external/he.json"),
};

const loaded = new Set<Lang>();

// loadExternalText makes the external-sharing words of l (and of English) available. It is idempotent.
export async function loadExternalText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}

export interface ExternalText {
  // ready: the words are there, so the page may draw its text.
  ready: boolean;
  // failed: the words did not load; the page shows an error with retry (Try again) and no text of this catalogue.
  failed: boolean;
  retry: () => void;
}

// useExternalText loads the external-sharing words of the current language. The component re-renders when they arrive. A
// component that shows these keys waits for them, so no raw key is shown. A failed load is shown, and retry loads them again.
export function useExternalText(): ExternalText {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [attempt, setAttempt] = useState(0);
  const [failedKey, setFailedKey] = useState<string | null>(null);
  const ready = loaded.has(l) && loaded.has("en");
  const key = `${l}:${attempt}`;
  useEffect(() => {
    if (ready) return;
    let live = true;
    loadExternalText(l).then(
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

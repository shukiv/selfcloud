// SPDX-License-Identifier: AGPL-3.0-or-later

// The drive lists' own strings: the column titles that Starred, Recent, Shared with me, Trash and Search draw on the one table
// (tl4, decision 137). They are a catalogue of their own, loaded with those pages, so the main catalogues (which every page
// loads) do not grow with them: the Russian one is at its budget. The keys are read with the main translator once loaded.
// English is loaded with each language, as the fallback.
import { useCallback, useEffect, useReducer, useState } from "react";
import { addCatalogue, useLang, type Lang } from "../i18n";

type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/drive/en.json"),
  es: () => import("../../i18n/drive/es.json"),
  fr: () => import("../../i18n/drive/fr.json"),
  pt: () => import("../../i18n/drive/pt.json"),
  ru: () => import("../../i18n/drive/ru.json"),
  ar: () => import("../../i18n/drive/ar.json"),
  he: () => import("../../i18n/drive/he.json"),
};

const loaded = new Set<Lang>();

// loadDriveText makes the drive lists' strings of l (and of English) available. It is idempotent.
export async function loadDriveText(l: Lang): Promise<void> {
  const wanted: Lang[] = l === "en" ? ["en"] : ["en", l];
  for (const want of wanted) {
    if (loaded.has(want)) continue;
    const mod = await loaders[want]();
    addCatalogue(want, mod.default);
    loaded.add(want);
  }
}

export interface DriveText {
  // ready: the strings are there, so a table may draw its titles.
  ready: boolean;
  // failed: the strings did not load; the page shows an error with retry (Try again) and no table.
  failed: boolean;
  retry: () => void;
}

// useDriveText loads the drive lists' strings of the current language. The component re-renders when they arrive. A table waits
// for them: its column titles are these keys until then. A failed load is shown, and retry loads them again (tl4 r1, finding 10).
export function useDriveText(): DriveText {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [attempt, setAttempt] = useState(0);
  const [failedKey, setFailedKey] = useState<string | null>(null);
  const ready = loaded.has(l) && loaded.has("en");
  const key = `${l}:${attempt}`;
  useEffect(() => {
    if (ready) return;
    let live = true;
    loadDriveText(l).then(
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

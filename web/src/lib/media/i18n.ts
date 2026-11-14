// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useReducer, useMemo, useState } from "react";
import i18next from "i18next";
import { useLang, type Lang, type Params } from "../i18n";

// The texts of the Media page are a separate catalogue (namespace "media"), loaded with the page as a chunk of its own,
// so the main catalogues stay under their budget. The sidebar's one entry (nav.media) is in the main catalogues. English
// is always loaded with a language, so a missing text falls back to English, as the main catalogues do. The same pattern
// as lib/notifications/i18n.ts.
type Catalogue = { default: Record<string, string> };
const NS = "media";
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/media/en.json"),
  es: () => import("../../i18n/media/es.json"),
  fr: () => import("../../i18n/media/fr.json"),
  pt: () => import("../../i18n/media/pt.json"),
  ru: () => import("../../i18n/media/ru.json"),
  ar: () => import("../../i18n/media/ar.json"),
  he: () => import("../../i18n/media/he.json"),
};

const loaded = new Set<Lang>();
const pending = new Map<Lang, Promise<void>>();

// loadMedia makes the media catalogue of l available, and English under it. It is idempotent.
export function loadMedia(l: Lang): Promise<void> {
  if (loaded.has(l)) return Promise.resolve();
  const running = pending.get(l);
  if (running) return running;
  const job = (async () => {
    if (!loaded.has("en")) {
      const en = await loaders.en();
      i18next.addResourceBundle("en", NS, en.default, true, true);
      loaded.add("en");
    }
    if (l !== "en") {
      const mod = await loaders[l]();
      i18next.addResourceBundle(l, NS, mod.default, true, true);
    }
    loaded.add(l);
  })().finally(() => pending.delete(l));
  pending.set(l, job);
  return job;
}

// mtranslate reads a text of the media catalogue in l. A key with no text in any catalogue is shown as the key.
export function mtranslate(l: Lang, key: string, params?: Params): string {
  const opts: Record<string, string> = {};
  for (const [k, v] of Object.entries(params ?? {})) opts[k] = String(v);
  return String(i18next.getFixedT(l, NS)(key, opts));
}

export interface MediaT {
  // ready is false until the catalogue of the current language has loaded. The page's own texts wait for it; the heading
  // falls back to the sidebar's word, which is always there.
  ready: boolean;
  // failed is true when that load failed. The page then says so in the main catalogue, which is always there.
  failed: boolean;
  t: (key: string, params?: Params) => string;
}

// useMT returns the translator of the media catalogue for the current language, and re-renders when the language changes
// or its catalogue arrives.
export function useMT(): MediaT {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [failedFor, setFailedFor] = useState<Lang | null>(null);
  useEffect(() => {
    if (loaded.has(l)) return;
    let live = true;
    void loadMedia(l).then(
      () => live && bump(),
      () => live && setFailedFor(l),
    );
    return () => {
      live = false;
    };
  }, [l]);
  return useMemo(
    () => ({
      ready: loaded.has(l),
      failed: failedFor === l && !loaded.has(l),
      t: (key: string, params?: Params) => mtranslate(l, key, params),
    }),
    // The arrival of the catalogue re-renders the hook, and the ready flag is read again then.
    [l, loaded.has(l), failedFor],
  );
}

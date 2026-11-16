// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useReducer, useMemo, useState } from "react";
import i18next from "i18next";
import { useLang, type Lang, type Params } from "../i18n";

// The texts of the frame views are a separate catalogue (namespace "embed"), loaded as a chunk of its own when a frame
// session draws one of them, so the main catalogues stay under their budget and a tab session never loads it (decision
// 214). English is always loaded with a language, so a missing text falls back to English, as the main catalogues do.
// The same pattern as lib/media/i18n.ts.
type Catalogue = { default: Record<string, string> };
const NS = "embed";
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/embed/en.json"),
  es: () => import("../../i18n/embed/es.json"),
  fr: () => import("../../i18n/embed/fr.json"),
  pt: () => import("../../i18n/embed/pt.json"),
  ru: () => import("../../i18n/embed/ru.json"),
  ar: () => import("../../i18n/embed/ar.json"),
  he: () => import("../../i18n/embed/he.json"),
};

const loaded = new Set<Lang>();
const pending = new Map<Lang, Promise<void>>();
// Every open frame view watches the catalogue, so a Retry in one loads it once and every view re-renders when it arrives.
const watchers = new Set<() => void>();

// loadEmbed makes the embed catalogue of l available, and English under it. It is idempotent; a failed load is not
// kept, so a Retry asks again.
export function loadEmbed(l: Lang): Promise<void> {
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
    for (const watch of watchers) watch();
  })().finally(() => pending.delete(l));
  pending.set(l, job);
  return job;
}

// etranslate reads a text of the embed catalogue in l. A key with no text in any catalogue is shown as the key.
export function etranslate(l: Lang, key: string, params?: Params): string {
  const opts: Record<string, string> = {};
  for (const [k, v] of Object.entries(params ?? {})) opts[k] = String(v);
  return String(i18next.getFixedT(l, NS)(key, opts));
}

export interface EmbedT {
  // ready is false until the catalogue of the current language has loaded. The view's own texts wait for it.
  ready: boolean;
  // failed is true when that load failed. The view then says so in the main catalogue, which is always there.
  failed: boolean;
  t: (key: string, params?: Params) => string;
}

// useET returns the translator of the embed catalogue for the current language, and re-renders when the language
// changes or its catalogue arrives.
export function useET(): EmbedT {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [failedFor, setFailedFor] = useState<Lang | null>(null);
  useEffect(() => {
    const watch = () => bump();
    watchers.add(watch);
    return () => {
      watchers.delete(watch);
    };
  }, []);
  useEffect(() => {
    if (loaded.has(l)) return;
    let live = true;
    void loadEmbed(l).then(
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
      t: (key: string, params?: Params) => etranslate(l, key, params),
    }),
    // The arrival of the catalogue re-renders the hook, and the ready flag is read again then.
    [l, loaded.has(l), failedFor],
  );
}

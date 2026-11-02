// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useReducer, useMemo, useState } from "react";
import i18next from "i18next";
import { useLang, type Lang, type Params } from "../i18n";

// The texts of the comments, activity and who-viewed panels, and of the sharing switches, are a separate catalogue
// (namespace "comments"), loaded with the panels as a chunk of their own. The main catalogues are close to their
// size budget, and these texts are not needed until a file is open. English is always loaded with a language, so a
// missing text falls back to English, as the main catalogues do.
type Catalogue = { default: Record<string, string> };
const NS = "comments";
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/comments/en.json"),
  es: () => import("../../i18n/comments/es.json"),
  fr: () => import("../../i18n/comments/fr.json"),
  pt: () => import("../../i18n/comments/pt.json"),
  ru: () => import("../../i18n/comments/ru.json"),
  ar: () => import("../../i18n/comments/ar.json"),
  he: () => import("../../i18n/comments/he.json"),
};

const loaded = new Set<Lang>();
const pending = new Map<Lang, Promise<void>>();

// loadComments makes the comments catalogue of l available, and English under it. It is idempotent.
export function loadComments(l: Lang): Promise<void> {
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

// ctranslate reads a text of the comments catalogue in l. A key with no text in any catalogue is shown as the key.
export function ctranslate(l: Lang, key: string, params?: Params): string {
  const opts: Record<string, string> = {};
  for (const [k, v] of Object.entries(params ?? {})) opts[k] = String(v);
  return String(i18next.getFixedT(l, NS)(key, opts));
}

export interface CommentsT {
  // ready is false until the catalogue of the current language has loaded. A panel draws nothing until then.
  ready: boolean;
  // failed is true when that load failed. The panels then say so, in the main catalogue, which is always there.
  failed: boolean;
  t: (key: string, params?: Params) => string;
}

// useCT returns the translator of the comments catalogue for the current language, and re-renders when the language
// changes or its catalogue arrives.
export function useCT(): CommentsT {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [failedFor, setFailedFor] = useState<Lang | null>(null);
  useEffect(() => {
    if (loaded.has(l)) return;
    let live = true;
    void loadComments(l).then(
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
      t: (key: string, params?: Params) => ctranslate(l, key, params),
    }),
    // The arrival of the catalogue re-renders the hook, and the ready flag is read again then.
    [l, loaded.has(l), failedFor],
  );
}

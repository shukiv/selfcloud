// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useReducer, useMemo, useState } from "react";
import i18next from "i18next";
import { useLang, type Lang, type Params } from "../i18n";

// The texts of the bell and the notifications page are a separate catalogue (namespace "notifications"), loaded with the
// bell as a chunk of its own, so the main catalogues stay under their budget. English is always loaded with a language, so
// a missing text falls back to English, as the main catalogues do. The same pattern as lib/comments/i18n.ts.
type Catalogue = { default: Record<string, string> };
const NS = "notifications";
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/notifications/en.json"),
  es: () => import("../../i18n/notifications/es.json"),
  fr: () => import("../../i18n/notifications/fr.json"),
  pt: () => import("../../i18n/notifications/pt.json"),
  ru: () => import("../../i18n/notifications/ru.json"),
  ar: () => import("../../i18n/notifications/ar.json"),
  he: () => import("../../i18n/notifications/he.json"),
};

const loaded = new Set<Lang>();
const pending = new Map<Lang, Promise<void>>();

// loadNotifications makes the notifications catalogue of l available, and English under it. It is idempotent.
export function loadNotifications(l: Lang): Promise<void> {
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

// ntranslate reads a text of the notifications catalogue in l. A key with no text in any catalogue is shown as the key.
export function ntranslate(l: Lang, key: string, params?: Params): string {
  const opts: Record<string, string> = {};
  for (const [k, v] of Object.entries(params ?? {})) opts[k] = String(v);
  return String(i18next.getFixedT(l, NS)(key, opts));
}

export interface NotificationsT {
  // ready is false until the catalogue of the current language has loaded. The bell draws nothing until then.
  ready: boolean;
  // failed is true when that load failed. The page then says so in the main catalogue, which is always there.
  failed: boolean;
  t: (key: string, params?: Params) => string;
}

// useNT returns the translator of the notifications catalogue for the current language, and re-renders when the language
// changes or its catalogue arrives.
export function useNT(): NotificationsT {
  const l = useLang();
  const [, bump] = useReducer((n: number) => n + 1, 0);
  const [failedFor, setFailedFor] = useState<Lang | null>(null);
  useEffect(() => {
    if (loaded.has(l)) return;
    let live = true;
    void loadNotifications(l).then(
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
      t: (key: string, params?: Params) => ntranslate(l, key, params),
    }),
    // The arrival of the catalogue re-renders the hook, and the ready flag is read again then.
    [l, loaded.has(l), failedFor],
  );
}

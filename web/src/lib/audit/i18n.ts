// SPDX-License-Identifier: AGPL-3.0-or-later

import { useEffect, useReducer, useMemo, useState } from "react";
import i18next from "i18next";
import { useLang, type Lang, type Params } from "../i18n";
import { renderSentence, type Ref, type Sentence } from "./sentence";

// The activity log's sentences are a separate catalogue (namespace "audit"), loaded with the parts that show them as a
// chunk of their own, so the main catalogues stay under their budget. English is always loaded with a language, so a
// missing text falls back to English, as the main catalogues do. The same pattern as lib/media/i18n.ts.
type Catalogue = { default: Record<string, string> };
const NS = "audit";
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: () => import("../../i18n/audit/en.json"),
  es: () => import("../../i18n/audit/es.json"),
  fr: () => import("../../i18n/audit/fr.json"),
  pt: () => import("../../i18n/audit/pt.json"),
  ru: () => import("../../i18n/audit/ru.json"),
  ar: () => import("../../i18n/audit/ar.json"),
  he: () => import("../../i18n/audit/he.json"),
};

const loaded = new Set<Lang>();
const pending = new Map<Lang, Promise<void>>();
// Every open part watches the catalogue, so a Retry in one part loads it once and every part re-renders when it arrives.
const watchers = new Set<() => void>();

// loadAudit makes the audit catalogue of l available, and English under it. It is idempotent.
export function loadAudit(l: Lang): Promise<void> {
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

// atranslate reads a text of the audit catalogue in l. A key with no text in any catalogue is shown as the key.
export function atranslate(l: Lang, key: string, params?: Params): string {
  const opts: Record<string, string> = {};
  for (const [k, v] of Object.entries(params ?? {})) opts[k] = String(v);
  return String(i18next.getFixedT(l, NS)(key, opts));
}

export interface AuditT {
  // ready is false until the catalogue of the current language has loaded. The part that shows sentences waits for it.
  ready: boolean;
  // failed is true when that load failed. The part then says so in the main catalogue, which is always there.
  failed: boolean;
  t: (key: string, params?: Params) => string;
}

// useAT returns the translator of the audit catalogue for the current language, and re-renders when the language changes
// or its catalogue arrives.
export function useAT(): AuditT {
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
    void loadAudit(l).then(
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
      t: (key: string, params?: Params) => atranslate(l, key, params),
    }),
    // The arrival of the catalogue re-renders the hook, and the ready flag is read again then.
    [l, loaded.has(l), failedFor],
  );
}

type Translate = (key: string, params?: Params) => string;

// refText reads one parameter of a sentence: text is shown as it is, an audit key is read from the audit catalogue, and
// any other key from the main one (a sentence may name a main catalogue's word too).
export function refText(ref: Ref, t: Translate, at: Translate): string {
  if ("text" in ref) return ref.text;
  return ref.key.startsWith("audit.") ? at(ref.key) : t(ref.key);
}

// auditText turns a sentence into words: its key and its audit parameters are read from the audit catalogue, any other
// parameter from the main one. Every place that shows a sentence reads it through here.
export function auditText(s: Sentence, t: Translate, at: Translate): string {
  return renderSentence(
    s,
    (key, params) => (key.startsWith("audit.") ? at(key, params) : t(key, params)),
    (ref) => refText(ref, t, at),
  );
}

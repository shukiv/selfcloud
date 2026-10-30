// SPDX-License-Identifier: AGPL-3.0-or-later

import i18next, { type FormatterModule } from 'i18next';
import { useMemo } from 'react';
import type { Locale as AntdLocale } from 'antd/es/locale';
import en from '../i18n/en.json';
import { useStore, derived, get, writable } from './store';
import { PREF_LANG, readPref, writePref } from './prefs';

export const LANGUAGES = ['en', 'es', 'fr', 'pt', 'ru', 'ar', 'he'] as const;
export type Lang = (typeof LANGUAGES)[number];

// NATIVE_NAMES names each language in itself, for the language picker.
export const NATIVE_NAMES: Record<Lang, string> = {
  en: 'English',
  es: 'Español',
  fr: 'Français',
  pt: 'Português (Portugal)',
  ru: 'Русский',
  ar: 'العربية',
  he: 'עברית',
};

export type Params = Record<string, string | number>;

// matchLang returns the supported language for a tag: the exact tag, then its base language, else
// null. "pt-BR" is Portuguese; "pt-PT" is too, since the only Portuguese text is European.
export function matchLang(tag: string | null | undefined): Lang | null {
  if (!tag) return null;
  const t = tag.trim().toLowerCase();
  if (t === '') return null;
  if ((LANGUAGES as readonly string[]).includes(t)) return t as Lang;
  const base = t.split('-')[0];
  return (LANGUAGES as readonly string[]).includes(base) ? (base as Lang) : null;
}

export function directionOf(l: Lang): 'ltr' | 'rtl' {
  return l === 'ar' || l === 'he' ? 'rtl' : 'ltr';
}

// Catalogues: English is bundled; the others load on first use, before the language is set.
type Catalogue = { default: Record<string, string> };
const loaders: Record<Lang, () => Promise<Catalogue>> = {
  en: async () => ({ default: en }),
  es: () => import('../i18n/es.json'),
  fr: () => import('../i18n/fr.json'),
  pt: () => import('../i18n/pt.json'),
  ru: () => import('../i18n/ru.json'),
  ar: () => import('../i18n/ar.json'),
  he: () => import('../i18n/he.json'),
};

const loaded = new Set<Lang>(['en']);

// formatValue writes a number with the digits of its language. Other values are already strings.
function formatValue(value: unknown, _format: string | undefined, lng: string | undefined): string {
  if (value === undefined) return value as unknown as string; // no value: i18next handles it
  if (typeof value === 'number') return new Intl.NumberFormat(lng).format(value);
  return String(value);
}

// The formatter is a module, so the placeholder formatting goes through i18next's own pipeline.
const formatter = {
  type: 'formatter',
  init() {},
  add() {},
  addCached() {},
  format: formatValue,
} as unknown as FormatterModule;

i18next.use(formatter).init({
  lng: 'en',
  fallbackLng: 'en',
  resources: { en: { common: en } },
  defaultNS: 'common',
  ns: ['common'],
  keySeparator: false,
  nsSeparator: false,
  interpolation: {
    prefix: '{',
    suffix: '}',
    escapeValue: false,
    alwaysFormat: true,
  },
  // A placeholder with no value is left as written.
  missingInterpolationHandler: (_text: string, match: RegExpExecArray) => match[0],
});

// toOptions turns every parameter into text except count, which stays a number so it is formatted
// (and selects the plural form).
function toOptions(params?: Params): Record<string, string | number> {
  const out: Record<string, string | number> = {};
  for (const [k, v] of Object.entries(params ?? {})) out[k] = k === 'count' ? v : String(v);
  return out;
}

// translate returns the text for key in l, falling back to English, then to the key itself. A
// placeholder with no value stays in the text.
export function translate(l: Lang, key: string, params?: Params): string {
  return String(i18next.getFixedT(l, 'common')(key, toOptions(params)));
}

// translatePlural picks the form of key that count needs in l, and fills {count} with the number
// formatted for l.
export function translatePlural(l: Lang, key: string, count: number, params?: Params): string {
  return String(i18next.getFixedT(l, 'common')(key, { ...toOptions(params), count }));
}

// loadLang makes the catalogue of l available. It is idempotent.
export async function loadLang(l: Lang): Promise<void> {
  if (loaded.has(l)) return;
  const mod = await loaders[l]();
  i18next.addResourceBundle(l, 'common', mod.default, true, true);
  loaded.add(l);
}

// lang is the language the interface is shown in.
export const lang = writable<Lang>('en');

// setLang loads the language if needed, then switches to it and sets the document's lang and dir,
// which the layout depends on.
export async function setLang(l: Lang): Promise<void> {
  await loadLang(l);
  await i18next.changeLanguage(l);
  lang.set(l);
  document.documentElement.lang = l;
  document.documentElement.dir = directionOf(l);
}

// chooseLang applies the start-up order: ?lang=, then the signed-in person's locale, then the saved
// choice, then the browser's languages in order, then English.
export function chooseLang(input: {
  query?: string | null;
  userLocale?: string | null;
  saved?: string | null;
  browser?: readonly string[];
}): Lang {
  const candidates = [input.query, input.userLocale, input.saved, ...(input.browser ?? [])];
  for (const c of candidates) {
    const m = matchLang(c);
    if (m) return m;
  }
  return 'en';
}

// savedLang reads the language chosen while signed out, or null.
export function savedLang(): string | null {
  return readPref(PREF_LANG);
}

// rememberLang saves the language chosen while signed out.
export function rememberLang(l: Lang): void {
  writePref(PREF_LANG, l);
}

// antdLocaleOf loads the antd locale for l (its own strings for pickers, pagination and forms).
const antdLoaders: Record<Lang, () => Promise<{ default: AntdLocale }>> = {
  en: () => import('antd/locale/en_US'),
  es: () => import('antd/locale/es_ES'),
  fr: () => import('antd/locale/fr_FR'),
  pt: () => import('antd/locale/pt_PT'),
  ru: () => import('antd/locale/ru_RU'),
  ar: () => import('antd/locale/ar_EG'),
  he: () => import('antd/locale/he_IL'),
};

export async function antdLocaleOf(l: Lang): Promise<AntdLocale> {
  return (await antdLoaders[l]()).default;
}

// useLang returns the current language, and re-renders when it changes.
export function useLang(): Lang {
  return useStore(lang);
}

// useT returns the translators of the current language: t for text, tp for a plural key. A component
// that uses them re-renders when the language changes.
export function useT(): {
  t: (key: string, params?: Params) => string;
  tp: (key: string, count: number, params?: Params) => string;
} {
  const current = useLang();
  return useMemo(
    () => ({
      t: (key: string, params?: Params) => translate(current, key, params),
      tp: (key: string, count: number, params?: Params) => translatePlural(current, key, count, params),
    }),
    [current],
  );
}

// tr and tp are the store forms of t and tp, for code outside React.
export const tr = derived(lang, ($l) => (key: string, params?: Params) => translate($l, key, params));
export const tp = derived(
  lang,
  ($l) => (key: string, count: number, params?: Params) => translatePlural($l, key, count, params),
);

// currentLang reads the language without subscribing.
export function currentLang(): Lang {
  return get(lang);
}

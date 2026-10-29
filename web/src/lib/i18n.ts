// SPDX-License-Identifier: AGPL-3.0-or-later

import { derived, writable } from 'svelte/store';
import en from '../i18n/en.json';
import es from '../i18n/es.json';
import fr from '../i18n/fr.json';
import pt from '../i18n/pt.json';
import ru from '../i18n/ru.json';
import ar from '../i18n/ar.json';
import he from '../i18n/he.json';

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

const catalogues: Record<Lang, Record<string, string>> = { en, es, fr, pt, ru, ar, he };

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

export const lang = writable<Lang>('en');

// setLang switches the language and the document's lang and dir, which the layout depends on.
export function setLang(l: Lang): void {
  lang.set(l);
  document.documentElement.lang = l;
  document.documentElement.dir = directionOf(l);
}

function fill(raw: string, params?: Params): string {
  if (!params) return raw;
  return raw.replace(/\{([A-Za-z]+)\}/g, (whole, name: string) =>
    Object.prototype.hasOwnProperty.call(params, name) ? String(params[name]) : whole,
  );
}

// translate returns the text for key in l, falling back to English, then to the key itself.
export function translate(l: Lang, key: string, params?: Params): string {
  const raw = catalogues[l][key] ?? catalogues.en[key] ?? key;
  return fill(raw, params);
}

// translatePlural picks the form of key that count needs in l (Intl.PluralRules), and fills {count}
// with the number formatted for l.
export function translatePlural(l: Lang, key: string, count: number, params?: Params): string {
  const category = new Intl.PluralRules(l).select(count);
  const raw =
    catalogues[l][`${key}.${category}`] ??
    catalogues[l][`${key}.other`] ??
    catalogues.en[`${key}.other`] ??
    key;
  return fill(raw, { count: new Intl.NumberFormat(l).format(count), ...params });
}

// tr and tp follow the language, so a template that reads $tr re-renders when it changes.
export const tr = derived(lang, ($l) => (key: string, params?: Params) => translate($l, key, params));
export const tp = derived(
  lang,
  ($l) => (key: string, count: number, params?: Params) => translatePlural($l, key, count, params),
);

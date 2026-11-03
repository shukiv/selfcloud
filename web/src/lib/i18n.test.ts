// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeAll, describe, expect, it } from 'vitest';
import en from '../i18n/en.json';
import es from '../i18n/es.json';
import fr from '../i18n/fr.json';
import pt from '../i18n/pt.json';
import ru from '../i18n/ru.json';
import ar from '../i18n/ar.json';
import he from '../i18n/he.json';
import {
  chooseLang,
  directionOf,
  isolate,
  loadLang,
  LANGUAGES,
  matchLang,
  setLang,
  translate,
  translatePlural,
} from './i18n';

beforeAll(async () => {
  for (const l of LANGUAGES) await loadLang(l);
});

describe('matchLang', () => {
  it('takes an exact tag, then its base language', () => {
    expect(matchLang('he')).toBe('he');
    expect(matchLang('he-IL')).toBe('he');
    expect(matchLang('pt-BR')).toBe('pt');
    expect(matchLang('EN-us')).toBe('en');
    expect(matchLang(' fr ')).toBe('fr');
  });

  it('is null for an unknown or empty tag', () => {
    expect(matchLang('xx')).toBeNull();
    expect(matchLang('xx-YY')).toBeNull();
    expect(matchLang('')).toBeNull();
    expect(matchLang(null)).toBeNull();
    expect(matchLang(undefined)).toBeNull();
  });

  it('marks Hebrew and Arabic right to left', () => {
    expect(directionOf('he')).toBe('rtl');
    expect(directionOf('ar')).toBe('rtl');
    expect(directionOf('en')).toBe('ltr');
    expect(directionOf('pt')).toBe('ltr');
  });
});

describe('translate', () => {
  it('fills named placeholders and leaves unknown ones alone', () => {
    expect(translate('en', 'placeholder.body', { product: 'Acme' })).toBe('This part of Acme is not built yet.');
    expect(translate('en', 'placeholder.body')).toBe('This part of {product} is not built yet.');
  });

  it('shows the key when no language has it', () => {
    expect(translate('he', 'no.such.key')).toBe('no.such.key');
  });

  it('gives Hebrew the plural imperative for an instruction', () => {
    expect(translate('he', 'login.submit')).toBe('התחברו');
  });
});

// A value shown inside a right-to-left sentence is wrapped in isolate marks (decision 67); these are U+2068 and U+2069.
const FSI = '\u2068';
const PDI = '\u2069';

describe('values in a right-to-left sentence', () => {
  it('isolates a Latin name in a Hebrew sentence, and leaves the rest of it alone', () => {
    expect(translate('he', 'files.newFolder.created', { name: 'Report.pdf' })).toBe(`נוצרה התיקייה ${FSI}Report.pdf${PDI}.`);
    expect(translate('ar', 'files.newFolder.created', { name: 'Report.pdf' })).toBe(`تم إنشاء ${FSI}Report.pdf${PDI}.`);
  });

  it('isolates empty text as nothing, in every direction', () => {
    expect(isolate('he', '')).toBe('');
    expect(isolate('en', '')).toBe('');
  });

  it('adds no isolate marks to English, Spanish or Russian', () => {
    expect(translate('en', 'files.newFolder.created', { name: 'Report.pdf' })).toBe('Created Report.pdf.');
    expect(translate('es', 'files.newFolder.created', { name: 'Report.pdf' })).not.toMatch(/[\u2066-\u2069]/);
    expect(translate('ru', 'files.newFolder.created', { name: 'Report.pdf' })).not.toMatch(/[\u2066-\u2069]/);
  });

  it('isolates the count in a Hebrew plural, and still picks the form for the count', () => {
    expect(translatePlural('he', 'error.rateLimited', 3)).toBe(`יותר מדי ניסיונות. נסו שוב בעוד ${FSI}3${PDI} דקות.`);
    expect(translatePlural('he', 'error.rateLimited', 1)).toBe('יותר מדי ניסיונות. נסו שוב בעוד דקה.');
    expect(translatePlural('he', 'error.rateLimited', 2)).toBe('יותר מדי ניסיונות. נסו שוב בעוד שתי דקות.');
  });
});

describe('translatePlural', () => {
  it('has the count of anyone links in every language, for each plural form', () => {
    for (const l of ['en', 'es', 'fr', 'pt', 'ru', 'ar', 'he'] as const) {
      for (const n of [0, 1, 2, 3, 11, 100]) {
        const text = translatePlural(l, 'policy.anyoneLinks', n);
        expect(text).not.toBe('policy.anyoneLinks');
        if (n >= 3) expect(text).toContain(new Intl.NumberFormat(l).format(n));
      }
    }
  });

  it('picks Russian forms by the count', () => {
    const at = (n: number) => translatePlural('ru', 'error.rateLimited', n);
    expect(at(1)).toContain('минуту');
    expect(at(2)).toContain('минуты');
    expect(at(5)).toContain('минут');
    expect(at(21)).toContain('минуту');
  });

  it('picks Arabic forms, including the dual', () => {
    const at = (n: number) => translatePlural('ar', 'error.rateLimited', n);
    expect(at(0)).toContain('محاولات كثيرة');
    expect(at(1)).toContain('دقيقة واحدة');
    expect(at(2)).toContain('دقيقتين');
    expect(at(5)).toContain('دقائق');
    expect(at(11)).toContain('دقيقة');
  });

  it('fills the count, and the English singular', () => {
    expect(translatePlural('en', 'error.rateLimited', 1)).toBe('Too many attempts. Try again in 1 minute.');
    expect(translatePlural('en', 'error.rateLimited', 3)).toBe('Too many attempts. Try again in 3 minutes.');
  });
});

describe('chooseLang', () => {
  it('takes the query first, then the person, then the saved choice, then the browser', () => {
    expect(chooseLang({ query: 'fr', userLocale: 'he', saved: 'ru', browser: ['es'] })).toBe('fr');
    expect(chooseLang({ query: null, userLocale: 'he', saved: 'ru', browser: ['es'] })).toBe('he');
    expect(chooseLang({ saved: 'ru', browser: ['es'] })).toBe('ru');
    expect(chooseLang({ browser: ['xx', 'pt-BR'] })).toBe('pt');
    expect(chooseLang({})).toBe('en');
  });
});

describe('setLang', () => {
  it('loads the language, then sets the document direction and language', async () => {
    await setLang('he');
    expect(document.documentElement.lang).toBe('he');
    expect(document.documentElement.dir).toBe('rtl');
    expect(translate('he', 'login.submit')).toBe('התחברו');
    await setLang('en');
    expect(document.documentElement.dir).toBe('ltr');
  });
});

// The catalogue keys of the Starred and Recent pages and the star button. They were checked with the Home page's keys
// until decision 53 removed Home; each must exist in all seven catalogues, and the placeholders the code fills must stay.
describe('Starred, Recent and star catalogues', () => {
  const CATALOGUES: Record<string, Record<string, string>> = { en, es, fr, pt, ru, ar, he };
  const KEYS = Object.keys(en).filter(
    (k) => k === 'nav.starred' || k === 'nav.recent' || k.startsWith('starred.') || k.startsWith('recent.') || k.startsWith('star.'),
  );

  it('has English text for every key, and every catalogue has every key', () => {
    expect(KEYS.length).toBeGreaterThan(15);
    for (const k of KEYS) {
      expect(translate('en', k), k).not.toBe(k);
      for (const l of LANGUAGES) expect(CATALOGUES[l][k], `${l} ${k}`).toBeTypeOf('string');
    }
  });

  it('keeps the placeholders the code fills, in every language', () => {
    for (const l of LANGUAGES) {
      expect(CATALOGUES[l]['starred.on'], l).toContain('{date}');
      expect(CATALOGUES[l]['recent.opened'], l).toContain('{date}');
      expect(CATALOGUES[l]['recent.changed'], l).toContain('{date}');
      expect(CATALOGUES[l]['recent.removeName'], l).toContain('{name}');
    }
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { directionOf, matchLang, translate, translatePlural } from './i18n';

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

// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeAll, describe, expect, it } from 'vitest';
import { loadLang, LANGUAGES, translate, translatePlural, type Lang } from './i18n';
import en from '../i18n/en.json';
import es from '../i18n/es.json';
import fr from '../i18n/fr.json';
import pt from '../i18n/pt.json';
import ru from '../i18n/ru.json';
import ar from '../i18n/ar.json';
import he from '../i18n/he.json';

beforeAll(async () => {
  for (const l of LANGUAGES) await loadLang(l);
});

const CATALOGUES: Record<Lang, Record<string, string>> = { en, es, fr, pt, ru, ar, he };

// The keys of slice p2: Home, Starred, Recent, the star button and the storage notice. Each must exist in all seven
// catalogues, and the plural key in all six CLDR categories.
const KEYS = Object.keys(en).filter(
  (k) =>
    k === 'nav.home' ||
    k === 'nav.starred' ||
    k === 'nav.recent' ||
    k.startsWith('home.') ||
    k.startsWith('starred.') ||
    k.startsWith('recent.') ||
    k.startsWith('star.'),
);

const PLURAL_CATEGORIES = ['zero', 'one', 'two', 'few', 'many', 'other'];

describe('slice p2 catalogues', () => {
  it('has English text for every new key', () => {
    expect(KEYS.length).toBeGreaterThan(25);
    for (const k of KEYS) {
      if (k.startsWith('home.waiting.count.')) continue;
      expect(translate('en', k), k).not.toBe(k);
    }
  });

  it('has every new key, and the plural key in all six categories, in every language', () => {
    for (const l of LANGUAGES) {
      for (const k of KEYS) {
        if (k.startsWith('home.waiting.count.')) continue;
        expect(CATALOGUES[l][k], `${l} ${k}`).toBeTypeOf('string');
      }
      for (const c of PLURAL_CATEGORIES) {
        expect(CATALOGUES[l][`home.waiting.count_${c}`], `${l} waiting_${c}`).toBeTypeOf('string');
      }
    }
  });

  it('fills the waiting count in every language', () => {
    for (const l of LANGUAGES) {
      for (const n of [0, 1, 2, 3, 5, 11, 21, 100]) {
        const text = translatePlural(l, 'home.waiting.count', n);
        expect(text, `${l} ${n}`).not.toBe('home.waiting.count');
        if (n >= 3) expect(text).toContain(new Intl.NumberFormat(l).format(n));
      }
    }
  });

  it('keeps the placeholders the code fills', () => {
    for (const l of LANGUAGES) {
      expect(CATALOGUES[l]['home.storageNotice'], l).toContain('{percent}');
      expect(CATALOGUES[l]['starred.on'], l).toContain('{date}');
      expect(CATALOGUES[l]['recent.opened'], l).toContain('{date}');
      expect(CATALOGUES[l]['recent.changed'], l).toContain('{date}');
      expect(CATALOGUES[l]['recent.removeName'], l).toContain('{name}');
      expect(CATALOGUES[l]['star.button'], l).toContain('{name}');
    }
  });

  it('picks Russian forms by the count', () => {
    expect(translatePlural('ru', 'home.waiting.count', 1)).toContain('ждёт');
    expect(translatePlural('ru', 'home.waiting.count', 2)).toContain('запроса ждут');
    expect(translatePlural('ru', 'home.waiting.count', 5)).toContain('запросов ждут');
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeAll, describe, expect, it } from 'vitest';
import { loadLang, LANGUAGES, translate, translatePlural, type Lang } from '../i18n';
import en from '../../i18n/en.json';
import es from '../../i18n/es.json';
import fr from '../../i18n/fr.json';
import pt from '../../i18n/pt.json';
import ru from '../../i18n/ru.json';
import ar from '../../i18n/ar.json';
import he from '../../i18n/he.json';

beforeAll(async () => {
  for (const l of LANGUAGES) await loadLang(l);
});

const CATALOGUES: Record<Lang, Record<string, string>> = { en, es, fr, pt, ru, ar, he };

// The keys the storage pages use. Each must exist in every catalogue; the plural key in all six CLDR categories.
const KEYS = Object.keys(en).filter(
  (k) =>
    (k.startsWith('storage.') && !k.startsWith('storage.fileCount.')) ||
    k === 'nav.storage' ||
    k === 'nav.manageStorage' ||
    k === 'manage.storage.title' ||
    k === 'files.err.quotaUser' ||
    k === 'files.scope.user',
);

const PLURAL_CATEGORIES = ['zero', 'one', 'two', 'few', 'many', 'other'];

describe('storage catalogues', () => {
  it('has English text for every storage key', () => {
    expect(KEYS.length).toBeGreaterThan(40);
    for (const k of KEYS) expect(translate('en', k), k).not.toBe(k);
  });

  it('has every storage key, and the plural key in all six categories, in every language', () => {
    for (const l of LANGUAGES) {
      for (const k of KEYS) expect(CATALOGUES[l][k], `${l} ${k}`).toBeTypeOf('string');
      for (const c of PLURAL_CATEGORIES) {
        expect(CATALOGUES[l][`storage.fileCount_${c}`], `${l} fileCount_${c}`).toBeTypeOf('string');
      }
    }
  });

  it('fills the file count in every language', () => {
    for (const l of LANGUAGES) {
      for (const n of [0, 1, 2, 3, 11, 100]) {
        const text = translatePlural(l, 'storage.fileCount', n);
        expect(text, `${l} ${n}`).not.toBe('storage.fileCount');
        if (n >= 3) expect(text).toContain(new Intl.NumberFormat(l).format(n));
      }
    }
  });
});

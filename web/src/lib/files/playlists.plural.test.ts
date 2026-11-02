// SPDX-License-Identifier: AGPL-3.0-or-later

// The file count of a saved playlist is a plural in each catalogue, as the other counts are: one file reads with the
// singular form, not "1 files" (review of slice x4).
import { describe, expect, it } from 'vitest';
import { loadLang, translatePlural, type Lang } from '../i18n';

describe('the file count of a saved playlist', () => {
  it.each<[Lang, number, string]>([
    ['en', 1, '1 file'],
    ['en', 3, '3 files'],
    ['es', 1, '1 archivo'],
    ['es', 3, '3 archivos'],
    ['he', 1, '1 קובץ'],
    ['he', 3, '3 קבצים'],
  ])('%s, %i: %s', async (lang, count, want) => {
    await loadLang(lang);
    expect(translatePlural(lang, 'files.playlists.count', count)).toBe(want);
  });
});

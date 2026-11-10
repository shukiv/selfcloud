// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from 'vitest';
import { formatBytes, formatDate, formatNumber } from './format';

// Every row of a listing formats its date and its size, so the formatters must not build an Intl object on each call
// (perf-1 section 6). The constructors are counted: a call that reuses an object builds nothing.
describe('the formatters reuse their Intl objects', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('builds at most one DateTimeFormat for fifty dates in one language', () => {
    formatDate('2026-10-01T10:00:00Z', 'en');
    const spy = vi.spyOn(Intl, 'DateTimeFormat');
    for (let i = 0; i < 50; i++) formatDate(`2026-10-0${(i % 9) + 1}T10:00:00Z`, 'en');
    expect(spy.mock.calls.length).toBe(0);
  });

  it('builds no NumberFormat for fifty sizes of one unit in one language', () => {
    formatBytes(3_100_000, 'en');
    const spy = vi.spyOn(Intl, 'NumberFormat');
    for (let i = 0; i < 50; i++) formatBytes(3_100_000 + i, 'en');
    expect(spy.mock.calls.length).toBe(0);
  });

  it('builds no NumberFormat for counts once the language has been used', () => {
    formatNumber(1, 'fr');
    const spy = vi.spyOn(Intl, 'NumberFormat');
    for (let i = 0; i < 50; i++) formatNumber(i, 'fr');
    expect(spy.mock.calls.length).toBe(0);
  });
});

// The cache is keyed by the language: a switch of language changes the output at once, and back again.
describe('a change of language changes the output at once', () => {
  const iso = '2026-10-01T10:00:00Z';

  it('formats a date in the language asked for, after another language was used', () => {
    const en = formatDate(iso, 'en');
    const fr = formatDate(iso, 'fr');
    expect(fr).not.toBe(en);
    expect(fr).toBe(new Intl.DateTimeFormat('fr', { dateStyle: 'medium', timeStyle: 'short' }).format(new Date(iso)));
    expect(formatDate(iso, 'en')).toBe(en);
  });

  it('formats a size in the language asked for, after another language was used', () => {
    const en = formatBytes(3_100_000, 'en');
    const es = formatBytes(3_100_000, 'es');
    expect(es).not.toBe(en);
    expect(formatBytes(3_100_000, 'en')).toBe(en);
    expect(formatBytes(3_100_000, 'es')).toBe(es);
  });

  it('formats a count in the language asked for, after another language was used', () => {
    expect(formatNumber(1234567, 'en')).toBe('1,234,567');
    expect(formatNumber(1234567, 'de')).toBe(new Intl.NumberFormat('de').format(1234567));
    expect(formatNumber(1234567, 'en')).toBe('1,234,567');
  });
});

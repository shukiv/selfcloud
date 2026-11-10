// SPDX-License-Identifier: AGPL-3.0-or-later

import { directionOf, isolate, matchLang } from '../i18n';

// Sizes and dates, in the language of the interface. Sizes use decimal units, the way Intl names them.
const UNITS = ['byte', 'kilobyte', 'megabyte', 'gigabyte', 'terabyte'] as const;

// Every row of a listing formats its date and its size, and building an Intl object costs far more than using one (perf-1
// section 6). The objects are kept by language and by every option that changes the output, so a change of language asks for
// a different object at once. The keys are few: the interface's languages, the units and two digit counts.
const formatters = new Map<string, Intl.DateTimeFormat | Intl.NumberFormat>();
function cached<T extends Intl.DateTimeFormat | Intl.NumberFormat>(key: string, make: () => T): T {
  let f = formatters.get(key) as T | undefined;
  if (f === undefined) {
    f = make();
    formatters.set(key, f);
  }
  return f;
}

// formatBytes returns a size such as "1.5 MB" for lang, with the unit named in that language.
export function formatBytes(bytes: number, lang: string): string {
  let v = Math.max(0, Number.isFinite(bytes) ? bytes : 0);
  let i = 0;
  while (v >= 1000 && i < UNITS.length - 1) {
    v /= 1000;
    i++;
  }
  const digits = i === 0 ? 0 : v < 10 ? 1 : 0;
  // Bytes are written in full ("9 bytes"); the larger units keep their short form ("2 kB").
  const display = i === 0 ? 'long' : 'short';
  const nf = cached(`bytes|${lang}|${i}|${display}|${digits}`, () =>
    new Intl.NumberFormat(lang, {
      style: 'unit',
      unit: UNITS[i],
      unitDisplay: display,
      maximumFractionDigits: digits,
    }),
  );
  // In a right-to-left language the size keeps its own order, digits then unit, inside the sentence (decision 67).
  const l = matchLang(lang);
  if (l === null || directionOf(l) !== 'rtl') return nf.format(v);
  // Hebrew's data puts a Latin unit first for exactly one ("MB", a right-to-left mark, "1"), which the isolate draws as
  // "MB1". Such a size is put back together digits first.
  const parts = nf.formatToParts(v);
  const unitAt = parts.findIndex((p) => p.type === 'unit');
  const numberAt = parts.findIndex((p) => p.type === 'integer');
  if (unitAt >= 0 && numberAt > unitAt && /^[A-Za-z]+$/.test(parts[unitAt].value)) {
    const number = parts
      .filter((p) => p.type !== 'unit' && p.type !== 'literal')
      .map((p) => p.value)
      .join('');
    return isolate(lang, `${number} ${parts[unitAt].value}`);
  }
  return isolate(lang, nf.format(v));
}

// formatNumber formats a count or a byte total with the locale's digits.
export function formatNumber(n: number, lang: string): string {
  return cached(`number|${lang}`, () => new Intl.NumberFormat(lang)).format(n);
}

// formatDate formats an RFC 3339 time for lang. A time that does not parse is shown as an empty string.
export function formatDate(iso: string | null | undefined, lang: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return cached(`date|${lang}`, () => new Intl.DateTimeFormat(lang, { dateStyle: 'medium', timeStyle: 'short' })).format(d);
}

// daysUntil is the whole days from now until iso, rounded up, never below 0.
export function daysUntil(iso: string, now: number = Date.now()): number {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return 0;
  return Math.max(0, Math.ceil((t - now) / 86_400_000));
}

// formatRate formats a transfer speed such as "3.2 MB/s"; a speed of 0 or less shows no value.
export function formatRate(bytesPerSecond: number, lang: string): string {
  if (!(bytesPerSecond > 0)) return '';
  return formatBytes(bytesPerSecond, lang);
}

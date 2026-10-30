// SPDX-License-Identifier: AGPL-3.0-or-later

// Sizes and dates, in the language of the interface. Sizes use decimal units, the way Intl names them.
const UNITS = ['byte', 'kilobyte', 'megabyte', 'gigabyte', 'terabyte'] as const;

// formatBytes returns a size such as "1.5 MB" for lang, with the unit named in that language.
export function formatBytes(bytes: number, lang: string): string {
  let v = Math.max(0, Number.isFinite(bytes) ? bytes : 0);
  let i = 0;
  while (v >= 1000 && i < UNITS.length - 1) {
    v /= 1000;
    i++;
  }
  const digits = i === 0 ? 0 : v < 10 ? 1 : 0;
  return new Intl.NumberFormat(lang, {
    style: 'unit',
    unit: UNITS[i],
    unitDisplay: 'short',
    maximumFractionDigits: digits,
  }).format(v);
}

// formatNumber formats a count or a byte total with the locale's digits.
export function formatNumber(n: number, lang: string): string {
  return new Intl.NumberFormat(lang).format(n);
}

// formatDate formats an RFC 3339 time for lang. A time that does not parse is shown as an empty string.
export function formatDate(iso: string | null | undefined, lang: string): string {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return new Intl.DateTimeFormat(lang, { dateStyle: 'medium', timeStyle: 'short' }).format(d);
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

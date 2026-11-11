// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Setting } from "./api";
import { durationSeconds, sizeBytes } from "./units";

// The words for a value and for a range are the language's own unit names, from Intl, so no unit needs a string of its own.

type IntlUnit = "kilobyte" | "megabyte" | "gigabyte" | "terabyte" | "minute" | "hour" | "day";

function withUnit(n: number, unit: IntlUnit, lang: string): string {
  // A size is "64 MB" and a duration "3 days": the short form for bytes, the long one for time.
  const display = unit.endsWith("byte") ? "short" : "long";
  if (display === "short") {
    // Some languages write one byte unit before its number ("MB1" in Hebrew), which a sentence cannot hold. The number comes first,
    // with the unit's own word as it is written beside any other number.
    const fmt = new Intl.NumberFormat(lang, { style: "unit", unit, unitDisplay: display, maximumFractionDigits: 4 });
    const word = fmt.formatToParts(2).find((p) => p.type === "unit")?.value ?? unit;
    return `${new Intl.NumberFormat(lang, { maximumFractionDigits: 4 }).format(n)} ${word}`;
  }
  return new Intl.NumberFormat(lang, { style: "unit", unit, unitDisplay: display, maximumFractionDigits: 4 }).format(n);
}

const KIB = 1024;
const MIB = KIB ** 2;
const GIB = KIB ** 3;
const TIB = KIB ** 4;

// formatSizeBytes writes a size in the largest unit that divides it. The page's MB and GB are the binary units.
export function formatSizeBytes(bytes: number, lang: string): string {
  if (bytes >= TIB && bytes % TIB === 0) return withUnit(bytes / TIB, "terabyte", lang);
  if (bytes >= GIB && bytes % GIB === 0) return withUnit(bytes / GIB, "gigabyte", lang);
  if (bytes >= MIB && bytes % MIB === 0) return withUnit(bytes / MIB, "megabyte", lang);
  if (bytes % KIB === 0) return withUnit(bytes / KIB, "kilobyte", lang);
  return withUnit(bytes / MIB, "megabyte", lang);
}

// formatSeconds writes a duration in the largest unit that divides it.
export function formatSeconds(seconds: number, lang: string): string {
  if (seconds >= 86400 && seconds % 86400 === 0) return withUnit(seconds / 86400, "day", lang);
  if (seconds >= 3600 && seconds % 3600 === 0) return withUnit(seconds / 3600, "hour", lang);
  return withUnit(seconds / 60, "minute", lang);
}

// formatBound writes a bound of a field's range: bytes, seconds or a plain number, by the field's type.
export function formatBound(type: Setting["type"], n: number, lang: string): string {
  if (type === "bytes") return formatSizeBytes(n, lang);
  if (type === "duration") return formatSeconds(n, lang);
  return new Intl.NumberFormat(lang).format(n);
}

// formatValue writes a setting's value, as the API gives it, for a sentence. on and off are the words for a switch and none is
// the word for an empty text.
export function formatValue(type: Setting["type"], value: Setting["value"], lang: string, words: { on: string; off: string; none: string }): string {
  switch (type) {
    case "bool":
      return value ? words.on : words.off;
    case "int":
      return new Intl.NumberFormat(lang).format(value as number);
    case "bytes":
      return formatSizeBytes(sizeBytes(value as string | number), lang);
    case "duration":
      return formatSeconds(durationSeconds(value as string), lang);
    default:
      return value === "" ? words.none : String(value);
  }
}

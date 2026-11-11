// SPDX-License-Identifier: AGPL-3.0-or-later

// The settings API writes a size in the server's normal form ("64MiB", "100GiB") and a duration in Go's ("168h0m0s"). The page
// shows them as a number and a unit a person picks, and sends them back as strings the server reads.

export type SizeUnit = "MB" | "GB";
export type DurationUnit = "minutes" | "hours" | "days";
export interface Amount<U extends string> {
  // value is null while the box is empty.
  value: number | null;
  unit: U;
}

const KIB = 1024;
const MIB = KIB * 1024;
const GIB = MIB * 1024;
const MINUTE = 60;
const HOUR = 3600;
const DAY = 86400;

const BYTE_UNITS: Record<string, number> = {
  "": 1,
  b: 1,
  kb: 1e3,
  mb: 1e6,
  gb: 1e9,
  tb: 1e12,
  kib: KIB,
  mib: MIB,
  gib: GIB,
  tib: GIB * 1024,
};

// round4 keeps a shown value to four decimals, so a binary size in another unit does not show float noise.
const round4 = (n: number): number => Math.round(n * 10000) / 10000;

// sizeBytes reads a size as the API writes it: a whole number of bytes, or a number with a unit. KB, MB, GB and TB are powers of
// 1000, and KiB, MiB, GiB and TiB powers of 1024, as the server reads them.
export function sizeBytes(text: string | number): number {
  if (typeof text === "number") return text;
  const m = /^\s*(\d+(?:\.\d+)?)\s*([a-zA-Z]*)\s*$/.exec(text);
  const mult = m ? BYTE_UNITS[m[2].toLowerCase()] : undefined;
  if (!m || mult === undefined) return Number.NaN;
  return Math.round(Number(m[1]) * mult);
}

// splitSize shows a size in GB when it is a whole number of gibibytes, else in MB. The page's MB and GB are the binary units
// (MiB and GiB), the units the server writes a size in, so a value is shown and sent without rounding.
export function splitSize(text: string | number): Amount<SizeUnit> {
  const bytes = sizeBytes(text);
  if (bytes > 0 && bytes % GIB === 0) return { value: bytes / GIB, unit: "GB" };
  return { value: round4(bytes / MIB), unit: "MB" };
}

// sizeText is the string sent to the server for a size, or null while the box is empty.
export function sizeText(a: Amount<SizeUnit>): string | null {
  if (a.value === null) return null;
  return `${a.value}${a.unit === "GB" ? "GiB" : "MiB"}`;
}

// durationSeconds reads a duration as Go writes it ("168h0m0s", "5m0s", "90s").
export function durationSeconds(text: string): number {
  if (!/^(?:\d+(?:\.\d+)?[hms])+$/.test(text)) return Number.NaN;
  let total = 0;
  for (const m of text.matchAll(/(\d+(?:\.\d+)?)([hms])/g)) {
    total += Number(m[1]) * (m[2] === "h" ? HOUR : m[2] === "m" ? MINUTE : 1);
  }
  return Math.round(total);
}

// splitDuration shows whole days in days, whole hours in hours, and anything else in minutes. Zero is shown in days.
export function splitDuration(text: string): Amount<DurationUnit> {
  const s = durationSeconds(text);
  if (s === 0) return { value: 0, unit: "days" };
  if (s % DAY === 0) return { value: s / DAY, unit: "days" };
  if (s % HOUR === 0) return { value: s / HOUR, unit: "hours" };
  return { value: round4(s / MINUTE), unit: "minutes" };
}

// durationText is the string sent to the server for a duration, or null while the box is empty. The server has no day unit, so
// days go as hours.
export function durationText(a: Amount<DurationUnit>): string | null {
  if (a.value === null) return null;
  const s = Math.round(a.value * (a.unit === "days" ? DAY : a.unit === "hours" ? HOUR : MINUTE));
  if (s % HOUR === 0) return `${s / HOUR}h`;
  if (s % MINUTE === 0) return `${s / MINUTE}m`;
  return `${s}s`;
}

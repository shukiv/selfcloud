// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from '../store';

// maxExpiryDays is the longest a share may run, in whole days, from GET /info. 0 means no cap. It is an
// installation setting, not a person's data, so it survives a change of session: Boot sets it when /info loads.
export const maxExpiryDays = writable(0);

// expiryLimit is the latest date a share may end on, counted from now, or null when there is no cap.
export function expiryLimit(days: number, now: Date): Date | null {
  if (days <= 0) return null;
  return new Date(now.getTime() + days * 24 * 60 * 60 * 1000);
}

// expiryFromInputCapped is expiryFromInput with the installation's cap applied: the end of the day picked, pulled back
// to now plus maxDays when that day reaches past it. The server refuses an expiry past the cap, and the date picker
// offers the day the cap ends on, so the last day would otherwise be refused. A cap of 0 means none.
export function expiryFromInputCapped(value: string, now: Date, maxDays: number): string | null {
  const iso = expiryFromInput(value);
  if (iso === null || maxDays <= 0) return iso;
  const latest = now.getTime() + maxDays * 24 * 60 * 60 * 1000;
  const end = new Date(iso).getTime();
  return new Date(Math.min(end, latest)).toISOString();
}

// dateInputValue formats a date as the value of a date input (YYYY-MM-DD, in local time).
export function dateInputValue(d: Date): string {
  const pad = (n: number) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// expiryFromInput turns a date input's value into the instant the share ends: the end of that local day.
// An empty value means the share never ends. A malformed value is null.
export function expiryFromInput(value: string): string | null {
  if (value === '') return null;
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!m) return null;
  const end = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), 23, 59, 59, 0);
  return end.toISOString();
}

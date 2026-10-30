// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from '../store';

// maxExpiryDays is the longest a share may run, in whole days, from GET /info. 0 means no cap.
export const maxExpiryDays = writable(0);

// expiryLimit is the latest date a share may end on, counted from now, or null when there is no cap.
export function expiryLimit(days: number, now: Date): Date | null {
  if (days <= 0) return null;
  return new Date(now.getTime() + days * 24 * 60 * 60 * 1000);
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

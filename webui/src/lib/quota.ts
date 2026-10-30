// SPDX-License-Identifier: AGPL-3.0-or-later

// Storage limits as the administration pages write them. The meter and the sizes elsewhere use decimal units
// (lib/files/format.ts): 1 GB is 1000 MB, so a limit entered as "50 GB" reads back as "50 GB".

export type LimitUnit = 'GB' | 'TB';

const FACTOR: Record<LimitUnit, number> = { GB: 1e9, TB: 1e12 };

// limitBytes turns an amount and its unit into bytes. An amount is a positive whole or decimal number with at
// most two decimals, so 1.5 TB is allowed. Anything else is null. The result is a safe integer.
export function limitBytes(amount: string, unit: LimitUnit): number | null {
  const v = amount.trim();
  if (!/^\d+(\.\d{1,2})?$/.test(v)) return null;
  const n = Number(v);
  if (!Number.isFinite(n) || n <= 0) return null;
  const bytes = Math.round(n * FACTOR[unit]);
  return Number.isSafeInteger(bytes) ? bytes : null;
}

// splitLimit names a stored limit in the largest unit it fills exactly enough for the text field: the amount
// as a plain number and its unit. A limit of 0 means no limit and is not split (the caller shows "No limit").
export function splitLimit(bytes: number): { amount: string; unit: LimitUnit } {
  const unit: LimitUnit = bytes >= FACTOR.TB ? 'TB' : 'GB';
  const amount = Math.round((bytes / FACTOR[unit]) * 100) / 100;
  return { amount: String(amount), unit };
}

// SPDX-License-Identifier: AGPL-3.0-or-later

// The storage pages' rules, as plain functions: what the meter shows, how a limit is read from a text field and
// written back, and how lists are ordered and paged. The server decides every limit; these only shape what the
// pages send and show.
import { limitBytes, type LimitUnit } from '../quota';

// MeStorage is the caller's own storage, GET /api/v1/me `storage`: its My files and the limit that applies to it.
export interface MeStorage {
  used_bytes: number;
  reserved_bytes: number;
  quota_bytes: number | null;
  available_bytes: number | null;
}

// The meter shows what the account or person holds: the files and the bytes unfinished uploads have reserved.
export function heldBytes(s: { used_bytes: number; reserved_bytes: number }): number {
  return Math.max(0, s.used_bytes) + Math.max(0, s.reserved_bytes);
}

// meterLevel gives the meter's state, with the same thresholds as UsageMeter: 80% warns, 95% is danger. A null or
// zero limit is no limit, and has no level.
export function meterLevel(held: number, quota: number | null): 'ok' | 'warning' | 'danger' | 'none' {
  if (quota === null || quota <= 0) return 'none';
  const share = Math.min(1, held / quota);
  if (share >= 0.95) return 'danger';
  if (share >= 0.8) return 'warning';
  return 'ok';
}

// availableOf is what a limit still leaves free: null with no limit, never below zero.
export function availableOf(held: number, quota: number | null): number | null {
  if (quota === null || quota <= 0) return null;
  return Math.max(0, quota - held);
}

// LimitText is what a limit text field holds: an amount and its unit, or nothing.
export interface LimitText {
  amount: string;
  unit: LimitUnit;
}

// parseUserLimit reads a person's limit. Empty means the account default, which the server stores as null. "0" means
// no limit. Anything else is an amount in the unit, as limitBytes reads it. An invalid text gives ok false.
export function parseUserLimit(text: LimitText): { ok: true; value: number | null } | { ok: false } {
  const a = text.amount.trim();
  if (a === '') return { ok: true, value: null };
  if (a === '0') return { ok: true, value: 0 };
  const bytes = limitBytes(a, text.unit);
  return bytes === null ? { ok: false } : { ok: true, value: bytes };
}

// parseAccountLimit reads the default limit for each person. The default cannot follow itself, so empty and "0"
// both mean no default limit (0). Anything else is an amount, as limitBytes reads it.
export function parseAccountLimit(text: LimitText): { ok: true; value: number } | { ok: false } {
  const a = text.amount.trim();
  if (a === '' || a === '0') return { ok: true, value: 0 };
  const bytes = limitBytes(a, text.unit);
  return bytes === null ? { ok: false } : { ok: true, value: bytes };
}

// limitTextFor fills the text field from a stored limit. A null (follow the default) and a 0 (no limit) both leave
// the field empty; the caller shows which of the two it is.
export function limitTextFor(stored: number | null): LimitText {
  if (stored === null || stored <= 0) return { amount: '', unit: 'GB' };
  const unit: LimitUnit = stored >= 1e12 ? 'TB' : 'GB';
  const amount = Math.round((stored / (unit === 'TB' ? 1e12 : 1e9)) * 100) / 100;
  return { amount: String(amount), unit };
}

// Use-ordered lists: most held first, then by ID so a tie keeps a fixed order.
export interface Held {
  id: string;
  used_bytes: number;
  reserved_bytes: number;
}

export function sortByUse<T extends Held>(items: readonly T[]): T[] {
  return [...items].sort((a, b) => heldBytes(b) - heldBytes(a) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0));
}

// mergePage adds a later page to the rows already shown. A row that is already there keeps its place, so a page
// that overlaps the last one does not repeat a row.
export function mergePage<T extends { id: string }>(shown: readonly T[], more: readonly T[]): T[] {
  const seen = new Set(shown.map((x) => x.id));
  const out = [...shown];
  for (const x of more) {
    if (!seen.has(x.id)) {
      seen.add(x.id);
      out.push(x);
    }
  }
  return out;
}

// pageQuery builds the query string of one page: the limit and, after the first page, the cursor. Each value is
// encoded, so a cursor from the server cannot change the query.
export function pageQuery(params: { limit?: number; cursor?: string | null; extra?: Record<string, string | undefined> }): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params.extra ?? {})) {
    if (v !== undefined && v !== '') q.set(k, v);
  }
  if (params.limit !== undefined) q.set('limit', String(params.limit));
  if (params.cursor) q.set('cursor', params.cursor);
  const s = q.toString();
  return s === '' ? '' : `?${s}`;
}

// The size of one page of each list the storage pages read.
export const LARGEST_PAGE = 20;
export const PEOPLE_PAGE = 50;

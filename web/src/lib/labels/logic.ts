// SPDX-License-Identifier: AGPL-3.0-or-later

import type { Lang } from '../i18n';
import { ApiError } from '../apiError';
import type { LabelChoice, LabelField, LabelType, NodeLabel } from './api';

// The limits the server checks, repeated so a form can say so first. The server still decides.
export const LABEL_NAME_MAX = 255;
export const LABEL_TEXT_MAX = 500;
export const LABEL_CHOICES_MAX = 100;

export const LABEL_TYPES: LabelType[] = ['text', 'number', 'date', 'user', 'choice'];

// parseNumber reads a number the way the editor typed it: a decimal comma is accepted, and anything that is not a finite
// number is null.
export function parseNumber(input: string): number | null {
  const s = input.trim().replace(',', '.');
  if (s === '') return null;
  const n = Number(s);
  return Number.isFinite(n) ? n : null;
}

// isDate reports whether s is a real calendar date written YYYY-MM-DD.
export function isDate(s: string): boolean {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(s);
  if (!m) return false;
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const t = new Date(Date.UTC(y, mo - 1, d));
  return t.getUTCFullYear() === y && t.getUTCMonth() === mo - 1 && t.getUTCDate() === d;
}

// showNumber formats a stored number for the reader, in the locale's digits and separators.
export function showNumber(n: number, lang: Lang): string {
  return new Intl.NumberFormat(lang).format(n);
}

// isChoice and isUser tell the kinds of a value apart: a choice has a name and an archived flag, a user an ID.
export function isChoice(v: unknown): v is LabelChoice {
  return typeof v === 'object' && v !== null && 'archived' in v && 'name' in v;
}

export function isUser(v: unknown): v is { id: string; name?: string } {
  return typeof v === 'object' && v !== null && !('archived' in v) && 'id' in v;
}

// draftChoices is a field's choices as the editor shows them, with the rows the editor added at the end.
export interface ChoiceRow {
  id?: string;
  name: string;
  archived: boolean;
}

// choiceProblem says what is wrong with the rows of a choice field, or null when they are fine: a row needs a name,
// two rows may not share one (compared without case and spaces), and the count stays within the limit.
export function choiceProblem(rows: ChoiceRow[]): 'empty' | 'duplicate' | 'limit' | null {
  if (rows.length > LABEL_CHOICES_MAX) return 'limit';
  const seen = new Set<string>();
  for (const r of rows) {
    const key = r.name.trim().toLocaleLowerCase();
    if (key === '') return 'empty';
    if (seen.has(key)) return 'duplicate';
    seen.add(key);
  }
  return null;
}

// nameProblem says what is wrong with a field name, or null: it is required and within the limit.
export function nameProblem(name: string): 'empty' | 'long' | null {
  const s = name.trim();
  if (s === '') return 'empty';
  if (s.length > LABEL_NAME_MAX) return 'long';
  return null;
}

// textProblem says what is wrong with a text value, or null when it may be sent.
export function textProblem(text: string): 'empty' | 'long' | null {
  if (text.trim() === '') return 'empty';
  if ([...text].length > LABEL_TEXT_MAX) return 'long';
  return null;
}

// labelFailure is the code and reason the server gave for a refused label change, when it gave one. The pages show a
// sentence for these codes, and the generic failure text for any other.
export function labelFailure(err: unknown): string | null {
  if (!(err instanceof ApiError)) return null;
  const reason = typeof err.details.reason === 'string' ? err.details.reason : '';
  if (err.code === 'conflict' && ['name_taken', 'type_locked', 'limit'].includes(reason)) return reason;
  if (err.code === 'forbidden' && reason === 'restricted_label') return reason;
  return null;
}

// visibleFields lines the fields up with the values the server sent. The server leaves a restricted field out of a
// reader's answer, so a restricted field appears here only when a value for it came back.
export function visibleFields(fields: LabelField[], values: NodeLabel[]): NodeLabel[] {
  const byField = new Map(values.map((v) => [v.field_id, v]));
  return fields
    .filter((f) => !f.restricted || byField.has(f.id))
    .map((f) => byField.get(f.id) ?? { field_id: f.id, name: f.name, type: f.type, restricted: f.restricted, value: null });
}

// activeChoices are the choices a value may take now: the archived ones are kept for the values that use them.
export function activeChoices(choices: LabelChoice[]): LabelChoice[] {
  return choices.filter((c) => !c.archived);
}

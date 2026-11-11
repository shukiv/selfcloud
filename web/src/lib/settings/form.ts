// SPDX-License-Identifier: AGPL-3.0-or-later

import { ApiError } from "../apiError";
import type { Setting } from "./api";
import { durationSeconds, durationText, sizeBytes, sizeText, splitDuration, splitSize, type Amount, type DurationUnit, type SizeUnit } from "./units";

// What a person has typed into one field: a switch, a text, a count, or a number with its unit. A box that was emptied holds null.
export type Draft = boolean | string | number | null | Amount<SizeUnit> | Amount<DurationUnit>;

// FieldEdit is an edit of one field. reset is set by the Reset icon: the stored value is to be removed, so the default applies.
export interface FieldEdit {
  value: Draft;
  reset: boolean;
}

// Edits holds only the fields a person has touched. A field with no entry shows the server's value as it is now.
export type Edits = Record<string, FieldEdit | undefined>;

type Wire = boolean | number | string;

// draftOf is a value as the box shows it.
export function draftOf(s: Pick<Setting, "type" | "value">): Draft {
  switch (s.type) {
    case "bytes":
      return splitSize(s.value as string | number);
    case "duration":
      return splitDuration(s.value as string);
    default:
      return s.value;
  }
}

// wireOf is the value the API is sent for a draft, or null while the box is incomplete.
export function wireOf(type: Setting["type"], d: Draft): Wire | null {
  switch (type) {
    case "bytes":
      return sizeText(d as Amount<SizeUnit>);
    case "duration":
      return durationText(d as Amount<DurationUnit>);
    case "int":
      return typeof d === "number" ? d : null;
    case "bool":
      return typeof d === "boolean" ? d : null;
    default:
      return typeof d === "string" ? d : null;
  }
}

// sameValue compares two values of one type as numbers where they are sizes or durations, so 1 day and 24 hours are the same.
function sameValue(type: Setting["type"], a: Wire, b: Wire): boolean {
  if (type === "bytes") return sizeBytes(a as string | number) === sizeBytes(b as string | number);
  if (type === "duration") return durationSeconds(a as string) === durationSeconds(b as string);
  return a === b;
}

// isChanged says whether an edit changes the field. A locked field never changes; a reset changes a field that has a value
// stored; an incomplete box counts as changed, so the page asks for a number.
export function isChanged(s: Setting, e: FieldEdit | undefined): boolean {
  if (e === undefined || s.locked) return false;
  if (e.reset) return s.source === "settings";
  const w = wireOf(s.type, e.value);
  if (w === null) return true;
  return !sameValue(s.type, w, s.value);
}

export interface SectionChange {
  set: Record<string, Wire>;
  reset: string[];
  // incomplete names the fields whose box is empty: they are not sent.
  incomplete: string[];
}

// sectionChange is what Save of one section sends: its changed keys only.
export function sectionChange(all: Setting[], keys: string[], edits: Edits): SectionChange {
  const out: SectionChange = { set: {}, reset: [], incomplete: [] };
  for (const key of keys) {
    const s = all.find((x) => x.key === key);
    const e = edits[key];
    if (s === undefined || e === undefined || !isChanged(s, e)) continue;
    if (e.reset) {
      out.reset.push(key);
      continue;
    }
    const w = wireOf(s.type, e.value);
    if (w === null) out.incomplete.push(key);
    else out.set[key] = w;
  }
  return out;
}

// messagesFor are the messages that name the key as a whole word. The server's messages are in the style of the config file's
// errors, and each names the keys it is about.
export function messagesFor(key: string, messages: string[]): string[] {
  const escaped = key.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  const named = new RegExp(`(^|[^\\w.])${escaped}(?![\\w.])`);
  return messages.filter((m) => named.test(m));
}

export type Refusal =
  | { kind: "invalid"; fields: Record<string, string | null>; unmatched: string[] }
  | { kind: "locked"; keys: string[] }
  | { kind: "other"; error: unknown };

const strings = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === "string") : []);

// refusal reads a refused change. A value the server does not accept (400 invalid_setting) names every refused key, each with
// the messages about it, or null when none names it. A key that became locked (409 setting_locked) is named in keys. Anything
// else is passed through to the general error.
export function refusal(err: unknown): Refusal {
  if (err instanceof ApiError) {
    if (err.status === 400 && err.details.reason === "invalid_setting") {
      const keys = strings(err.details.keys);
      const errors = strings(err.details.errors);
      const fields: Record<string, string | null> = {};
      for (const key of keys) {
        const own = messagesFor(key, errors);
        fields[key] = own.length > 0 ? own.join(" ") : null;
      }
      // A message that names none of the keys would be lost: it is kept for the section's notice.
      const unmatched = errors.filter((m) => !keys.some((key) => messagesFor(key, [m]).length > 0));
      return { kind: "invalid", fields, unmatched };
    }
    if (err.status === 409 && err.details.reason === "setting_locked") {
      return { kind: "locked", keys: strings(err.details.keys) };
    }
  }
  return { kind: "other", error: err };
}

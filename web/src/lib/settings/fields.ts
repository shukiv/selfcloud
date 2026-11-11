// SPDX-License-Identifier: AGPL-3.0-or-later

import type { SettingType } from "./api";

// The page's sections and the settings in each, in the order they are drawn (decision 143). Each field's range is the floor and
// cap the server holds a value set through the API to (decision 139); the server stays the judge, and the page only states it.

const MIB = 1024 ** 2;
const GIB = 1024 ** 3;
const TIB = 1024 ** 4;
const MINUTE = 60;
const HOUR = 3600;
const DAY = 86400;

// Range is in bytes for a size, in seconds for a duration, and in the number itself for a count. zero says that 0 is allowed too,
// and means no limit.
export interface Range {
  min: number;
  max: number;
  zero?: boolean;
}

export interface FieldSpec {
  key: string;
  type: SettingType;
  // The strings of the field are settings.field.<id>.label and settings.field.<id>.help, with id the key.
  range?: Range;
  // notes are the keys of the sentences that follow the help, for a rule the field shares with others.
  notes?: string[];
}

export interface Group {
  // title is the catalogue key of the group's heading, or null for a section that has one group and needs none.
  title: string | null;
  fields: FieldSpec[];
}

export type SectionId = "uploads" | "sharing" | "signin" | "about";

export interface SectionSpec {
  id: SectionId;
  groups: Group[];
}

const LOWERING = "settings.note.lowering";

export const SECTIONS: SectionSpec[] = [
  {
    id: "uploads",
    groups: [
      {
        title: null,
        fields: [
          { key: "upload.max_file_size", type: "bytes", range: { min: MIB, max: 16 * TIB } },
          { key: "upload.max_chunk_size", type: "bytes", range: { min: MIB, max: 4 * GIB } },
          { key: "upload.max_small_file_size", type: "bytes", range: { min: 64 * 1024, max: GIB } },
          { key: "storage.min_free", type: "bytes", range: { min: 256 * MIB, max: TIB } },
        ],
      },
    ],
  },
  {
    id: "sharing",
    groups: [
      {
        title: "settings.group.links",
        fields: [
          { key: "sharing.max_expiry", type: "duration", range: { min: HOUR, max: 3650 * DAY, zero: true } },
          { key: "links.default_ttl", type: "duration", range: { min: MINUTE, max: 7 * DAY } },
          { key: "links.max_ttl", type: "duration", range: { min: MINUTE, max: 7 * DAY } },
        ],
      },
      {
        title: "settings.group.trash",
        fields: [
          { key: "trash.retention", type: "duration", range: { min: DAY, max: 3650 * DAY } },
          { key: "versions.max_count", type: "int", range: { min: 1, max: 10000, zero: true } },
          { key: "versions.max_age", type: "duration", range: { min: DAY, max: 3650 * DAY, zero: true } },
        ],
      },
    ],
  },
  {
    id: "signin",
    groups: [
      {
        title: "settings.group.sessions",
        fields: [
          { key: "sessions.idle_timeout", type: "duration", range: { min: 15 * MINUTE, max: 30 * DAY }, notes: ["settings.note.office", LOWERING] },
          { key: "sessions.max_age", type: "duration", range: { min: HOUR, max: 90 * DAY }, notes: [LOWERING] },
          { key: "sessions.admin_idle_timeout", type: "duration", range: { min: 5 * MINUTE, max: 12 * HOUR }, notes: [LOWERING] },
          { key: "sessions.admin_max_age", type: "duration", range: { min: HOUR, max: 7 * DAY }, notes: [LOWERING] },
          { key: "users.verification_ttl", type: "duration", range: { min: 15 * MINUTE, max: 30 * DAY } },
          { key: "users.reset_ttl", type: "duration", range: { min: 5 * MINUTE, max: DAY } },
        ],
      },
      {
        title: "settings.group.search",
        fields: [
          { key: "search.enabled", type: "bool" },
          { key: "search.office_text", type: "bool" },
        ],
      },
      {
        title: "settings.group.office",
        fields: [
          { key: "office.enabled", type: "bool" },
          { key: "office.discovery_url", type: "string" },
          { key: "office.lock_minutes", type: "int", range: { min: 1, max: 30 } },
          { key: "office.max_file_bytes", type: "bytes", range: { min: MIB, max: 4 * GIB } },
        ],
      },
      {
        title: "settings.group.admin",
        fields: [{ key: "admin.tenant_access", type: "bool" }],
      },
    ],
  },
];

// fieldKeys are the keys of a section's fields, in order.
export function fieldKeys(section: SectionSpec): string[] {
  return section.groups.flatMap((g) => g.fields.map((f) => f.key));
}

export function specOf(key: string): FieldSpec | undefined {
  for (const s of SECTIONS) for (const g of s.groups) for (const f of g.fields) if (f.key === key) return f;
  return undefined;
}

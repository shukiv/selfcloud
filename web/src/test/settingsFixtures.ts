// SPDX-License-Identifier: AGPL-3.0-or-later

// Fixtures for the Settings page tests: the answer of GET /api/v1/settings with every setting at its built-in default. Only the
// tests import this file.
import type { Fact, Picture, Setting, SettingsAnswer } from "../lib/settings/api";

type Row = [key: string, type: Setting["type"], value: Setting["value"], extra?: Partial<Setting>];

const ROWS: Row[] = [
  ["admin.tenant_access", "bool", false],
  ["links.default_ttl", "duration", "5m0s"],
  ["links.max_ttl", "duration", "24h0m0s"],
  ["office.discovery_url", "string", "", { locked: true, restart: true }],
  ["office.enabled", "bool", false, { locked: true, restart: true }],
  ["office.lock_minutes", "int", 30],
  ["office.max_file_bytes", "bytes", "50MiB"],
  ["search.enabled", "bool", true],
  ["search.office_text", "bool", true],
  ["sessions.admin_idle_timeout", "duration", "12h0m0s"],
  ["sessions.admin_max_age", "duration", "168h0m0s"],
  ["sessions.idle_timeout", "duration", "168h0m0s"],
  ["sessions.max_age", "duration", "720h0m0s"],
  ["sharing.max_expiry", "duration", "8760h0m0s"],
  ["storage.min_free", "bytes", "1GiB"],
  ["trash.retention", "duration", "720h0m0s"],
  ["ui.brand.accent", "string", "#053D8E"],
  ["ui.brand.name", "string", "SelfCloud"],
  ["ui.brand.palette.dark.accent", "string", ""],
  ["ui.brand.palette.dark.accent_border", "string", ""],
  ["ui.brand.palette.dark.accent_fill", "string", ""],
  ["ui.brand.palette.dark.accent_fill_active", "string", ""],
  ["ui.brand.palette.dark.accent_fill_hover", "string", ""],
  ["ui.brand.palette.dark.accent_soft", "string", ""],
  ["ui.brand.palette.dark.accent_text", "string", ""],
  ["ui.brand.palette.light.accent", "string", ""],
  ["ui.brand.palette.light.accent_border", "string", ""],
  ["ui.brand.palette.light.accent_fill", "string", ""],
  ["ui.brand.palette.light.accent_fill_active", "string", ""],
  ["ui.brand.palette.light.accent_fill_hover", "string", ""],
  ["ui.brand.palette.light.accent_soft", "string", ""],
  ["ui.brand.palette.light.accent_text", "string", ""],
  ["ui.brand.sidebar.active", "string", ""],
  ["ui.brand.sidebar.bg", "string", ""],
  ["ui.brand.sidebar.caption", "string", ""],
  ["ui.brand.sidebar.on_active", "string", ""],
  ["ui.brand.sidebar.text", "string", ""],
  ["upload.max_chunk_size", "bytes", "64MiB"],
  ["upload.max_file_size", "bytes", "100GiB"],
  ["upload.max_small_file_size", "bytes", "16MiB"],
  ["users.reset_ttl", "duration", "1h0m0s"],
  ["users.verification_ttl", "duration", "72h0m0s"],
  ["versions.max_age", "duration", "720h0m0s"],
  ["versions.max_count", "int", 100],
];

export const FACTS: Fact[] = [
  { key: "listen", value: ":8080", source: "file" },
  { key: "log.level", value: "info", source: "default" },
  { key: "public_url", value: "https://drive.example.com", source: "file" },
  { key: "trusted_proxies", value: ["10.0.0.0/8", "192.168.0.0/16"], source: "file" },
  { key: "ui.enabled", value: true, source: "default" },
  { key: "webhooks.allow_http", value: false, source: "default" },
];

// settingsAnswer is the answer with every setting at its default. over changes a setting by key: a stored value is
// { value, source: "settings" }, and a key the file writes is { value, source: "file", locked: true }.
export const PICTURES: Picture[] = [
  { key: "ui.brand.icon", state: "none", locked: false },
  { key: "ui.brand.logo", state: "none", locked: false },
];

export function settingsAnswer(over: Record<string, Partial<Setting>> = {}, facts: Fact[] = FACTS, pictures: Picture[] = PICTURES): SettingsAnswer {
  return {
    pictures,
    settings: ROWS.map(([key, type, value, extra]) => ({
      key,
      type,
      value,
      default: value,
      source: "default",
      locked: false,
      restart: false,
      ...extra,
      ...over[key],
    })),
    facts,
  };
}

export const KEYS = ROWS.map((r) => r[0]);

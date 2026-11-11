// SPDX-License-Identifier: AGPL-3.0-or-later

import { api } from "../api";

// The settings an installation admin changes while the server runs (decision 139). type says how the value is written: a
// JSON boolean, a JSON integer, or a string (a duration such as "168h0m0s", a size such as "64MiB", or text).
export type SettingType = "bool" | "int" | "duration" | "bytes" | "string";
export type Source = "default" | "settings" | "file" | "env";

export interface Setting {
  key: string;
  type: SettingType;
  value: boolean | number | string;
  default: boolean | number | string;
  source: Source;
  // locked is true when the config file or the environment writes the key, or the server reads it only at start.
  locked: boolean;
  // restart marks a setting the server reads only at start (the office pair).
  restart: boolean;
}

// A fact is a read-only value of the configuration, never a secret.
export interface Fact {
  key: string;
  value: boolean | number | string | string[];
  source: Source;
}

export interface SettingsAnswer {
  settings: Setting[];
  facts: Fact[];
}

export interface SettingsChange {
  set?: Record<string, boolean | number | string>;
  reset?: string[];
}

export function getSettings(): Promise<SettingsAnswer> {
  return api<SettingsAnswer>("GET", "/api/v1/settings");
}

// changeSettings sends the changed keys only; the answer is the settings after the change. The change is all or nothing.
export function changeSettings(change: SettingsChange): Promise<SettingsAnswer> {
  return api<SettingsAnswer>("PATCH", "/api/v1/settings", { body: change });
}

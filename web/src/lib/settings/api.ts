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

// The logo and the icon are uploaded pictures, which are not settings. state is none (the installation's artwork or the server's own
// choice), file (the config file sets it), uploaded (an upload in use) or unusable (an upload is stored and the server did not use it:
// reason says why, and Remove clears it). type and sha256 are there for file and uploaded only.
export type PictureKey = "ui.brand.logo" | "ui.brand.icon";
export type PictureKind = "logo" | "icon";
export interface Picture {
  key: PictureKey;
  state: "none" | "file" | "uploaded" | "unusable";
  type?: "image/png" | "image/svg+xml";
  sha256?: string;
  // reason is a code (row_not_written, not_regular_file, hash_changed, file_missing, unreadable, invalid_picture) for unusable only.
  reason?: string;
  locked: boolean;
}

// logoHash is the hash of the logo in use (an upload or the config file's), or undefined when the artwork shows.
export function logoHash(a: SettingsAnswer): string | undefined {
  const p = a.pictures.find((x) => x.key === "ui.brand.logo");
  return p !== undefined && (p.state === "uploaded" || p.state === "file") ? p.sha256 : undefined;
}

export interface SettingsAnswer {
  settings: Setting[];
  facts: Fact[];
  pictures: Picture[];
}

export interface SettingsChange {
  set?: Record<string, boolean | number | string>;
  reset?: string[];
}

// The two picture routes are written out, so the route table and the command-line parity check find them by their text.
const PICTURE_PATH = { logo: "/api/v1/settings/brand/logo", icon: "/api/v1/settings/brand/icon" } as const;

export function getSettings(): Promise<SettingsAnswer> {
  return api<SettingsAnswer>("GET", "/api/v1/settings");
}

// changeSettings sends the changed keys only; the answer is the settings after the change. The change is all or nothing.
export function changeSettings(change: SettingsChange): Promise<SettingsAnswer> {
  return api<SettingsAnswer>("PATCH", "/api/v1/settings", { body: change });
}

// putPicture uploads a logo or an icon as the raw body, typed as the file is. The answer is the settings after the change. The server
// checks the type and the size and says why when it refuses.
export function putPicture(kind: PictureKind, file: Blob, type: string): Promise<SettingsAnswer> {
  return api<SettingsAnswer>("PUT", PICTURE_PATH[kind], { raw: { body: file, type } });
}

// deletePicture removes the upload of a logo or an icon, and what a start could not use.
export function deletePicture(kind: PictureKind): Promise<SettingsAnswer> {
  return api<SettingsAnswer>("DELETE", PICTURE_PATH[kind]);
}

// SPDX-License-Identifier: AGPL-3.0-or-later

import type { SortDir, SortKey } from './sort';

// The list's view and sort, kept per browser. Storage can be blocked, so every access is guarded and the
// defaults apply when nothing can be read.
export type ViewMode = 'list' | 'grid';

export interface ViewPrefs {
  view: ViewMode;
  sort: { key: SortKey; dir: SortDir };
}

export const PREF_FILES = 'selfcloud.files';
export const DEFAULT_PREFS: ViewPrefs = { view: 'list', sort: { key: 'name', dir: 'asc' } };

const KEYS: SortKey[] = ['name', 'modified', 'size'];

// parsePrefs reads stored preferences, ignoring anything it does not recognise.
export function parsePrefs(raw: string | null): ViewPrefs {
  if (!raw) return DEFAULT_PREFS;
  try {
    const v = JSON.parse(raw) as Partial<ViewPrefs>;
    const view: ViewMode = v.view === 'grid' ? 'grid' : 'list';
    const key = KEYS.includes(v.sort?.key as SortKey) ? (v.sort!.key as SortKey) : 'name';
    const dir: SortDir = v.sort?.dir === 'desc' ? 'desc' : 'asc';
    return { view, sort: { key, dir } };
  } catch {
    return DEFAULT_PREFS;
  }
}

export function readPrefs(): ViewPrefs {
  try {
    return parsePrefs(window.localStorage.getItem(PREF_FILES));
  } catch {
    return DEFAULT_PREFS;
  }
}

export function writePrefs(prefs: ViewPrefs): void {
  try {
    window.localStorage.setItem(PREF_FILES, JSON.stringify(prefs));
  } catch {
    // Not saved; the choice lasts for this page.
  }
}

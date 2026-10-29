// SPDX-License-Identifier: AGPL-3.0-or-later

import { writable } from 'svelte/store';
import { PREF_THEME, readPref, writePref } from './prefs';

// Theme is the person's choice. "system" follows prefers-color-scheme, which the stylesheet reads
// when no data-theme is set, so there is no flash on load.
export type Theme = 'system' | 'light' | 'dark';

export const theme = writable<Theme>('system');

function isTheme(v: string | null): v is Theme {
  return v === 'system' || v === 'light' || v === 'dark';
}

export function setTheme(t: Theme, persist = true): void {
  theme.set(t);
  const root = document.documentElement;
  if (t === 'system') {
    root.removeAttribute('data-theme');
  } else {
    root.dataset.theme = t;
  }
  if (persist) writePref(PREF_THEME, t);
}

export function loadTheme(): void {
  const saved = readPref(PREF_THEME);
  setTheme(isTheme(saved) ? saved : 'system', false);
}

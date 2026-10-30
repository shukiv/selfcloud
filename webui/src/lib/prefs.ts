// SPDX-License-Identifier: AGPL-3.0-or-later

// Per-browser choices. Storage can be blocked or full (private windows, for one); then the choice
// lasts for the page only, and the page still works.
export const PREF_LANG = 'selfcloud.lang';
export const PREF_THEME = 'selfcloud.theme';

export function readPref(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writePref(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Not saved; the choice still applies to this page.
  }
}

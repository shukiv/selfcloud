// SPDX-License-Identifier: AGPL-3.0-or-later

import type { DirectoryEntry } from '../files/types';

// The people combobox's state, as a reducer so its rules can be tested without a page. It follows the ARIA
// combobox pattern: a query, a popup list of options, one active option, and a chosen entry.

export const MIN_QUERY = 2;

export interface ComboState {
  query: string;
  open: boolean;
  status: 'idle' | 'loading' | 'ready' | 'error';
  items: DirectoryEntry[];
  // active is the index of the option the arrow keys are on, or -1.
  active: number;
  chosen: DirectoryEntry | null;
}

export type ComboAction =
  | { type: 'type'; query: string }
  | { type: 'results'; query: string; items: DirectoryEntry[] }
  | { type: 'fail'; query: string }
  | { type: 'move'; delta: number }
  | { type: 'pick'; entry: DirectoryEntry }
  | { type: 'choose' }
  | { type: 'open' }
  | { type: 'close' }
  | { type: 'clear' };

export const COMBO_START: ComboState = { query: '', open: false, status: 'idle', items: [], active: -1, chosen: null };

export function comboReducer(s: ComboState, a: ComboAction): ComboState {
  switch (a.type) {
    case 'type': {
      // Typing starts a new search and drops what was chosen before.
      if (a.query.trim().length < MIN_QUERY) {
        return { ...s, query: a.query, open: false, status: 'idle', items: [], active: -1, chosen: null };
      }
      return { ...s, query: a.query, open: true, status: 'loading', items: [], active: -1, chosen: null };
    }
    case 'results': {
      // An answer to an old query is dropped, so a slow answer cannot overwrite a newer one.
      if (a.query !== s.query) return s;
      return { ...s, status: 'ready', items: a.items, active: a.items.length > 0 ? 0 : -1 };
    }
    case 'fail': {
      if (a.query !== s.query) return s;
      return { ...s, status: 'error', items: [], active: -1 };
    }
    case 'move': {
      if (!s.open || s.items.length === 0) return s;
      const n = s.items.length;
      return { ...s, active: (((s.active + a.delta) % n) + n) % n };
    }
    case 'pick':
      return { ...s, query: a.entry.name, open: false, active: -1, chosen: a.entry };
    case 'choose': {
      const entry = s.items[s.active];
      if (!s.open || entry === undefined) return s;
      return { ...s, query: entry.name, open: false, active: -1, chosen: entry };
    }
    case 'open':
      // Reopens the list (on the arrow keys, after a blur) only when it has something to show.
      if (s.items.length === 0) return s;
      return { ...s, open: true, active: s.active < 0 ? 0 : s.active };
    case 'close':
      return { ...s, open: false, active: -1 };
    case 'clear':
      return COMBO_START;
  }
}

// debounce runs fn once, ms after the last call. cancel drops a call that has not run yet.
export function debounce<A extends unknown[]>(fn: (...args: A) => void, ms: number) {
  let timer: ReturnType<typeof setTimeout> | null = null;
  return {
    call(...args: A): void {
      if (timer !== null) clearTimeout(timer);
      timer = setTimeout(() => {
        timer = null;
        fn(...args);
      }, ms);
    },
    cancel(): void {
      if (timer !== null) clearTimeout(timer);
      timer = null;
    },
  };
}

// SEARCH_DELAY is how long the combobox waits after the last key before it searches.
export const SEARCH_DELAY = 250;

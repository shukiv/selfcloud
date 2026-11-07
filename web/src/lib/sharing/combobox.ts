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
  // more is true when the shown answer holds fewer entries than matched (decision 96).
  more: boolean;
  // browse is true once the field has been focused. Only then does a query under two characters open the
  // account's list; the mention picker never sets it, so its short queries stay closed.
  browse: boolean;
  // focus is the cached answer to an empty q, the account's list, or null until it arrives. It is dropped with a
  // session (clear).
  focus: { items: DirectoryEntry[]; more: boolean } | null;
}

export type ComboAction =
  | { type: 'type'; query: string }
  | { type: 'results'; query: string; items: DirectoryEntry[]; more?: boolean }
  | { type: 'fail'; query: string }
  | { type: 'focus' }
  | { type: 'listed'; items: DirectoryEntry[]; more: boolean }
  | { type: 'listfail' }
  | { type: 'move'; delta: number }
  | { type: 'pick'; entry: DirectoryEntry }
  | { type: 'choose' }
  | { type: 'open' }
  | { type: 'close' }
  | { type: 'clear' };

export const COMBO_START: ComboState = {
  query: '',
  open: false,
  status: 'idle',
  items: [],
  active: -1,
  chosen: null,
  more: false,
  browse: false,
  focus: null,
};

// showShort is the state of a field under MIN_QUERY that has been focused: the cached list when there is one, else
// a loading status until it arrives.
function showShort(s: ComboState): ComboState {
  if (s.focus) {
    const items = s.focus.items;
    return { ...s, open: true, status: 'ready', items, more: s.focus.more, active: -1 };
  }
  return { ...s, open: true, status: 'loading', items: [], more: false, active: -1 };
}

export function comboReducer(s: ComboState, a: ComboAction): ComboState {
  switch (a.type) {
    case 'type': {
      // Typing starts a new search and drops what was chosen before.
      if (a.query.trim().length < MIN_QUERY) {
        if (s.browse) return showShort({ ...s, query: a.query, chosen: null });
        return { ...s, query: a.query, open: false, status: 'idle', items: [], active: -1, chosen: null, more: false };
      }
      return { ...s, query: a.query, open: true, status: 'loading', items: [], active: -1, chosen: null, more: false };
    }
    case 'results': {
      // An answer to an old query is dropped, so a slow answer cannot overwrite a newer one.
      if (a.query !== s.query) return s;
      return { ...s, status: 'ready', items: a.items, more: a.more === true, active: a.items.length > 0 ? 0 : -1 };
    }
    case 'fail': {
      if (a.query !== s.query) return s;
      return { ...s, status: 'error', items: [], more: false, active: -1 };
    }
    case 'focus': {
      if (s.query.trim().length >= MIN_QUERY) return { ...s, browse: true };
      return showShort({ ...s, browse: true });
    }
    case 'listed': {
      // The account's list is cached whatever the query is now. It is shown only while the field holds fewer than two
      // characters: an answer that lands after two characters were typed is not shown, so the search's status stays.
      const focus = { items: a.items, more: a.more };
      if (s.query.trim().length >= MIN_QUERY || !s.browse) return { ...s, focus };
      const items = a.items;
      return { ...s, focus, status: 'ready', items, more: a.more, active: -1 };
    }
    case 'listfail': {
      if (s.query.trim().length >= MIN_QUERY || !s.browse) return s;
      return { ...s, status: 'error', items: [], more: false, active: -1 };
    }
    case 'move': {
      if (!s.open || s.items.length === 0) return s;
      const n = s.items.length;
      // From no active option, the first arrow goes to the first option and the up arrow to the last.
      if (s.active < 0) return { ...s, active: a.delta > 0 ? 0 : n - 1 };
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

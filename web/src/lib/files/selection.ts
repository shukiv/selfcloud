// SPDX-License-Identifier: AGPL-3.0-or-later

// Selection in a list, as pure functions. Positions are indexes into the list's ids.
export interface Selection {
  ids: string[];
  // anchor is where a range starts (the last plain or ctrl click); focus is the keyboard position.
  anchor: number | null;
  focus: number | null;
}

export const EMPTY: Selection = { ids: [], anchor: null, focus: null };

export interface ClickMods {
  shift?: boolean;
  toggle?: boolean; // Ctrl or Cmd
}

// rangeIDs returns the ids from position a to b, both included, in list order.
export function rangeIDs(all: string[], a: number, b: number): string[] {
  const [lo, hi] = a <= b ? [a, b] : [b, a];
  return all.slice(lo, hi + 1);
}

// clickSelect applies a click on the item at index. A plain click selects only that item; Ctrl or Cmd
// toggles it; Shift selects the range from the anchor, keeping what was selected before only when Ctrl
// is also held.
export function clickSelect(state: Selection, all: string[], index: number, mods: ClickMods = {}): Selection {
  const id = all[index];
  if (id === undefined) return state;
  if (mods.shift && state.anchor !== null && all[state.anchor] !== undefined) {
    const range = rangeIDs(all, state.anchor, index);
    const ids = mods.toggle ? [...new Set([...state.ids, ...range])] : range;
    return { ids, anchor: state.anchor, focus: index };
  }
  if (mods.toggle) {
    const ids = state.ids.includes(id) ? state.ids.filter((x) => x !== id) : [...state.ids, id];
    return { ids, anchor: index, focus: index };
  }
  return { ids: [id], anchor: index, focus: index };
}

// moveFocus moves the keyboard position by delta, clamped to the list. With extend (Shift+arrow) the
// selection grows from the anchor to the new position.
export function moveFocus(state: Selection, all: string[], delta: number, extend = false): Selection {
  if (all.length === 0) return EMPTY;
  const from = state.focus ?? -1;
  const to = Math.min(all.length - 1, Math.max(0, from + delta));
  if (!extend) return { ids: [all[to]], anchor: to, focus: to };
  const anchor = state.anchor ?? to;
  return { ids: rangeIDs(all, anchor, to), anchor, focus: to };
}

// selectAll selects every id in the list.
export function selectAll(all: string[]): Selection {
  return { ids: [...all], anchor: all.length ? 0 : null, focus: all.length ? all.length - 1 : null };
}

// pruneSelection drops ids that are no longer in the list, after a reload or a delete.
export function pruneSelection(state: Selection, all: string[]): Selection {
  const present = new Set(all);
  const ids = state.ids.filter((id) => present.has(id));
  const anchor = state.anchor !== null && state.anchor < all.length ? state.anchor : null;
  const focus = state.focus !== null && state.focus < all.length ? state.focus : null;
  return { ids, anchor, focus };
}

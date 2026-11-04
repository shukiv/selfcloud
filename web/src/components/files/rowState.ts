// SPDX-License-Identifier: AGPL-3.0-or-later

// The list's per-row state (slice vl, decision 78): which rows are selected, which one has the focus, which are cut, and which
// name is being renamed. The listing writes the state on each of its renders. A row's cells read the part that concerns the
// row, so a change draws the rows it touches and no others. The state is written during the render, so a row drawn in that
// render reads it at once; the rows that are not drawn again are told of the change after the commit (notify).

import { useSyncExternalStore } from "react";

// The bits a row's state is read as: a number, so a row's subscription compares a primitive and draws only on a change.
export const BIT_SELECTED = 1;
export const BIT_FOCUSED = 2;
export const BIT_CUT = 4;

export interface RenameState {
  id: string;
  value: string;
  error: string | null;
}

export interface RowState {
  selected: Set<string>;
  focusID: string | null;
  cutIDs: Set<string>;
  renameID: string | null;
  renameValue: string;
  renameError: string | null;
}

export interface RowStore {
  // set replaces the state. Subscribers are not told here: notify does that after the commit.
  set(s: RowState): void;
  notify(): void;
  subscribe(listener: () => void): () => void;
  bits(id: string): number;
  // rename is the rename of this row, or null. Its identity changes only when the rename does.
  rename(id: string): RenameState | null;
}

function sameRename(a: RenameState | null, b: RenameState | null): boolean {
  if (a === null || b === null) return a === b;
  return a.id === b.id && a.value === b.value && a.error === b.error;
}

export function createRowStore(): RowStore {
  let selected = new Set<string>();
  let focusID: string | null = null;
  let cut = new Set<string>();
  let rename: RenameState | null = null;
  const listeners = new Set<() => void>();
  return {
    set(s) {
      selected = s.selected;
      focusID = s.focusID;
      cut = s.cutIDs;
      const next = s.renameID === null ? null : { id: s.renameID, value: s.renameValue, error: s.renameError };
      if (!sameRename(rename, next)) rename = next;
    },
    notify() {
      for (const listener of [...listeners]) listener();
    },
    subscribe(listener) {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    bits(id) {
      return (selected.has(id) ? BIT_SELECTED : 0) | (focusID === id ? BIT_FOCUSED : 0) | (cut.has(id) ? BIT_CUT : 0);
    },
    rename(id) {
      return rename !== null && rename.id === id ? rename : null;
    },
  };
}

export function useRowBits(store: RowStore, id: string): number {
  return useSyncExternalStore(store.subscribe, () => store.bits(id));
}

export function useRename(store: RowStore, id: string): RenameState | null {
  return useSyncExternalStore(store.subscribe, () => store.rename(id));
}

// SPDX-License-Identifier: AGPL-3.0-or-later

import { useCallback, useMemo, useRef, useState, type KeyboardEvent } from "react";
import { isTextField } from "./files/keys";

// The selection and the keys every list shares. Listing applies them to a preset list (one that
// passes its columns); My files keeps its own selection and keys in FilesPage for now, and passes them through as before.
// Both hooks keep ids, not places: a re-sort or a new page cannot move the selection or the focus to another row.

// ListSelection is a list's selected rows and the row that has the keyboard's focus.
export interface ListSelection {
  // selected holds the selected ids that are in the list now: an id that left the list is not selected.
  selected: ReadonlySet<string>;
  // focusID is the row the roving focus is on: the one set last, or the first row when none was set or it has gone.
  focusID: string | null;
  setFocus: (id: string) => void;
  // pick is a click or a key on a row. Alone it selects that row only; toggle (Ctrl, Cmd or Space) adds or removes it;
  // range (Shift) selects from the anchor (the row picked last) to it, and with toggle adds that range to the selection.
  pick: (id: string, how?: { toggle?: boolean; range?: boolean }) => void;
  selectAll: () => void;
  clear: () => void;
}

interface SelState {
  picked: ReadonlySet<string>;
  anchor: string | null;
  focus: string | null;
}

const NONE: SelState = { picked: new Set(), anchor: null, focus: null };

// useListSelection keeps the selection of the list whose rows are ids, in their order.
export function useListSelection(ids: readonly string[]): ListSelection {
  const [state, setState] = useState<SelState>(NONE);
  // The callbacks read the ids of the latest render, so they stay the same functions from one render to the next.
  const idsRef = useRef(ids);
  idsRef.current = ids;
  const present = useMemo(() => new Set(ids), [ids]);
  const selected = useMemo(() => new Set([...state.picked].filter((id) => present.has(id))), [state.picked, present]);
  const focusID = state.focus !== null && present.has(state.focus) ? state.focus : (ids[0] ?? null);

  const setFocus = useCallback((id: string) => setState((s) => (s.focus === id ? s : { ...s, focus: id })), []);
  const pick = useCallback((id: string, how: { toggle?: boolean; range?: boolean } = {}) => {
    setState((s) => {
      const list = idsRef.current;
      if (how.range) {
        const anchor = s.anchor !== null && list.includes(s.anchor) ? s.anchor : id;
        const a = list.indexOf(anchor);
        const b = list.indexOf(id);
        const span = list.slice(Math.min(a, b), Math.max(a, b) + 1);
        return { picked: new Set(how.toggle ? [...s.picked, ...span] : span), anchor, focus: id };
      }
      if (how.toggle) {
        const picked = new Set(s.picked);
        if (picked.has(id)) picked.delete(id);
        else picked.add(id);
        return { picked, anchor: id, focus: id };
      }
      return { picked: new Set([id]), anchor: id, focus: id };
    });
  }, []);
  const selectAll = useCallback(() => setState((s) => ({ ...s, picked: new Set(idsRef.current) })), []);
  const clear = useCallback(() => setState((s) => (s.picked.size === 0 ? s : { ...s, picked: new Set(), anchor: null })), []);
  return { selected, focusID, setFocus, pick, selectAll, clear };
}

// ListKeysOptions are what the keys act on.
export interface ListKeysOptions {
  // ids are the list's rows in their order, and focusID the row that has the roving focus.
  ids: readonly string[];
  focusID: string | null;
  // focusRow moves the keyboard's focus to the row with this id. The list does it, in its own rows.
  focusRow: (id: string) => void;
  // onOpen opens a row: Enter on the row itself. A list whose rows do not open leaves it out.
  onOpen?: (id: string) => void;
  // selection is given on a list with a selection: Space toggles the focused row, Shift with an arrow extends the range,
  // Ctrl or Cmd with A selects every row, and Escape clears.
  selection?: Pick<ListSelection, "pick" | "selectAll" | "clear">;
  // columns is the number of tiles in a row of a tile view; a list (the default, 1) has one. In a tile view Left and
  // Right move by one tile, mirrored when dir is rtl, and Up and Down by a row of tiles. In a list Left and Right do nothing.
  columns?: number;
  dir?: "ltr" | "rtl";
}

// useListKeys returns the key handler of a list's rows: Up and Down move the focus (Left and Right too in a tile view),
// Home and End go to the first and the last row, Enter opens, and on a list with a selection Space toggles, Shift
// extends, Ctrl or Cmd with A selects all and Escape clears. A key from a control inside a row (a link, a button) keeps
// its own meaning, except the arrows, which move on from it; Home and End move only from the row itself. A key from a
// field inside a row (a text box, a textarea, a select, a combobox) is the field's, Escape apart; a checkbox is not a field. A key from a
// portal the row opened (a menu, a dialog) is left to the portal.
export function useListKeys(o: ListKeysOptions): (e: KeyboardEvent<HTMLElement>) => void {
  const ref = useRef(o);
  ref.current = o;
  return useCallback((e: KeyboardEvent<HTMLElement>) => {
    const { ids, focusID, focusRow, onOpen, selection, columns = 1, dir = "ltr" } = ref.current;
    if (ids.length === 0 || e.altKey) return;
    // React passes a key in a portal the row opened (its ⋮ menu, a dialog) up to the row; that key is the portal's.
    if (e.target instanceof Node && !e.currentTarget.contains(e.target)) return;
    // A field in the row (a text box, a textarea, a select, a combobox) keeps its arrows and its other keys; only Escape goes on.
    // A checkbox is not a field: its arrows move and extend the selection.
    if ((isTextField(e.target) || (e.target instanceof Element && e.target.matches("[role=combobox]"))) && e.key !== "Escape") return;
    const at = focusID === null ? -1 : ids.indexOf(focusID);
    const onRow = e.target === e.currentTarget;
    const moveTo = (i: number) => {
      e.preventDefault();
      const id = ids[Math.max(0, Math.min(ids.length - 1, i))];
      if (selection && e.shiftKey) selection.pick(id, { range: true, toggle: e.ctrlKey || e.metaKey });
      focusRow(id);
    };
    const step = columns > 1 ? columns : 1;
    switch (e.key) {
      case "ArrowDown":
        return moveTo(at < 0 ? 0 : at + step);
      case "ArrowUp":
        return moveTo(at < 0 ? 0 : at - step);
      case "ArrowRight":
      case "ArrowLeft": {
        if (columns <= 1) return;
        const forward = (e.key === "ArrowRight") === (dir === "ltr");
        return moveTo(at < 0 ? 0 : at + (forward ? 1 : -1));
      }
      case "Home":
        if (onRow) moveTo(0);
        return;
      case "End":
        if (onRow) moveTo(ids.length - 1);
        return;
      case "Enter":
        if (!onRow || !onOpen || focusID === null) return;
        e.preventDefault();
        onOpen(focusID);
        return;
      case " ":
        if (!onRow || !selection || focusID === null) return;
        e.preventDefault();
        selection.pick(focusID, { toggle: true });
        return;
      case "Escape":
        selection?.clear();
        return;
      case "a":
      case "A":
        if (!onRow || !selection || !(e.ctrlKey || e.metaKey)) return;
        e.preventDefault();
        selection.selectAll();
        return;
    }
  }, []);
}

// SPDX-License-Identifier: AGPL-3.0-or-later

// The keyboard map of the file listing, as a pure function. The page reads the event and the layout, and acts on
// the action returned. The rules are those of the Svelte listing: arrows move by a row or a tile, Home and End
// go to the ends, Shift extends the selection, and so on.

export interface KeyFields {
  key: string;
  shiftKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  altKey: boolean;
}

export interface KeyLayout {
  // cols is how many tiles sit on one row; 1 for the list.
  cols: number;
  // grid is true in the tile view, where left and right move between tiles.
  grid: boolean;
  // rtl mirrors left and right.
  rtl: boolean;
  // count is how many items are listed; Home and End move by it.
  count: number;
}

export type KeyAction =
  | { kind: "move"; delta: number; extend: boolean }
  | { kind: "enter" }
  | { kind: "space" }
  | { kind: "f2" }
  | { kind: "delete" }
  | { kind: "up" }
  | { kind: "selectAll" }
  | { kind: "escape" };

// keyAction returns what a key press means in the listing, or null for a key the listing does not use.
// Alt+ArrowUp is an ArrowUp press, so it moves up a row, as it does in the Svelte listing. The "up" action (go to
// the parent folder) is therefore reached by Backspace only.
export function keyAction(e: KeyFields, layout: KeyLayout): KeyAction | null {
  const { cols, grid, rtl, count } = layout;
  switch (e.key) {
    case "ArrowDown":
      return { kind: "move", delta: cols, extend: e.shiftKey };
    case "ArrowUp":
      return { kind: "move", delta: -cols, extend: e.shiftKey };
    case "ArrowRight":
      return grid ? { kind: "move", delta: rtl ? -1 : 1, extend: e.shiftKey } : null;
    case "ArrowLeft":
      return grid ? { kind: "move", delta: rtl ? 1 : -1, extend: e.shiftKey } : null;
    case "Home":
      return { kind: "move", delta: -count, extend: e.shiftKey };
    case "End":
      return { kind: "move", delta: count, extend: e.shiftKey };
    case "Enter":
      return { kind: "enter" };
    case " ":
      return { kind: "space" };
    case "F2":
      return { kind: "f2" };
    case "Delete":
      return { kind: "delete" };
    case "Backspace":
      return { kind: "up" };
    case "a":
    case "A":
      return e.ctrlKey || e.metaKey ? { kind: "selectAll" } : null;
    case "Escape":
      return { kind: "escape" };
    default:
      return null;
  }
}

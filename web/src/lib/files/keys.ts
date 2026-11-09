// SPDX-License-Identifier: AGPL-3.0-or-later

// The keyboard map of the file listing, as a pure function. The page reads the event and the layout, and acts on
// the action returned. The rules are those of the Svelte listing: arrows move by a row or a tile, Home and End
// go to the ends, Shift extends the selection, and so on.

export interface KeyFields {
  key: string;
  // code is the key's position (KeyC and so on), which does not change with the layout.
  code?: string;
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
  // mac is true on Apple's systems, where Command+Backspace is the trash's key (review fix 8).
  mac?: boolean;
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
  | { kind: "escape" }
  | { kind: "copy" }
  | { kind: "cut" }
  | { kind: "paste" };

// isTextField says whether an event's target is a field the person types in: a text box, an area, a select or an editable
// element. A checkbox or a button is not one, so a key on it still acts on the page.
export function isTextField(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  // A node inside a contenteditable counts too: closest() reads the attribute, which jsdom and older engines do not give isContentEditable.
  if (target.isContentEditable || target.closest('[contenteditable]:not([contenteditable="false"])')) return true;
  if (target instanceof HTMLTextAreaElement || target instanceof HTMLSelectElement) return true;
  return target instanceof HTMLInputElement && !["checkbox", "radio", "button", "submit"].includes(target.type);
}

// keyAction returns what a key press means in the listing, or null for a key the listing does not use.
// Alt+ArrowUp goes up a folder, as Backspace does; a plain ArrowUp moves up a row. Ctrl or Cmd with C, X or V copies,
// cuts or pastes; Shift with them is left to the browser.
// letterOf is the Latin letter a Ctrl or Cmd key types, lower case. The letter typed is used when it is Latin, so a layout that
// moves the keys (Dvorak) keeps its own letters. When the layout types another alphabet (Hebrew, Cyrillic, Arabic), the key's
// position says which letter it is.
function letterOf(e: KeyFields): string {
  if (/^[A-Za-z]$/.test(e.key)) return e.key.toLowerCase();
  const position = /^Key([A-Z])$/.exec(e.code ?? "");
  return position ? position[1].toLowerCase() : "";
}

export function keyAction(e: KeyFields, layout: KeyLayout): KeyAction | null {
  const { cols, grid, rtl, count } = layout;
  const accel = e.ctrlKey || e.metaKey;
  if (e.key === "ArrowUp" && e.altKey) return { kind: "up" };
  // On a Mac, Command+Backspace is Delete (move to the trash); Backspace alone still goes up a folder.
  if (layout.mac && e.key === "Backspace" && e.metaKey && !e.shiftKey && !e.altKey) return { kind: "delete" };
  if (accel && !e.shiftKey && !e.altKey) {
    const letter = letterOf(e);
    if (letter === "c") return { kind: "copy" };
    if (letter === "x") return { kind: "cut" };
    if (letter === "v") return { kind: "paste" };
  }
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
    case "Escape":
      return { kind: "escape" };
    default:
      return accel && !e.shiftKey && !e.altKey && letterOf(e) === "a" ? { kind: "selectAll" } : null;
  }
}

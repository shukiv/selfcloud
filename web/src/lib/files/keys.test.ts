// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { keyAction, type KeyFields, type KeyLayout } from "./keys";

const none: KeyFields = { key: "", shiftKey: false, ctrlKey: false, metaKey: false, altKey: false };
const press = (key: string, mods: Partial<KeyFields> = {}): KeyFields => ({ ...none, key, ...mods });
const list: KeyLayout = { cols: 1, grid: false, rtl: false, count: 10 };
const grid: KeyLayout = { cols: 4, grid: true, rtl: false, count: 10 };

describe("keyAction", () => {
  it("moves up and down by the number of columns: one row in the list, a row of tiles in the grid", () => {
    expect(keyAction(press("ArrowDown"), list)).toEqual({ kind: "move", delta: 1, extend: false });
    expect(keyAction(press("ArrowUp"), list)).toEqual({ kind: "move", delta: -1, extend: false });
    expect(keyAction(press("ArrowDown"), grid)).toEqual({ kind: "move", delta: 4, extend: false });
    expect(keyAction(press("ArrowUp"), grid)).toEqual({ kind: "move", delta: -4, extend: false });
  });

  it("moves left and right only in the grid, and mirrors them in right-to-left", () => {
    expect(keyAction(press("ArrowRight"), list)).toBeNull();
    expect(keyAction(press("ArrowLeft"), list)).toBeNull();
    expect(keyAction(press("ArrowRight"), grid)).toEqual({ kind: "move", delta: 1, extend: false });
    expect(keyAction(press("ArrowLeft"), grid)).toEqual({ kind: "move", delta: -1, extend: false });
    const rtl = { ...grid, rtl: true };
    expect(keyAction(press("ArrowRight"), rtl)).toEqual({ kind: "move", delta: -1, extend: false });
    expect(keyAction(press("ArrowLeft"), rtl)).toEqual({ kind: "move", delta: 1, extend: false });
  });

  it("goes to the first and last item with Home and End", () => {
    expect(keyAction(press("Home"), list)).toEqual({ kind: "move", delta: -10, extend: false });
    expect(keyAction(press("End"), grid)).toEqual({ kind: "move", delta: 10, extend: false });
  });

  it("extends the selection with Shift on any move", () => {
    expect(keyAction(press("ArrowDown", { shiftKey: true }), list)).toEqual({ kind: "move", delta: 1, extend: true });
    expect(keyAction(press("End", { shiftKey: true }), list)).toEqual({ kind: "move", delta: 10, extend: true });
    expect(keyAction(press("ArrowRight", { shiftKey: true }), grid)).toEqual({ kind: "move", delta: 1, extend: true });
  });

  it("maps Enter, Space, F2 and Delete", () => {
    expect(keyAction(press("Enter"), list)).toEqual({ kind: "enter" });
    expect(keyAction(press(" "), list)).toEqual({ kind: "space" });
    expect(keyAction(press("F2"), list)).toEqual({ kind: "f2" });
    expect(keyAction(press("Delete"), list)).toEqual({ kind: "delete" });
  });

  it("maps Backspace to up", () => {
    expect(keyAction(press("Backspace"), list)).toEqual({ kind: "up" });
  });

  it("goes up a folder with Alt+ArrowUp, as with Backspace, and does not move the row", () => {
    expect(keyAction(press("ArrowUp", { altKey: true }), list)).toEqual({ kind: "up" });
    expect(keyAction(press("ArrowUp", { altKey: true }), grid)).toEqual({ kind: "up" });
  });

  it("maps Ctrl or Cmd plus C, X and V to copy, cut and paste, in either case", () => {
    expect(keyAction(press("c", { ctrlKey: true }), list)).toEqual({ kind: "copy" });
    expect(keyAction(press("C", { metaKey: true }), list)).toEqual({ kind: "copy" });
    expect(keyAction(press("x", { ctrlKey: true }), list)).toEqual({ kind: "cut" });
    expect(keyAction(press("X", { metaKey: true }), list)).toEqual({ kind: "cut" });
    expect(keyAction(press("v", { ctrlKey: true }), list)).toEqual({ kind: "paste" });
    expect(keyAction(press("V", { metaKey: true }), grid)).toEqual({ kind: "paste" });
  });

  it("leaves plain C, X and V, and Shift with them, to the browser", () => {
    expect(keyAction(press("c"), list)).toBeNull();
    expect(keyAction(press("x"), list)).toBeNull();
    expect(keyAction(press("v"), list)).toBeNull();
    expect(keyAction(press("v", { ctrlKey: true, shiftKey: true }), list)).toBeNull();
  });

  it("reads the letter of Ctrl or Cmd keys from the key's position when the layout's letter is not Latin", () => {
    // Hebrew, Russian and Arabic layouts type another letter on the key of C, X, V and A; the position says which key it is.
    expect(keyAction({ ...press("ב", { ctrlKey: true }), code: "KeyC" }, list)).toEqual({ kind: "copy" });
    expect(keyAction({ ...press("ח", { ctrlKey: true }), code: "KeyX" }, list)).toEqual({ kind: "cut" });
    expect(keyAction({ ...press("м", { metaKey: true }), code: "KeyV" }, list)).toEqual({ kind: "paste" });
    expect(keyAction({ ...press("ש", { ctrlKey: true }), code: "KeyA" }, list)).toEqual({ kind: "selectAll" });
  });

  it("keeps the Latin letter of a layout that moves the keys: the letter typed is the one that acts", () => {
    // Dvorak types C on the key that the position calls J. The typed letter wins whenever it is a Latin letter.
    expect(keyAction({ ...press("c", { ctrlKey: true }), code: "KeyJ" }, list)).toEqual({ kind: "copy" });
    // On the same Dvorak layout the key at position C types j, which is not copy.
    expect(keyAction({ ...press("j", { ctrlKey: true }), code: "KeyC" }, list)).toBeNull();
  });

  it("selects all with Ctrl or Cmd plus A, in either case, and ignores plain A", () => {
    expect(keyAction(press("a", { ctrlKey: true }), list)).toEqual({ kind: "selectAll" });
    expect(keyAction(press("A", { metaKey: true }), list)).toEqual({ kind: "selectAll" });
    expect(keyAction(press("a"), list)).toBeNull();
  });

  it("maps Escape to clear the selection", () => {
    expect(keyAction(press("Escape"), list)).toEqual({ kind: "escape" });
  });

  it("returns null for keys the listing does not use", () => {
    expect(keyAction(press("Tab"), list)).toBeNull();
    expect(keyAction(press("x"), grid)).toBeNull();
  });
});

// Decision 70: on a Mac, Command+Backspace moves the selection to the trash; Backspace alone still goes up a folder,
// and elsewhere Backspace is unchanged.
describe("keyAction: Mac Delete", () => {
  const mac: KeyLayout = { cols: 1, grid: false, rtl: false, count: 10, mac: true };
  it("Command+Backspace on a Mac is the delete action", () => {
    expect(keyAction(press("Backspace", { metaKey: true }), mac)).toEqual({ kind: "delete" });
  });
  it("Backspace alone on a Mac still goes up a folder", () => {
    expect(keyAction(press("Backspace"), mac)).toEqual({ kind: "up" });
  });
  it("elsewhere, Command or Control with Backspace still goes up, and Delete is unchanged", () => {
    expect(keyAction(press("Backspace", { metaKey: true }), list)).toEqual({ kind: "up" });
    expect(keyAction(press("Backspace", { ctrlKey: true }), list)).toEqual({ kind: "up" });
    expect(keyAction(press("Delete"), mac)).toEqual({ kind: "delete" });
  });
});

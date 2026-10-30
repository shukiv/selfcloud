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

  it("treats Alt+ArrowUp as an arrow: it moves up a row, as the Svelte listing does", () => {
    // The old chain tested ArrowUp before the Alt combination, so the parent-folder branch was unreachable there
    // too. The test pins what the page does today; it is a finding, not a goal.
    expect(keyAction(press("ArrowUp", { altKey: true }), list)).toEqual({ kind: "move", delta: -1, extend: false });
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

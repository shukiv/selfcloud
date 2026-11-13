// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, renderHook } from "@testing-library/react";
import type { KeyboardEvent } from "react";
import { describe, expect, it, vi } from "vitest";
import { useListKeys, useListSelection, type ListKeysOptions } from "./lists";

// The shared selection and keys of a list. The preset lists wait for them in later slices (Starred
// and Trash select, tl4); Listing applies them to every preset list now.

describe("useListSelection", () => {
  const ids = ["a", "b", "c", "d"];

  it("a pick alone selects that row only; a toggle adds and removes; a range runs from the anchor", () => {
    const { result } = renderHook(({ list }) => useListSelection(list), { initialProps: { list: ids } });
    act(() => result.current.pick("b"));
    expect([...result.current.selected]).toEqual(["b"]);
    act(() => result.current.pick("d", { toggle: true }));
    expect([...result.current.selected].sort()).toEqual(["b", "d"]);
    act(() => result.current.pick("b", { toggle: true }));
    expect([...result.current.selected]).toEqual(["d"]);
    // The anchor is the row picked last (b, by its toggle): Shift on d selects b to d.
    act(() => result.current.pick("d", { range: true }));
    expect([...result.current.selected].sort()).toEqual(["b", "c", "d"]);
    act(() => result.current.pick("a"));
    expect([...result.current.selected]).toEqual(["a"]);
    expect(result.current.focusID).toBe("a");
  });

  it("selects all, clears, and keeps the focus on the first row until one is set", () => {
    const { result } = renderHook(() => useListSelection(ids));
    expect(result.current.focusID).toBe("a");
    act(() => result.current.selectAll());
    expect(result.current.selected.size).toBe(4);
    act(() => result.current.clear());
    expect(result.current.selected.size).toBe(0);
    act(() => result.current.setFocus("c"));
    expect(result.current.focusID).toBe("c");
  });

  it("keeps ids, not places: a re-sort keeps the selection and the focus on the same rows, and a row that left is dropped", () => {
    const { result, rerender } = renderHook(({ list }) => useListSelection(list), { initialProps: { list: ids } });
    act(() => result.current.pick("c"));
    rerender({ list: ["d", "c", "b", "a"] });
    expect([...result.current.selected]).toEqual(["c"]);
    expect(result.current.focusID).toBe("c");
    rerender({ list: ["a", "b", "d"] });
    expect(result.current.selected.size).toBe(0);
    expect(result.current.focusID).toBe("a");
    // A range from an anchor that left the list starts at the row itself.
    act(() => result.current.pick("d", { range: true }));
    expect([...result.current.selected]).toEqual(["d"]);
  });
});

// key builds a key event as React gives it to a row's handler. on says where the key came from: the row itself, a button
// in the row, or a menu the row opened, which React passes up to the row although it is not in the row's DOM.
function key(
  k: string,
  mods: { shiftKey?: boolean; ctrlKey?: boolean; metaKey?: boolean } = {},
  on: "row" | "button" | "menu" | "input" | "textarea" | "select" | "combobox" | "checkbox" = "row",
) {
  const row = document.createElement("tr");
  const field = (tag: string, role?: string) => {
    const el = row.appendChild(document.createElement(tag));
    if (role) el.setAttribute("role", role);
    if (on === "checkbox") el.setAttribute("type", "checkbox");
    return el;
  };
  const target =
    on === "row"
      ? row
      : on === "button"
        ? row.appendChild(document.createElement("button"))
        : on === "menu"
          ? document.createElement("li")
          : on === "combobox"
            ? field("div", "combobox")
            : field(on === "checkbox" ? "input" : on);
  const preventDefault = vi.fn();
  const e = { key: k, shiftKey: false, ctrlKey: false, metaKey: false, altKey: false, ...mods, target, currentTarget: row, preventDefault };
  return e as unknown as KeyboardEvent<HTMLElement> & { preventDefault: typeof preventDefault };
}

describe("useListKeys", () => {
  const ids = ["a", "b", "c", "d"];
  const setup = (over: Partial<ListKeysOptions> = {}) => {
    const focusRow = vi.fn();
    const onOpen = vi.fn();
    const selection = { pick: vi.fn(), selectAll: vi.fn(), clear: vi.fn() };
    const opts: ListKeysOptions = { ids, focusID: "b", focusRow, onOpen, selection, ...over };
    const { result } = renderHook(() => useListKeys(opts));
    return { press: result.current, focusRow, onOpen, selection };
  };

  it("Up and Down move the focus one row, and stop at the ends; Home and End go to the first and the last", () => {
    const { press, focusRow } = setup();
    press(key("ArrowDown"));
    press(key("ArrowUp"));
    press(key("Home"));
    press(key("End"));
    expect(focusRow.mock.calls.map((c) => c[0])).toEqual(["c", "a", "a", "d"]);
    const atEnd = setup({ focusID: "d" });
    atEnd.press(key("ArrowDown"));
    expect(atEnd.focusRow).toHaveBeenCalledWith("d");
  });

  it("an arrow moves on from a button in the row; Home, End, Enter and Space there keep their own meaning", () => {
    const { press, focusRow, onOpen, selection } = setup();
    press(key("ArrowDown", {}, "button"));
    expect(focusRow).toHaveBeenCalledWith("c");
    for (const k of ["Home", "End", "Enter", " "]) press(key(k, {}, "button"));
    expect(focusRow).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
    expect(selection.pick).not.toHaveBeenCalled();
  });

  it("an arrow from a field in the row (an input, a textarea, a select, a combobox) is the field's: the rows do not move, and Escape still clears", () => {
    const { press, focusRow, selection } = setup();
    for (const on of ["input", "textarea", "select", "combobox"] as const) {
      for (const k of ["ArrowDown", "ArrowUp", "ArrowLeft", "ArrowRight"]) {
        const e = key(k, {}, on);
        press(e);
        expect(e.preventDefault).not.toHaveBeenCalled();
      }
      press(key("Escape", {}, on));
    }
    expect(focusRow).not.toHaveBeenCalled();
    expect(selection.clear).toHaveBeenCalledTimes(4);
  });

  it("a row's checkbox is not a field: arrows and Shift with an arrow from it move and extend, and Space and Enter stay its own", () => {
    const { press, focusRow, onOpen, selection } = setup();
    press(key("ArrowDown", {}, "checkbox"));
    expect(focusRow).toHaveBeenLastCalledWith("c");
    press(key("ArrowDown", { shiftKey: true }, "checkbox"));
    expect(selection.pick).toHaveBeenLastCalledWith("c", { range: true, toggle: false });
    press(key(" ", {}, "checkbox"));
    press(key("Enter", {}, "checkbox"));
    expect(selection.pick).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("a key from a menu the row opened (a portal outside the row) is the menu's: no key moves the rows from there", () => {
    const { press, focusRow, onOpen, selection } = setup();
    for (const k of ["ArrowDown", "ArrowUp", "Enter", " ", "Escape", "Home"]) press(key(k, {}, "menu"));
    expect(focusRow).not.toHaveBeenCalled();
    expect(onOpen).not.toHaveBeenCalled();
    expect(selection.pick).not.toHaveBeenCalled();
    expect(selection.clear).not.toHaveBeenCalled();
  });

  it("Enter opens the focused row", () => {
    const { press, onOpen } = setup();
    const e = key("Enter");
    press(e);
    expect(onOpen).toHaveBeenCalledWith("b");
    expect(e.preventDefault).toHaveBeenCalled();
  });

  it("on a list with a selection: Space toggles, Shift extends, Ctrl or Cmd with A selects all, Escape clears", () => {
    const { press, selection } = setup();
    press(key(" "));
    expect(selection.pick).toHaveBeenLastCalledWith("b", { toggle: true });
    press(key("ArrowDown", { shiftKey: true }));
    expect(selection.pick).toHaveBeenLastCalledWith("c", { range: true, toggle: false });
    press(key("a", { metaKey: true }));
    expect(selection.selectAll).toHaveBeenCalledTimes(1);
    press(key("Escape"));
    expect(selection.clear).toHaveBeenCalledTimes(1);
  });

  it("on a list with no selection Space and Shift select nothing", () => {
    const { press, focusRow } = setup({ selection: undefined });
    press(key(" "));
    press(key("ArrowDown", { shiftKey: true }));
    expect(focusRow).toHaveBeenCalledWith("c");
  });

  it("in a tile view Left and Right move one tile, mirrored right to left, and Up and Down a row of tiles", () => {
    const ltr = setup({ columns: 2, focusID: "b" });
    ltr.press(key("ArrowLeft"));
    ltr.press(key("ArrowDown"));
    expect(ltr.focusRow.mock.calls.map((c) => c[0])).toEqual(["a", "d"]);
    const rtl = setup({ columns: 2, focusID: "b", dir: "rtl" });
    rtl.press(key("ArrowLeft"));
    expect(rtl.focusRow).toHaveBeenCalledWith("c");
    const list = setup();
    list.press(key("ArrowLeft"));
    expect(list.focusRow).not.toHaveBeenCalled();
  });
});

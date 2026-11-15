// SPDX-License-Identifier: AGPL-3.0-or-later

// The phone menus slide one level at a time (decision 200): useMenuLevel turns antd items with children into a first level
// of slide rows and a sub-level of a back row, a divider and the same children. The New menu and the row menu both read it.
// These tests drive the hook alone; the render tests (SidebarNew, FilesPage) drive it through a real dropdown.
import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import type { MenuProps } from "antd";
import { useMenuLevel } from "./menuLevels";

type Items = NonNullable<MenuProps["items"]>;
type Item = Items[number];
// Row is the shape these checks read off a menu item: antd's item union names no label on every member (a divider has
// none), so find and click read the rows through this one structural type. The assertions are unchanged.
type Row = {
  key?: string | number;
  type?: string;
  label?: ReactNode;
  extra?: ReactNode;
  children?: Row[];
  onClick?: (info: { key: string; keyPath: string[]; domEvent: never; item: never }) => void;
  "aria-label"?: string;
  "data-menu-level"?: string;
};

const items: Items = [
  { key: "action:newFolder", label: "New folder" },
  { type: "divider", key: "divider:1" },
  {
    key: "submenu:document",
    label: "Document",
    children: [
      { key: "blank:docx", label: "Blank" },
      { key: "template:document", label: "From a template" },
    ],
  },
  { key: "submenu:more", label: "More", children: [{ key: "blank:odg", label: "Drawing" }] },
];

const keysOf = (list: Items): (string | undefined)[] => list.map((i) => (i && "key" in i ? String(i.key) : undefined));
const find = (list: Items, key: string): Row => {
  const item = list.find((i) => i && "key" in i && i.key === key);
  if (!item) throw new Error(`no item ${key} in ${JSON.stringify(keysOf(list))}`);
  return item as Row;
};
// click runs a row's onClick as antd's Menu does, with the least info it needs.
const click = (item: Row) => {
  if (!item || !("onClick" in item) || typeof item.onClick !== "function") throw new Error("the row has no onClick");
  item.onClick({ key: String(item.key), keyPath: [], domEvent: {} as never, item: null as never });
};
const hasChildren = (i: Item): boolean => !!i && "children" in i && Array.isArray(i.children);

describe("useMenuLevel off a phone", () => {
  it("returns the items unchanged, with no slide rows and no back row", () => {
    const { result } = renderHook(() => useMenuLevel(items, false));
    expect(result.current.items).toBe(items);
    expect(result.current.open).toBe(false);
  });

  it("opens and closes as the dropdown asks, and keeps the items unchanged", () => {
    const { result } = renderHook(() => useMenuLevel(items, false));
    act(() => result.current.onOpenChange(true, { source: "trigger" }));
    expect(result.current.open).toBe(true);
    expect(result.current.items).toBe(items);
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    expect(result.current.open).toBe(false);
  });
});

describe("useMenuLevel on a phone", () => {
  it("the first level lists each submenu as a slide row with no children, and keeps the plain rows", () => {
    const { result } = renderHook(() => useMenuLevel(items, true));
    const shown = result.current.items;
    expect(keysOf(shown)).toEqual(keysOf(items));
    expect(shown.some(hasChildren)).toBe(false);
    const doc = find(shown, "submenu:document");
    expect(doc && "data-menu-level" in doc && doc["data-menu-level"]).toBe("slide");
    expect(doc && "extra" in doc && doc.extra).toBeTruthy();
    expect(find(shown, "action:newFolder").label).toBe("New folder");
  });

  it("a slide row names no popup: no aria-haspopup and no aria-expanded", () => {
    const { result } = renderHook(() => useMenuLevel(items, true));
    for (const item of result.current.items) {
      if (!item) continue;
      expect("aria-haspopup" in item ? item["aria-haspopup"] : undefined).toBeUndefined();
      expect("aria-expanded" in item ? item["aria-expanded"] : undefined).toBeUndefined();
    }
  });

  it("tapping a slide row replaces the level with the back row, a divider and the same children, and the menu stays open", () => {
    const { result } = renderHook(() => useMenuLevel(items, true));
    act(() => result.current.onOpenChange(true, { source: "trigger" }));
    act(() => click(find(result.current.items, "submenu:document")));
    // antd asks to close on every item click: the slide row's click is not a close.
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    expect(result.current.open).toBe(true);
    const shown = result.current.items;
    expect(keysOf(shown)).toEqual(["menu-level:back", "menu-level:divider", "blank:docx", "template:document"]);
    expect(shown[1] && "type" in shown[1] && shown[1].type).toBe("divider");
    expect(find(shown, "blank:docx").label).toBe("Blank");
    expect(find(shown, "template:document").label).toBe("From a template");
    const back = find(shown, "menu-level:back");
    expect(back && "aria-label" in back && back["aria-label"]).toBe("Back to Document");
  });

  it("the back row returns to the first level, and the menu stays open", () => {
    const { result } = renderHook(() => useMenuLevel(items, true));
    act(() => result.current.onOpenChange(true, { source: "trigger" }));
    act(() => click(find(result.current.items, "submenu:more")));
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    expect(keysOf(result.current.items)).toEqual(["menu-level:back", "menu-level:divider", "blank:odg"]);
    act(() => click(find(result.current.items, "menu-level:back")));
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    expect(result.current.open).toBe(true);
    expect(keysOf(result.current.items)).toEqual(keysOf(items));
    expect(result.current.items.some(hasChildren)).toBe(false);
  });

  it("a close (outside tap or Escape) resets to the first level, and so does the next opening", () => {
    const { result } = renderHook(() => useMenuLevel(items, true));
    act(() => result.current.onOpenChange(true, { source: "trigger" }));
    act(() => click(find(result.current.items, "submenu:document")));
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    act(() => result.current.onOpenChange(false, { source: "trigger" }));
    expect(result.current.open).toBe(false);
    act(() => result.current.onOpenChange(true, { source: "trigger" }));
    expect(result.current.open).toBe(true);
    expect(keysOf(result.current.items)).toEqual(keysOf(items));
  });

  it("an action's click closes the menu, as antd asks", () => {
    const { result } = renderHook(() => useMenuLevel(items, true));
    act(() => result.current.onOpenChange(true, { source: "trigger" }));
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    expect(result.current.open).toBe(false);
  });

  it("setOpen(false) closes and resets, as the Escape wrapper asks", () => {
    const { result } = renderHook(() => useMenuLevel(items, true));
    act(() => result.current.onOpenChange(true, { source: "trigger" }));
    act(() => click(find(result.current.items, "submenu:document")));
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    act(() => result.current.setOpen(false));
    expect(result.current.open).toBe(false);
    act(() => result.current.setOpen(true));
    expect(keysOf(result.current.items)).toEqual(keysOf(items));
  });

  it("a group's children slide too, and the sub-level stands alone, without the group", () => {
    const grouped: Items = [{ type: "group", key: "reason", label: "You cannot add to this folder.", children: items }];
    const { result } = renderHook(() => useMenuLevel(grouped, true));
    const group = result.current.items[0];
    expect(group && "type" in group && group.type).toBe("group");
    const body = group && "children" in group && group.children ? group.children : [];
    expect(body.some(hasChildren)).toBe(false);
    act(() => result.current.onOpenChange(true, { source: "trigger" }));
    act(() => click(find(body, "submenu:document")));
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    expect(keysOf(result.current.items)).toEqual(["menu-level:back", "menu-level:divider", "blank:docx", "template:document"]);
  });

  it("the caller's own open state drives the reset: the context menu unmounts without a close ask", () => {
    const onOpenChange = vi.fn();
    const { result, rerender } = renderHook(({ open }) => useMenuLevel(items, true, { open, onOpenChange }), { initialProps: { open: true } });
    act(() => click(find(result.current.items, "submenu:document")));
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    expect(onOpenChange).not.toHaveBeenCalled();
    expect(keysOf(result.current.items)).toEqual(["menu-level:back", "menu-level:divider", "blank:docx", "template:document"]);
    // The page set its menu to null (a path change): the hook's level resets with the open it is given.
    rerender({ open: false });
    rerender({ open: true });
    expect(keysOf(result.current.items)).toEqual(keysOf(items));
    // An action's close reaches the caller.
    act(() => result.current.onOpenChange(false, { source: "menu" }));
    expect(onOpenChange).toHaveBeenCalledWith(false, { source: "menu" });
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

// The phone menus slide one level at a time (decision 185). Below antd's md breakpoint (768 px, the same test the file list
// reads for its phone layout) a submenu cannot open to the side: the popup is as wide as the screen less a gutter, so a side
// popup falls past the window's edge and widens the page. useMenuLevel turns antd items that carry children into two levels
// drawn in the same popup, at the same width: a first level where each submenu is a plain row ending in a chevron, and a
// sub-level of a back row, a divider and that submenu's own children. The New menu (newMenu.ts) and the row menu (FilesPage's
// groupedItems) both read it, so they cannot drift apart. Off a phone the items pass through unchanged and the submenus open
// to the side as before.
import { createElement, useEffect, useId, useRef, useState, type CSSProperties, type ReactNode } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import type { MenuProps } from "antd";
import { useT } from "./i18n";

type Items = NonNullable<MenuProps["items"]>;
type Item = Items[number];

// OpenInfo is the reason antd's Dropdown gives for an open change: "trigger" for the button, an outside tap or Escape,
// "menu" for a click on one of the menu's rows. antd asks to close on every row click; a slide row's click is not a close.
interface MenuOpenInfo {
  source: "trigger" | "menu";
}

// MenuLevelControl lets a caller that owns its own open state (the right-click menu, which unmounts when the page sets it to
// null) drive the hook: the hook reads open, forwards the closes that reach it, and resets its level when the caller closes.
interface MenuLevelControl {
  open: boolean;
  onOpenChange: (open: boolean, info: MenuOpenInfo) => void;
}

interface MenuLevelState {
  items: Items;
  open: boolean;
  onOpenChange: (open: boolean, info: MenuOpenInfo) => void;
  setOpen: (open: boolean) => void;
}

// Level is the submenu the phone has slid into: its key, the label the back row names, its children, and the row style its
// title carried, so the back row is the same height as the children below it.
interface Level {
  key: string;
  label: ReactNode;
  children: Items;
  style?: CSSProperties;
}

const CHEVRON_PROPS = { size: 16, strokeWidth: 1.75, "aria-hidden": true, focusable: false, className: "icon-dir" } as const;
// FORWARD is the chevron a submenu row ends in on a phone; newMenu.ts reads it too, so the New menu's submenu rows and the
// hook's own slide rows carry the one chevron, not two copies of it.
export const FORWARD = createElement(ChevronRight, CHEVRON_PROPS);
const BACK = createElement(ChevronLeft, CHEVRON_PROPS);

const hasChildren = (item: Item): boolean => !!item && "children" in item && Array.isArray((item as { children?: unknown }).children);

// slideRows turns a level's submenus into plain rows: no children (so antd draws no side popup and names no aria-haspopup),
// a chevron at the row's end (the caller's own, or this one), and a click that slides into the submenu. A group's children
// slide too; a plain row and a divider pass through. The original item is kept for the click, so its children are the ones
// the sub-level shows.
function slideRows(list: Items, enter: (item: Item) => void, uid: string): Items {
  return list.map((item) => {
    if (!item) return item;
    const rec = item as unknown as Record<string, unknown>;
    if (rec.type === "group") {
      return { ...item, children: slideRows((rec.children as Items) ?? [], enter, uid) };
    }
    if (hasChildren(item)) {
      const { children: _children, ...rest } = rec;
      return {
        ...rest,
        extra: rest.extra ?? FORWARD,
        "data-menu-level": "slide",
        "data-menu-level-id": uid,
        onClick: () => enter(item),
      } as unknown as Item;
    }
    return item;
  });
}

// useMenuLevel is the one helper the New and row menus read. With phone false it returns the items unchanged and glues the
// open state straight through. With phone true it draws one level at a time and keeps the popup open across a slide: antd's
// close ask that follows a slide row's click is ignored, and a close (an action, an outside tap, Escape) resets to the first
// level. After a level change the first focusable row of the new level takes the focus, for a keyboard user at a phone width.
export function useMenuLevel(items: Items, phone: boolean, control?: MenuLevelControl): MenuLevelState {
  const { t } = useT();
  const uid = useId();
  const [innerOpen, setInnerOpen] = useState(false);
  const [level, setLevel] = useState<Level | null>(null);
  // slideClick marks the close ask that follows a slide or back row's click, so it is not taken as a close.
  const slideClick = useRef(false);
  const open = control ? control.open : innerOpen;

  const enter = (item: Item) => {
    if (!item) return;
    const rec = item as unknown as Record<string, unknown>;
    slideClick.current = true;
    setLevel({
      key: String(rec.key ?? ""),
      label: rec.label as ReactNode,
      children: (rec.children as Items) ?? [],
      style: rec.style as CSSProperties | undefined,
    });
  };
  const back = () => {
    slideClick.current = true;
    setLevel(null);
  };

  const onOpenChange = (next: boolean, info: MenuOpenInfo) => {
    // A slide or back row was just clicked: antd asks to close on every row click, but this one changes the level, not the
    // open state. The flag is consumed here so the next close (an action, an outside tap) is taken normally.
    if (!next && info.source === "menu" && slideClick.current) {
      slideClick.current = false;
      return;
    }
    slideClick.current = false;
    if (!next) setLevel(null);
    if (control) control.onOpenChange(next, info);
    else setInnerOpen(next);
  };
  const setOpen = (next: boolean) => {
    if (!next) setLevel(null);
    if (control) control.onOpenChange(next, { source: "trigger" });
    else setInnerOpen(next);
  };

  // A caller that owns its open state may close without an open ask (the page sets its right-click menu to null on a path
  // change): the level resets with the open it is given, so the next opening is the first level again.
  const controlOpen = control?.open;
  useEffect(() => {
    if (controlOpen === false) setLevel(null);
  }, [controlOpen]);

  // The first focusable row of the level now shown takes the focus after a change. The menu is found by this hook's own
  // marker (a slide row on the first level, the back row on a sub-level), so another open menu is not touched.
  const shownKey = level?.key ?? null;
  const prevKey = useRef<string | null>(null);
  useEffect(() => {
    if (!phone || !open) {
      prevKey.current = null;
      return;
    }
    if (prevKey.current === shownKey) return;
    prevKey.current = shownKey;
    const focusFirst = () => {
      const marker = document.querySelector(`[data-menu-level-id="${uid}"]`);
      const menu = marker?.closest(".ant-dropdown-menu");
      const row = menu?.querySelector<HTMLElement>('li[role="menuitem"]:not(.ant-dropdown-menu-item-disabled)');
      row?.focus({ preventScroll: true });
    };
    focusFirst();
    // The popup mounts in a portal a frame after the first open; a level change while open is already drawn, but the retry
    // covers the first frame without fighting the dropdown's own focus on later frames.
    const raf = requestAnimationFrame(focusFirst);
    return () => cancelAnimationFrame(raf);
  }, [phone, open, shownKey, uid]);

  if (!phone) {
    return { items, open, onOpenChange, setOpen };
  }
  if (level === null) {
    return { items: slideRows(items, enter, uid), open, onOpenChange, setOpen };
  }
  const name = typeof level.label === "string" ? level.label : level.key;
  const backRow = {
    key: "menu-level:back",
    label: createElement("strong", null, level.label),
    icon: BACK,
    "aria-label": t("menu.back", { name }),
    "data-menu-level": "back",
    "data-menu-level-id": uid,
    style: level.style,
    onClick: back,
  } as Item;
  const shown: Items = [backRow, { type: "divider", key: "menu-level:divider" }, ...slideRows(level.children, enter, uid)];
  return { items: shown, open, onOpenChange, setOpen };
}

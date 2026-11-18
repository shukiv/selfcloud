// SPDX-License-Identifier: AGPL-3.0-or-later

import {
  createContext,
  memo,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type DragEvent as ReactDragEvent,
  type FocusEvent as ReactFocusEvent,
  type PointerEvent as ReactPointerEvent,
  type CSSProperties,
  type ReactNode,
  type Ref,
} from "react";
import { Button, Dropdown, Grid, Input, Table, theme as antdTheme, type MenuProps } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ArrowDown, ArrowUp, Check, EllipsisVertical, Link2, Minus, Plus, Star } from "lucide-react";
import EmptyState from "../EmptyState";
import IconButton from "../IconButton";
import { translatePlural, useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import type { SortDir, SortKey } from "../../lib/files/sort";
import type { Node } from "../../lib/files/types";
import type { ViewMode } from "../../lib/files/viewPrefs";
import { useDensity, type Density } from "../../lib/files/density";
import { isTextField } from "../../lib/files/keys";
import { useCoarsePointer } from "../../lib/pointer";
import { HEADER_H, THEAD_H } from "../../lib/files/layout";
import { WINDOW_MIN_ROWS, sameRange, spacers, windowRange, withPinned, type WindowRange } from "../../lib/files/window";
import { useListKeys, useListSelection, type ListSelection } from "../../lib/lists";
import { getRowHover, setRowHover, subscribeRowHover } from "./rowHover";
import Icon from "./Icon";
import CheckBadge from "./CheckBadge";
import PresetMark from "./PresetMark";
import KindMarks from "./KindMarks";
import SelectionBar, { type SelectionAction } from "./SelectionBar";
import Thumb from "./Thumb";
import { SIDEBAR } from "../../theme/tokens";
import { useModeOrLight } from "../../theme/theme";

// The hook lives in lib/pointer, so the shell can use it without loading this listing; it is still exported from here.
export { useCoarsePointer };


// Click is what the page needs from a mouse or touch event: which modifiers were held, and whether a finger made it.
// A finger's tap opens the item; a mouse's click selects it, and its double click opens it (the page decides).
export interface Click {
  shiftKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  touch?: boolean;
}

// RowAct is one of the four icons at a list row's end (Share, Download, Rename, Star): the page builds them from the same
// rules as the menu, so an icon is shown only where the menu would offer the action, and enabled.
export interface RowAct {
  key: string;
  label: string;
  icon: ReactNode;
  enabled: boolean;
  run: () => void;
}

// ListingProps are the props of the files preset, My files (the default): the page owns the selection, the keys, the menu
// and the rename, and Listing reports clicks, keys and focus. A caller that passes columns gets a preset list instead
// (ItemListingProps); the files preset has none.
export interface ListingProps {
  columns?: undefined;
  items: Node[];
  view: ViewMode;
  selected: Set<string>;
  focusID: string | null;
  sortKey: SortKey;
  sortDir: SortDir;
  renameID: string | null;
  renameValue: string;
  renameError: string | null;
  label: string;
  onRowClick: (index: number, e: Click) => void;
  // onRowDoubleClick gets the ID of the item the pointer was on, so a re-sort between the clicks cannot change it.
  onRowDoubleClick: (id: string) => void;
  onSelectAll: (checked: boolean) => void;
  onSort: (key: SortKey) => void;
  onRenameChange: (value: string) => void;
  onRename: (commit: boolean) => void;
  onKeyDown: (e: ReactKeyboardEvent<HTMLElement>) => void;
  onFocusItem: (index: number) => void;
  // focusRequest asks for the focus on a row, by its ID, for a key that moved it (decision 183). A row that is not drawn is
  // scrolled to first, then focused once it is drawn. A new request is a new seq, so the same row can be asked for again.
  focusRequest?: { id: string; seq: number };
  // rowActs gives the icons at a list row's end for one item. They are drawn with a mouse, where the row is hovered or selected.
  rowActs?: (n: Node) => RowAct[];
  // onRowContext asks for the item's menu: a right-click, or a finger held on the item for a moment. It gets the pointer's
  // place in the window, so the menu opens there.
  // keyboardOrigin is the ⋮ button when a key pressed it: the menu then takes the focus, and gives it back to the button.
  onRowContext?: (id: string, index: number, x: number, y: number, keyboardOrigin?: HTMLElement) => void;
  // cutIDs are the items a cut has taken: they are shown dimmed until they are pasted or the cut is cancelled.
  cutIDs?: Set<string>;
  // visitor is a visitor of a link: no selection, no owner's data, no drag. The row icons are drawn on every row at once
  // (a phone has no hover), the tiles show the type icon instead of a thumbnail, and the More button gives way to the icons.
  visitor?: boolean;
  // stickyTop is where the list's column header is pinned: under the shell's header and the toolbar's row.
  stickyTop?: number;
  containerRef?: Ref<HTMLDivElement>;
}

// ListColumn is one column of a preset list (the design, not in this repository, section 2.1). The first column is the name cell: the row's icon, then what
// the column renders. A secondary column is a column on a desktop; below md (768 px) its value is the name cell's second
// line, after the ones before it.
export interface ListColumn<T> {
  key: string;
  title: string;
  render: (row: T) => ReactNode;
  secondary?: boolean;
  // sortKey is the key this header sorts by, when the list has a sort. Without it the header is not a button.
  sortKey?: string;
  // ariaSort marks a column the rows are in order of with no control, such as a time column that lists newest first.
  ariaSort?: "descending";
  align?: "start" | "end";
  width?: number;
}

// ListAction is an icon at a row's end. Up to three show inline with a mouse on a desktop; the rest, and all of them on a
// phone or a finger, are entries of the row's ⋮ menu. A page lists only the actions that apply to the row; a disabled one
// (a request on its way) keeps its place, disabled, inline and in the menu.
export interface ListAction {
  key: string;
  label: string;
  icon: ReactNode;
  run: () => void;
  disabled?: boolean;
  danger?: boolean;
  // pressed is the state of a toggle-like action, such as a group's Members while that group is open: the inline icon says it
  // as aria-pressed (true or false). Left out, the icon has no pressed state. A menu entry has none (the design, not in this repository, section 7).
  pressed?: boolean;
  // href makes the action a real link: inline it is an anchor the browser follows, so a middle click opens a
  // new tab and Copy link works; in the ⋮ menu it is the menu's link, and a plain click runs run.
  href?: string;
}

// ListMenuEntry is one entry of a row's ⋮ menu, after the row's actions that do not fit inline. An entry with an href is
// a real link: a middle or a modified click opens it in a new tab, as the browser does, and a plain click or a key runs
// onSelect in this tab.
export interface ListMenuEntry {
  key: string;
  label: string;
  icon?: ReactNode;
  danger?: boolean;
  disabled?: boolean;
  href?: string;
  onSelect: () => void;
}

// ListSort is a preset list's sort: the key and the direction it is in, and what a header's click asks for.
export interface ListSort {
  key: string;
  dir: "asc" | "desc";
  onSort: (key: string) => void;
}

// ListEmpty is what an empty preset list shows inside its table, under the column headers (the owner's rule of
// 2026-11-09, decision 114): a bold title, one muted line of description, and the page's create action, a primary button
// with a + icon, when the page has one.
export interface ListEmpty {
  title: string;
  description?: string;
  action?: { label: string; onClick: () => void };
  // className is the empty state's class, for the page's own hooks (an e2e check reads it).
  className?: string;
}

// ItemListingProps are the props of a preset list: the admin, Manage and drive lists other than My files. Its columns,
// rows, actions and menu come from the page; the selection and the keys are the shared hooks (lib/lists.ts).
export interface ItemListingProps<T extends { id: string }> {
  items: T[];
  // label names the list's region; rowLabel names each row, for a screen reader and for the row's checkbox.
  label: string;
  rowLabel: (row: T) => string;
  columns: ListColumn<T>[];
  // moreLabel names a row's ⋮ button. Without it the button is "More actions for" the row's label.
  moreLabel?: (row: T) => string;
  icon?: (row: T) => ReactNode;
  actions?: (row: T) => ListAction[];
  menu?: (row: T) => ListMenuEntry[];
  // rowClassName gives a row its class, for the page's own hooks and styles.
  rowClassName?: (row: T) => string;
  // rowTestID gives a row its test id, for the page's e2e checks (tl5, decision 138).
  rowTestID?: (row: T) => string | undefined;
  // phone forces the phone layout whatever the window is: a secondary column is the name's second line, and the row's icons are
  // the ⋮ menu. A narrow side panel asks for it, since the window is wider than the panel (tl5, decision 138).
  phone?: boolean;
  // selectable gives the list a selection: checkboxes, the selection keys, and a selection bar with the bulk icons.
  selectable?: boolean;
  bulk?: (ids: string[]) => SelectionAction[];
  sort?: ListSort;
  // onOpen opens a row: Enter on the row, or a plain click on it. On a list with a selection, Ctrl, Cmd and Shift clicks and the
  // checkboxes select instead (tl4).
  onOpen?: (row: T) => void;
  // empty is what the table's body shows when there are no rows, under the header row, which stays (the design, not in this repository, section 2.5).
  empty: ListEmpty;
  // stickyTop is where the column header pins; the default is under the shell's header, as a preset list has no toolbar row.
  stickyTop?: number;
}

function isItems<T extends { id: string }>(p: ListingProps | ItemListingProps<T>): p is ItemListingProps<T> {
  return p.columns !== undefined;
}

const NO_IDS: ReadonlySet<string> = new Set();

// LONG_PRESS_MS is how long a finger rests on an item before it opens the item's menu. PRESS_SLOP_PX is how far it may move
// meanwhile: a finger that moves is scrolling, not pressing.
const LONG_PRESS_MS = 500;
const PRESS_SLOP_PX = 10;
// CUT_OPACITY is how faint a cut item is shown, until it is pasted or the cut is cancelled.
const CUT_OPACITY = 0.5;
// TILE_MIN_PX is the narrowest a tile is in the tile view. At 1920 wide the listing is 1600 pixels beside the sidebar, which
// gives five columns of 260 pixels and a gap of 12; a 320 pixel window still fits one column.
const TILE_MIN_PX = 260;
// PREVIEW_H_PX is the height of a file card's preview: the same for every card, so the cards of a row are one height.
const PREVIEW_H_PX = 120;
// ROW_ICON_PX is the width of one of a row's icon buttons. The row end keeps room for four icons and the More button, so a row
// does not move when its icons appear.
const ROW_ICON_PX = 40;
const ROW_ICONS = 4;
// DEFAULT_STICKY_TOP is the shell's header (56 px) and the toolbar's row (48 px with a mouse), where the column header pins.
const DEFAULT_STICKY_TOP = 104;

// emptySecondCell says a phone second-line cell draws nothing (mob4): an absent value, or text that is only whitespace. Such a
// cell is left out of the line, so no separator is drawn before, after or beside it. A cell that renders an element is kept.
function emptySecondCell(node: ReactNode): boolean {
  if (node === null || node === undefined || typeof node === "boolean") return true;
  if (typeof node === "string") return node.trim() === "";
  if (Array.isArray(node)) return node.every(emptySecondCell);
  return false;
}

type AriaSort = "ascending" | "descending" | "none";

// ariaSortOf is a header's aria-sort: the list's direction when it is sorted by the header's column, none when it is not.
function ariaSortOf(dir: "asc" | "desc" | null): AriaSort {
  if (dir === null) return "none";
  return dir === "asc" ? "ascending" : "descending";
}

// RowActs draws a list row's icons only while the row is hovered by a mouse, or selected, or has the focus: the icons
// are built by `children` only then, so a folder of 500 rows draws the few rows the person is on, not all of them.
function RowActs({ id, show, children }: { id: string; show: boolean; children: () => ReactNode }) {
  const hovered = useSyncExternalStore(subscribeRowHover, () => getRowHover() === id);
  return hovered || show ? <>{children()}</> : null;
}

// usePressFocus makes a press on a row and its click one update. The browser focuses a row on the press, and the click then
// selects it, and both used to set state: two renders of the list for one click. A focus that comes during a mouse's press is the
// press's own, so it is held, and the click applies it, in the click's bubble phase, in the batch of the click's own update.
// It is not applied in the capture phase: a browser lets React's microtask run between the root's capture listener and its bubble
// listener, so an update made in the capture phase is drawn before the click's own handlers run. That is a second pass over the
// list, and a controlled checkbox that React draws in between loses its click.
// pressing is the row whose press has not ended; pendingClick is a row whose release was on that same row, so its click is still to
// come. The click reaches the row's own handler (settle), or the handler of a control in the row that stops the click (it settles
// too), or the checkbox's selection, which sets the focus itself (consume). If none does, the click did not come: the focus is
// applied a turn later. A release anywhere else, or a cancel, has no click on the row: the focus is applied there if it is in the
// row, which is what the browser did on the press, and otherwise left alone. A finger's focus comes after its release (the
// browser's mouse events for a tap), so a touch holds nothing. Any key but a modifier, or a window that loses its focus, ends a
// press that was never released (the focus is applied if it is in the row), so a keyboard focus is never held.
function usePressFocus(apply: (id: string) => void) {
  const applyRef = useRef(apply);
  applyRef.current = apply;
  const pressing = useRef<string | null>(null);
  const pendingClick = useRef<string | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const clearTimer = () => {
    if (timer.current !== null) clearTimeout(timer.current);
    timer.current = null;
  };
  useEffect(() => {
    const release = (e: PointerEvent) => {
      const id = pressing.current;
      if (id === null) return;
      pressing.current = null;
      const target = e.target instanceof Element ? e.target : null;
      if (e.type === "pointerup" && target?.closest(`[data-id="${CSS.escape(id)}"]`)) {
        pendingClick.current = id;
        // A mouse's click follows its release in the same turn of the event loop, so this runs after it.
        clearTimer();
        timer.current = setTimeout(() => {
          timer.current = null;
          if (pendingClick.current !== id) return;
          pendingClick.current = null;
          applyRef.current(id);
        }, 0);
        return;
      }
      const focusedRow = document.activeElement instanceof Element ? document.activeElement.closest("[data-id]") : null;
      if (focusedRow?.getAttribute("data-id") === id) applyRef.current(id);
    };
    // end is a key but a modifier, or a window that loses its focus, during a press: the press is over, and the focus it holds is
    // applied if it is still in the row, as a cancel does.
    const end = (e: Event) => {
      // A modifier moves no focus, and a held one repeats its key on some systems, so it does not end a press.
      if (e instanceof KeyboardEvent && (e.key === "Control" || e.key === "Shift" || e.key === "Alt" || e.key === "Meta")) return;
      const id = pressing.current;
      pressing.current = null;
      pendingClick.current = null;
      clearTimer();
      if (id !== null && document.activeElement?.closest("[data-id]")?.getAttribute("data-id") === id) applyRef.current(id);
    };
    window.addEventListener("pointerup", release, true);
    window.addEventListener("pointercancel", release, true);
    window.addEventListener("keydown", end, true);
    // A blur that reaches the window itself (it does not bubble, so a control's blur is not one).
    window.addEventListener("blur", end);
    return () => {
      window.removeEventListener("pointerup", release, true);
      window.removeEventListener("pointercancel", release, true);
      window.removeEventListener("keydown", end, true);
      window.removeEventListener("blur", end);
      clearTimer();
    };
    // clearTimer reads a ref only: it is the same at every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return useMemo(
    () => ({
      // down is a press on the row.
      down: (id: string, pointerType: string) => {
        clearTimer();
        pendingClick.current = null;
        pressing.current = pointerType === "touch" ? null : id;
      },
      // focus is a focus that reached the row: a press's own is held, any other is applied.
      focus: (id: string) => {
        if (pressing.current === id) return;
        applyRef.current(id);
      },
      // settle applies the focus a press held, with the click that came of it: it is called from the click's own handlers.
      settle: (id: string) => {
        if (pendingClick.current !== id) return;
        pendingClick.current = null;
        clearTimer();
        applyRef.current(id);
      },
      // consume drops the focus a press held, for a click whose own update sets the focus (a selection).
      consume: (id: string) => {
        if (pendingClick.current !== id) return;
        pendingClick.current = null;
        clearTimer();
      },
    }),
    // clearTimer reads a ref only: it is the same at every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );
}

// keysStop stops Enter and Space at a row's button, so its own click is the one activation; the arrow keys reach the item's
// handler, so they move on from the button.
function keysStop(e: { key: string; stopPropagation: () => void }) {
  if (e.key === "Enter" || e.key === " ") e.stopPropagation();
}

const MORE_ICON = <EllipsisVertical size={18} aria-hidden="true" focusable="false" />;

// menuItemOf is a ⋮ entry. An entry with an address is a real link: the browser opens a middle or a modified click in a new
// tab, and a plain click or a key runs the entry here, in this tab. On a coarse pointer the entry is a finger's size (mob3).
function menuItemOf(e: ListMenuEntry, select: () => void, coarse: boolean): NonNullable<MenuProps["items"]>[number] {
  return {
    key: e.key,
    icon: e.icon,
    danger: e.danger,
    disabled: e.disabled,
    style: coarse ? { height: 44, paddingBlock: 0, lineHeight: "44px" } : undefined,
    label: e.href ? <a href={e.href}>{e.label}</a> : e.label,
    onClick: ({ domEvent }) => {
      if (e.href && "button" in domEvent && (domEvent.button !== 0 || domEvent.metaKey || domEvent.ctrlKey || domEvent.shiftKey || domEvent.altKey)) return;
      domEvent.preventDefault();
      select();
    },
  };
}

// ---- A row's and a tile's content, memoised (decision 128). ----
// Each is drawn again only when its own props change: the item, whether it is selected or focused, the rename state when the row
// is the one being renamed. The page's callbacks are read through one handler object that is the same on every render, so a page
// render that makes new callbacks does not draw every row again.

// FileHandlers are the files preset's callbacks, by item ID. They read the page's current props at the call.
interface FileHandlers {
  click: (id: string, e: Click) => void;
  dbl: (id: string) => void;
  renameChange: (value: string) => void;
  rename: (commit: boolean) => void;
  keyDown: (e: ReactKeyboardEvent<HTMLElement>) => void;
  // settle applies the focus the press on the item held, with the click of a control that stops the click at the control.
  settle: (id: string) => void;
  context: (id: string, x: number, y: number, keyboardOrigin?: HTMLElement) => void;
  acts: (n: Node) => RowAct[];
  act: (n: Node, key: string) => void;
}

// RowHandlers are the DOM handlers of a row or a tile. They name the item by the element's data-id, so one set serves every row.
type RowHandlers = Record<string, unknown>;

// FileCellProps are the props of a files row's or tile's content.
interface FileCellProps {
  n: Node;
  selected: boolean;
  coarse: boolean;
  renaming: boolean;
  renameValue: string;
  renameError: string | null;
  visitor: boolean;
  H: FileHandlers;
}

const CTRL_CLICK: Click = { shiftKey: false, ctrlKey: true, metaKey: false };

// HeadMark is the select-all mark in the name's header: a round button that reads false (an empty outline), mixed (a filled
// circle with a minus) or true (a filled circle with a check). It is always drawn, on every pointer, and is a tab stop. A click
// selects every loaded row, or clears the selection when all are selected.
function HeadMark({ state, onSelectAll }: { state: "none" | "some" | "all"; onSelectAll: (checked: boolean) => void }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const dark = useModeOrLight() === "dark";
  const filled = state !== "none";
  const rim = dark ? "" : ", 0 0 0 3px rgba(13, 33, 66, 0.55)";
  return (
    <button
      type="button"
      role="checkbox"
      aria-checked={state === "all" ? "true" : state === "some" ? "mixed" : "false"}
      aria-label={t("files.selectAll.box")}
      className="head-mark"
      onClick={(e) => {
        e.stopPropagation();
        onSelectAll(state !== "all");
      }}
      onKeyDown={keysStop}
    >
      <span
        aria-hidden="true"
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          width: 17,
          height: 17,
          borderRadius: "50%",
          boxSizing: "border-box",
          background: filled ? SIDEBAR.active : "transparent",
          color: filled ? SIDEBAR.onActive : token.colorTextSecondary,
          border: filled ? "none" : `2px solid ${token.colorTextSecondary}`,
          boxShadow: filled ? `0 0 0 2px ${token.colorBgContainer}${rim}` : undefined,
        }}
      >
        {state === "all" && <Check size={11} strokeWidth={3} aria-hidden="true" focusable="false" />}
        {state === "some" && <Minus size={11} strokeWidth={3} aria-hidden="true" focusable="false" />}
      </span>
    </button>
  );
}

function LinkBadge({ node }: { node: Node }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  if (!node.has_link) return null;
  return (
    <span style={{ display: "inline-flex", marginInlineStart: 8, color: token.colorTextSecondary, verticalAlign: "middle" }}>
      <Link2 size={14} aria-hidden="true" focusable="false" />
      <span className="sr-only">{t("files.linkBadge")}</span>
    </span>
  );
}

// StarMark: a starred item shows a small filled star after its name. The star is changed from the hover icon, the ⋮ menu or the
// Starred page; the mark is not a button.
function StarMark({ node }: { node: Node }) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  if (!node.starred) return null;
  return (
    <span style={{ display: "inline-flex", marginInlineStart: 4, color: token.colorWarning, verticalAlign: "middle", flex: "none" }}>
      <Star size={14} fill="currentColor" aria-hidden="true" focusable="false" />
      <span className="sr-only">{t("star.mark")}</span>
    </span>
  );
}

function RenameError({ error }: { error: string | null }) {
  const { token } = antdTheme.useToken();
  if (!error) return null;
  return (
    <div id="rename-error" role="alert" style={{ flexBasis: "100%", color: token.colorError, fontSize: token.fontSizeSM }}>
      {error}
    </div>
  );
}

// FileRename is the name field of a row or tile being renamed.
function FileRename({ value, error, H }: { value: string; error: string | null; H: FileHandlers }) {
  const { t } = useT();
  return (
    <Input
      className="rename"
      aria-label={t("files.rename.label")}
      aria-invalid={error ? true : undefined}
      aria-describedby={error ? "rename-error" : undefined}
      value={value}
      onChange={(e) => H.renameChange(e.target.value)}
      onKeyDown={(e) => {
        // Keys typed in the name field stay in the field: Enter must not also open the row.
        e.stopPropagation();
        if (e.key === "Enter") {
          e.preventDefault();
          H.rename(true);
        } else if (e.key === "Escape") {
          e.preventDefault();
          H.rename(false);
        }
      }}
      onBlur={() => H.rename(true)}
      ref={(el) => {
        if (el) {
          el.focus();
          el.select();
        }
      }}
      style={{ flex: "1 1 0", minWidth: 0, minHeight: 44 }}
    />
  );
}

// FileMore is a row's or a tile's ⋮ button. It opens the item's menu where the right-click menu opens: at the button.
const FileMore = memo(function FileMore({ n, tab, H }: { n: Node; tab: number; H: FileHandlers }) {
  const { t } = useT();
  return (
    <IconButton
      tabIndex={tab}
      label={t("files.tile.menu", { name: n.name })}
      tip={t("files.selection.more")}
      aria-haspopup="menu"
      onClick={(e) => {
        e.stopPropagation();
        H.settle(n.id);
        const r = e.currentTarget.getBoundingClientRect();
        // The menu opens at the button's start edge: its left in English, its right in Hebrew. A key's click has
        // no pointer, so its detail is 0: the menu then takes the focus.
        // The start edge is the button's x in English and its x plus its width in Hebrew, where the start is on the right.
        const rtl = document.documentElement.dir === "rtl";
        const startX = rtl ? r.x + r.width : r.x;
        H.context(n.id, startX, r.bottom, e.detail === 0 ? e.currentTarget : undefined);
      }}
      onKeyDown={keysStop}
      onDoubleClick={(e) => e.stopPropagation()}
      icon={MORE_ICON}
    />
  );
});

// FileNameCell is the name cell of a list row: the row's icon in its selection mark (a mouse), the name, its marks. A selected
// row shows a yellow check badge on its icon; a visitor's row has neither mark nor badge.
const FileNameCell = memo(function FileNameCell({ n, selected, coarse, renaming, renameValue, renameError, visitor, H }: FileCellProps) {
  const { t } = useT();
  const { token } = antdTheme.useToken();
  const icon = <Icon kind={kindOf(n)} size={22} colour={n.color} node={n} />;
  return (
    // verticalAlign middle: the line box does not take the baseline of the icon (decision 85, fu5). Without it the name moved
    // 0.75 px up on a hovered row.
    // While renaming the icon, the field and the marks share one line that does not wrap (mob4): the field grows from no
    // basis into the room there is. A rename error keeps its own line, so the cell wraps again to let it drop below.
    <span style={{ display: "inline-flex", alignItems: "center", gap: 8, flexWrap: renaming && !renameError ? "nowrap" : "wrap", minWidth: 0, maxWidth: "100%", verticalAlign: "middle" }}>
      {visitor ? (
        icon
      ) : coarse ? (
        // A finger's row keeps its icon plain (a tap opens the item); a long press selects it, and the badge then shows on the icon.
        <span style={{ position: "relative", display: "inline-flex", flex: "none", width: 22, height: 22 }}>
          <span className="row-icon">{icon}</span>
          {selected && <CheckBadge size={17} ring={token.colorPrimaryBg} pos={{ insetBlockEnd: -3, insetInlineEnd: -3 }} />}
        </span>
      ) : (
        // A mouse's mark is the icon wrapped in a button: a click on it toggles the row, as Ctrl does, and opens nothing. It is
        // not a tab stop (the row takes focus), and the badge shows on the icon when the row is selected.
        <button
          type="button"
          className="row-mark"
          tabIndex={-1}
          aria-pressed={selected}
          aria-label={t("files.select.item", { name: n.name })}
          style={{ width: 22, height: 22 }}
          onClick={(e) => {
            e.stopPropagation();
            H.click(n.id, CTRL_CLICK);
          }}
          onKeyDown={keysStop}
          onDoubleClick={(e) => e.stopPropagation()}
        >
          <span className="row-icon">{icon}</span>
          {selected && <CheckBadge size={17} ring={token.colorPrimaryBg} pos={{ insetBlockEnd: -3, insetInlineEnd: -3 }} />}
        </button>
      )}
      {renaming ? (
        <FileRename value={renameValue} error={renameError} H={H} />
      ) : (
        // The name is one line (decision 161): a long one is cut with an ellipsis, and its full text is the title. It starts at
        // no width and takes the room the icons and the marks leave, so the marks after it keep their line.
        <span className="row-name" title={n.name} style={{ flex: "1 1 0", minWidth: 0, overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>
          <bdi>{n.name}</bdi>
        </span>
      )}
      {/* The marks: a link, a kind, a star. They do not wrap, and an empty group takes no room (index.css, .row-marks:empty).
          A visitor has none of the owner's marks: the link and the star describe the owner's links and stars, not theirs. */}
      <span className="row-marks" style={{ display: "inline-flex", alignItems: "center", gap: 8, flex: "none", whiteSpace: "nowrap" }}>
        {!visitor && <LinkBadge node={n} />}
        <KindMarks node={n} />
        {!visitor && <StarMark node={n} />}
      </span>
      <RenameError error={renaming ? renameError : null} />
    </span>
  );
});

// RowActIcons draws the icons the page built for a row. A files action that is not enabled is one the item's menu does not
// offer, so it is not drawn (unlike a preset list's disabled action, which is one on its way).
function RowActIcons({ n, tab, H }: { n: Node; tab: number; H: FileHandlers }) {
  return (
    <>
      {H.acts(n)
        .filter((a) => a.enabled)
        .map((a) => (
          <IconButton
            key={a.key}
            label={a.label}
            icon={a.icon}
            tabIndex={tab}
            onClick={(e) => {
              e.stopPropagation();
              H.settle(n.id);
              H.act(n, a.key);
            }}
            onKeyDown={keysStop}
            onDoubleClick={(e) => e.stopPropagation()}
          />
        ))}
    </>
  );
}

// FileRowEnd is a list row's end: the page's row icons (drawn with a mouse, where the row is hovered, selected or focused) and the
// ⋮ button. sig is the row's actions as text, given for a selected or focused row: it is not read, but a change in it draws the
// row's end again, so an icon follows the page's actions. A row that is only hovered reads the actions when it draws its icons.
// A visitor's row (pb1) has no ⋮ button and its icons are drawn at once, on every row, for a mouse and a finger alike.
const FileRowEnd = memo(function FileRowEnd({ n, selected, focused, coarse, visitor, H }: { n: Node; selected: boolean; focused: boolean; coarse: boolean; visitor: boolean; sig: string; H: FileHandlers }) {
  const tab = focused ? 0 : -1;
  if (visitor) {
    return (
      <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "nowrap" }}>
        <span className="row-acts" style={{ display: "inline-flex", justifyContent: "flex-end" }}>
          <RowActIcons n={n} tab={tab} H={H} />
        </span>
      </div>
    );
  }
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "nowrap" }}>
      {!coarse && (
        <span className="row-acts" style={{ display: "inline-flex", justifyContent: "flex-end", minWidth: ROW_ICON_PX * ROW_ICONS }}>
          <RowActs id={n.id} show={selected || focused}>
            {() => <RowActIcons n={n} tab={tab} H={H} />}
          </RowActs>
        </span>
      )}
      <FileMore n={n} tab={tab} H={H} />
    </div>
  );
});

// actsSig is a row's actions as text: the keys, which are enabled, and the labels.
function actsSig(acts: RowAct[]): string {
  return acts.map((a) => `${a.key}${a.enabled ? "+" : "-"}${a.label}`).join("|");
}

// TypeIcon is a file's type icon, which keeps its size when the name is long. A folder's chip shows the selection badge on it
// (badgeSize); a file's card shows the badge on its preview instead, so its name-row icon has none.
function TypeIcon({ n, badgeSize, selected, visitor }: { n: Node; badgeSize?: number; selected: boolean; visitor: boolean }) {
  const { token } = antdTheme.useToken();
  return (
    <span style={{ position: "relative", display: "inline-flex", flex: "none" }}>
      <Icon kind={kindOf(n)} size={20} colour={n.color} node={n} />
      {badgeSize !== undefined && selected && !visitor && <CheckBadge size={badgeSize} ring={token.colorPrimaryBg} pos={{ insetBlockStart: -3, insetInlineStart: -3 }} />}
    </span>
  );
}

// OneLine is a tile's name: one line, cut with an ellipsis, with its full text in the title. Being renamed, it is the name field.
// The box takes the row's direction and sits at its start, so a name in another script does not drift to the far end; the bdi keeps
// the name's own direction.
function OneLine({ n, renaming, renameValue, renameError, H }: { n: Node; renaming: boolean; renameValue: string; renameError: string | null; H: FileHandlers }) {
  if (renaming) return <FileRename value={renameValue} error={renameError} H={H} />;
  return (
    <span
      style={{
        display: "block",
        flex: "1 1 auto",
        minWidth: 0,
        overflow: "hidden",
        direction: "inherit",
        textAlign: "start",
        textOverflow: "ellipsis",
        whiteSpace: "nowrap",
      }}
    >
      <bdi title={n.name}>{n.name}</bdi>
    </span>
  );
}

// rowDomAttrs gives a row or a tile its role, name, selection state, roving focus and handlers. Both views use it, so the keyboard
// model is the same in each. A visitor's row takes no drop (pb1): there is nothing to drag, and nowhere to drop it.
function rowDomAttrs(n: Node, selected: boolean, focused: boolean, RH: RowHandlers, visitor = false) {
  return {
    role: "row",
    "aria-label": n.name,
    "aria-selected": selected,
    tabIndex: focused ? 0 : -1,
    "data-id": n.id,
    // A folder is a place an item can be dropped on: the page reads where from these (see useGestures). Nothing else is.
    ...(n.kind === "dir" && !visitor ? { "data-drop-folder": n.folder_id, "data-drop-dir": n.id, "data-drop-role": n.role ?? "", "data-drop-name": n.name } : {}),
    ...RH,
  };
}

interface TileProps extends FileCellProps {
  kind: "chip" | "card";
  focused: boolean;
  cut: boolean;
  scrollMarginTop: number;
  RH: RowHandlers;
}

// FileTile is a tile of the tile view: a folder's chip or a file's card, with its box, its state and its content.
const FileTile = memo(function FileTile({ kind, n, selected, focused, cut, coarse, renaming, renameValue, renameError, visitor, scrollMarginTop, H, RH }: TileProps) {
  const { token } = antdTheme.useToken();
  const outer = {
    position: "relative" as const,
    display: "flex",
    minWidth: 0,
    border: `1px solid ${selected ? token.colorPrimary : token.colorBorderSecondary}`,
    borderRadius: token.borderRadiusLG,
    // A selected tile has an outline as well as its tint, so the selection shows at a glance.
    outline: selected ? `2px solid ${token.colorPrimary}` : undefined,
    outlineOffset: selected ? -2 : undefined,
    cursor: "default",
    userSelect: "none" as const,
    scrollMarginTop,
    opacity: cut ? CUT_OPACITY : undefined,
  };
  if (kind === "chip") {
    return (
      <div
        {...rowDomAttrs(n, selected, focused, RH, visitor)}
        role="option"
        style={{
          ...outer,
          alignItems: "center",
          gap: 6,
          minHeight: coarse ? 44 : 40,
          paddingInlineStart: 10,
          paddingInlineEnd: 4,
          boxSizing: "border-box",
          background: selected ? token.colorPrimaryBg : undefined,
        }}
      >
        <TypeIcon n={n} badgeSize={17} selected={selected} visitor={visitor} />
        <OneLine n={n} renaming={renaming} renameValue={renameValue} renameError={renameError} H={H} />
        <KindMarks node={n} />
        {!visitor && <StarMark node={n} />}
        {visitor ? <RowActIcons n={n} tab={focused ? 0 : -1} H={H} /> : <FileMore n={n} tab={-1} H={H} />}
        <RenameError error={renaming ? renameError : null} />
      </div>
    );
  }
  return (
    <div
      {...rowDomAttrs(n, selected, focused, RH, visitor)}
      role="option"
      style={{
        ...outer,
        flexDirection: "column",
        overflow: "hidden",
        background: selected ? token.colorPrimaryBg : token.colorBgContainer,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, minHeight: coarse ? 44 : 40, paddingInlineStart: 10, paddingInlineEnd: 4 }}>
        <TypeIcon n={n} selected={selected} visitor={visitor} />
        <OneLine n={n} renaming={renaming} renameValue={renameValue} renameError={renameError} H={H} />
        {!visitor && <StarMark node={n} />}
        {visitor ? <RowActIcons n={n} tab={focused ? 0 : -1} H={H} /> : <FileMore n={n} tab={-1} H={H} />}
      </div>
      {/* The preview is a fixed height, so every card is as tall as the others whatever its name. The thumbnail is shown where
          there is one; the rest show the type's icon on a tinted area. The play mark and the star sit in its corners. A visitor's
          tile shows the type icon alone (pb1): a thumbnail is asked for on a signed-in endpoint the visitor cannot reach. */}
      <div style={{ position: "relative", display: "flex", alignItems: "center", justifyContent: "center", height: PREVIEW_H_PX, minWidth: 0, background: token.colorFillQuaternary }}>
        {thumbable(n) && !visitor ? (
          <Thumb nod={n.id} version={n.sha256 ?? n.etag ?? ""} height={PREVIEW_H_PX} fallback={<Icon kind={kindOf(n)} size={48} colour={n.color} node={n} />} />
        ) : (
          <Icon kind={kindOf(n)} size={48} colour={n.color} node={n} />
        )}
        {/* A selected card shows the check badge at the preview's top-start corner; a visitor's tile has none (pb1). */}
        {selected && !visitor && <CheckBadge size={28} ring={token.colorPrimaryBg} pos={{ insetBlockStart: 6, insetInlineStart: 6 }} />}
        <span style={{ position: "absolute", insetBlockEnd: 6, insetInlineStart: 6, display: "inline-flex", alignItems: "center", maxWidth: "calc(100% - 60px)" }}>
          <KindMarks node={n} />
          {!visitor && <LinkBadge node={n} />}
        </span>
      </div>
      <RenameError error={renaming ? renameError : null} />
    </div>
  );
});

// ---- The windowed list (decision 183). Above 150 loaded rows, My files' list draws only the rows near the screen. ----

// WindowCtx is what the list's table reads from Listing: whether the list is windowed, the two spacers' heights, the loaded rows
// for aria-rowcount, and the columns a spacer row spans.
interface WindowCtxValue {
  windowed: boolean;
  top: number;
  bottom: number;
  rows: number;
  cols: number;
}
const WindowCtx = createContext<WindowCtxValue>({ windowed: false, top: 0, bottom: 0, rows: 0, cols: 0 });

// SpacerRow holds the place of the rows that are not drawn: one cell across the table, as high as the rows it stands for. It is
// hidden from a screen reader and takes no pointer, so it shows no band and catches no click.
function SpacerRow({ which, height, cols }: { which: "top" | "bottom"; height: number; cols: number }) {
  return (
    <tr aria-hidden="true" data-spacer={which} style={{ height, pointerEvents: "none" }}>
      <td colSpan={cols} style={{ height, padding: 0, borderBottom: 0 }} />
    </tr>
  );
}

// WindowBody is the table's body, with the two spacers round the drawn rows. The component is the same for the life of the list
// (the components object below is a module constant), so the rows are not drawn again when a list starts or stops windowing.
function WindowBody({ className, children }: { className?: string; children?: ReactNode }) {
  const w = useContext(WindowCtx);
  return (
    <tbody className={className}>
      {w.windowed && <SpacerRow which="top" height={w.top} cols={w.cols} />}
      {children}
      {w.windowed && <SpacerRow which="bottom" height={w.bottom} cols={w.cols} />}
    </tbody>
  );
}

// WindowTable is antd's table with aria-rowcount: the loaded rows and the header row.
function WindowTable(props: Record<string, unknown>) {
  const w = useContext(WindowCtx);
  return <table {...props} aria-rowcount={w.rows} />;
}

// WindowHeaderRow is the column header, row 1 of the table for a screen reader; the rows below count from 2.
function WindowHeaderRow(props: Record<string, unknown>) {
  return <tr {...props} aria-rowindex={1} />;
}

const FILES_TABLE_COMPONENTS = { table: WindowTable, header: { row: WindowHeaderRow }, body: { wrapper: WindowBody } };

// rowHeights is each pointer kind and density's row height, measured from a drawn row (decision 194). A row is one line
// (decision 183), so every row of a density is the same height. It is 0 until a row has been drawn. A finger keeps the full
// row whatever the choice, so its height has no density part.
const rowHeights: Record<string, number> = { "fine:comfortable": 0, "fine:compact": 0, coarse: 0 };

// heightKey is the row height the window reads: the pointer kind and, for a mouse, the density. A change of density reads
// another height, so the new rows are measured and the drawn rows recomputed (decision 194).
function heightKey(g: WindowInputs): string {
  return g.coarse ? "coarse" : `fine:${g.density}`;
}

// measureRow is the height of a drawn row that is not being renamed: a renamed row has its field, and its error line.
function measureRow(region: HTMLElement): number {
  for (const tr of region.querySelectorAll<HTMLElement>("tbody tr[data-id]")) {
    if (tr.querySelector("input.rename")) continue;
    const h = tr.getBoundingClientRect().height;
    if (h > 0) return h;
  }
  return 0;
}

// WindowInputs are what the window is worked out from, as of the last render: the list's rows and their IDs, the pointer kind,
// the row height choice (decision 194), the row being renamed (pinned, or -1) and where the column header is pinned.
interface WindowInputs {
  active: boolean;
  count: number;
  coarse: boolean;
  density: Density;
  pinned: number;
  stickyTop: number;
  ids: string[];
}

// WindowState is the rows drawn, and the row height they were worked out with.
interface WindowState {
  range: WindowRange;
  rowH: number;
}

// shownRange is the rows drawn: the window's, kept inside the list, with the pinned row in it.
function shownRange(r: WindowRange, count: number, pinned: number): WindowRange {
  return withPinned({ start: Math.min(r.start, count), end: Math.min(r.end, count) }, pinned, count);
}

// headHeight is the column header's height, which is the rows' offset from the top of the list.
function headHeight(region: HTMLElement): number {
  const head = region.querySelector("thead");
  return head ? head.getBoundingClientRect().height : THEAD_H;
}

// computeWindow reads the page and says which rows to draw. The list's top and the visible part of the window are read from the
// region, the header and the window; the row height is the density's, measured once.
function computeWindow(region: HTMLElement, g: WindowInputs): WindowState {
  const key = heightKey(g);
  if (!g.active) return { range: { start: 0, end: g.count }, rowH: rowHeights[key] };
  if (!rowHeights[key]) rowHeights[key] = measureRow(region);
  const rowH = rowHeights[key];
  const range = windowRange({
    count: g.count,
    rowH,
    bodyTop: region.getBoundingClientRect().top + headHeight(region),
    viewTop: g.stickyTop + THEAD_H,
    viewBottom: window.innerHeight,
  });
  return { range: shownRange(range, g.count, g.pinned), rowH };
}

// handOffFocus moves the focus to the list region when the row that holds it is about to leave the drawn rows. The region takes it
// without a scroll, so the focus is not lost to the body when the row is removed; the next arrow key brings the row back.
function handOffFocus(region: HTMLElement, range: WindowRange, ids: string[]): void {
  const a = document.activeElement;
  if (!(a instanceof HTMLElement) || !region.contains(a)) return;
  const id = a.closest<HTMLElement>("[data-id]")?.dataset.id;
  if (id === undefined) return;
  const i = ids.indexOf(id);
  if (i < range.start || i >= range.end) region.focus({ preventScroll: true });
}

// setRef assigns a ref that may be a callback or an object.
function setRef<T>(r: Ref<T> | undefined, el: T | null): void {
  if (typeof r === "function") r(el);
  else if (r) (r as { current: T | null }).current = el;
}

// PresetHandlers are a preset list's callbacks that a row's content runs, read from the page's current props at the call, so a row
// that was not drawn again still runs the page's latest action.
interface PresetHandlers {
  act: (row: { id: string }, key: string) => void;
  entry: (row: { id: string }, key: string) => void;
  // pick is a checkbox's selection of a row: it sets the focus itself, so the focus a press held is dropped.
  pick: ListSelection["pick"];
  // settle applies the focus the press on the row held, with the click of a control that stops the click at the control.
  settle: (id: string) => void;
}

interface PresetEndProps {
  row: { id: string };
  // sig is the row's actions and entries as text: it is what decides, with the row, whether the end is drawn again. The icons
  // and the entries follow their key and label; a page that changes an action's picture changes its label with it.
  sig: string;
  inline: ListAction[];
  entries: ListMenuEntry[];
  moreLabel: string;
  moreTip: string;
  PH: PresetHandlers;
  coarse: boolean;
}

// PresetEnd is a preset row's end: up to three icons inline, and the ⋮ menu with the rest of the actions and the page's entries.
// A row is drawn again only when its item or its actions change. An icon and an entry run the page's latest action at the click.
const PresetEnd = memo(
  function PresetEnd({ row, inline, entries, moreLabel, moreTip, PH, coarse }: PresetEndProps) {
    const { token } = antdTheme.useToken();
    return (
      <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "nowrap" }}>
        {inline.length > 0 && (
          <span className="row-acts" style={{ display: "inline-flex", justifyContent: "flex-end" }}>
            {inline.map((a) => (
              // An icon's click is its own: it does not reach the row. A disabled one keeps its place and the focus: it is
              // aria-disabled, drawn in the disabled colour, and its click and keys do nothing. It is not a disabled button, since a
              // browser takes the focus from a button that turns disabled (the re-check's e2e found the focus on BODY), and the
              // row's icons turn disabled under the pressed one while its request is on its way (decision 114). index.css passes its
              // pointer to the wrapper, as for a disabled one, so it has no hover background and shows the not-allowed cursor; its
              // colour is set here too, so it wins over a danger icon's, which IconButton sets inline.
              <IconButton
                key={a.key}
                label={a.label}
                icon={a.icon}
                pressed={a.pressed}
                href={a.href}
                aria-disabled={a.disabled ? true : undefined}
                danger={a.danger}
                style={a.disabled ? { color: token.colorTextDisabled } : undefined}
                onClick={(e) => {
                  e.stopPropagation();
                  PH.settle(row.id);
                  if (a.disabled) return;
                  // A link follows its own address: the browser's click does that, so run is not called for it.
                  if (a.href) return;
                  PH.act(row, a.key);
                }}
                onKeyDown={keysStop}
                onDoubleClick={(e) => e.stopPropagation()}
              />
            ))}
          </span>
        )}
        {entries.length > 0 && (
          <Dropdown menu={{ items: entries.map((e) => menuItemOf(e, () => PH.entry(row, e.key), coarse)) }} trigger={["click"]}>
            <IconButton
              aria-haspopup="menu"
              label={moreLabel}
              tip={moreTip}
              icon={MORE_ICON}
              onClick={(e) => {
                e.stopPropagation();
                PH.settle(row.id);
              }}
              onDoubleClick={(e) => e.stopPropagation()}
            />
          </Dropdown>
        )}
      </div>
    );
  },
  (a, b) => a.row === b.row && a.sig === b.sig && a.moreLabel === b.moreLabel && a.moreTip === b.moreTip && a.PH === b.PH && a.coarse === b.coarse,
);

// presetSig is a preset row's end as text: each inline action and each ⋮ entry with the parts the page changes while the row is
// there (its label, whether it is disabled, danger or pressed, its address).
function presetSig(inline: ListAction[], entries: ListMenuEntry[]): string {
  const flag = (b: boolean | undefined) => (b === undefined ? "-" : b ? "1" : "0");
  return [
    ...inline.map((a) => `i:${a.key}:${a.label}:${flag(a.disabled)}${flag(a.danger)}${flag(a.pressed)}:${a.href ?? ""}`),
    ...entries.map((e) => `e:${e.key}:${e.label}:${flag(e.disabled)}${flag(e.danger)}:${e.href ?? ""}`),
  ].join("\u0001");
}

// Listing is the one list of the web interface (the design, not in this repository; decision 114). Without columns it is the files preset, My files:
// a folder's items, a table in list view and tiles in grid view, whose keyboard handling belongs to the page; it reports
// clicks, keys and focus, and marks what is selected. With columns it is a preset list: the page's columns, rows, actions
// and menu, with the shared selection and keys. Both are drawn in the same frame: the region and its band variables, antd's
// table at the middle size with a fixed layout (the small size in compact, decision 194), the header cell, the row's icons and ⋮ button, and the shared CSS group
// (.files-list, .item-list in index.css). A preset list never takes the files-list class, which e2e waits on.
export default function Listing<T extends { id: string }>(p: ListingProps | ItemListingProps<T>) {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  // Date and role columns hide below the md breakpoint (768px), as the Svelte listing did below 767px.
  const screens = Grid.useBreakpoint();
  const narrow = !screens.md;
  const coarse = useCoarsePointer();
  // The row height choice (decision 194): compact with a mouse only. A finger keeps the full row, its tap targets as they are.
  const [density] = useDensity();
  const compact = density === "compact" && !coarse;
  const stickyTop = p.stickyTop ?? (isItems(p) ? HEADER_H : DEFAULT_STICKY_TOP);
  // A row or tile that the keyboard scrolls to keeps clear of the sticky header, the toolbar and, in the list, the column header.
  const scrollMarginTop = stickyTop + (isItems(p) || p.view === "list" ? THEAD_H : 0) + 4;
  // A preset list's selection and keys (the design, not in this repository, section 2.4). My files keeps its own in FilesPage, and these are not used for it.
  const rows = p.items as { id: string }[];
  const ids = useMemo(() => rows.map((r) => r.id), [rows]);
  const indexOf = useMemo(() => new Map(rows.map((r, i) => [r.id, i])), [rows]);
  const sel = useListSelection(ids);
  // rowMenu is the row whose ⋮ menu a right click or a finger's long press opened, and the pointer's place (tl4).
  const [rowMenu, setRowMenu] = useState<{ id: string; x: number; y: number } | null>(null);
  const itemRef = useRef<HTMLDivElement>(null);
  const focusRow = useCallback((id: string) => {
    const tr = [...(itemRef.current?.querySelectorAll<HTMLElement>("tbody tr[data-id]") ?? [])].find((r) => r.dataset.id === id);
    tr?.focus();
  }, []);
  const preset = isItems(p) ? p : null;
  const openRow = preset?.onOpen;
  const onItemKey = useListKeys({
    ids,
    focusID: sel.focusID,
    focusRow,
    onOpen: openRow
      ? (id: string) => {
          const row = preset?.items.find((r) => r.id === id);
          if (row) openRow(row);
        }
      : undefined,
    selection: preset?.selectable ? sel : undefined,
  });
  // The kind of pointer that last went down on a row. Its click is the one that follows, so a finger's tap is known
  // to be a tap and a mouse's click is known to be a click, each by its own event.
  const pointer = useRef<string>("mouse");
  // press is the finger's long press on a row, while it is down; pressed says a long press has opened the menu, so the tap
  // that ends it opens nothing.
  const press = useRef<{ x: number; y: number; timer: ReturnType<typeof setTimeout> } | null>(null);
  const pressed = useRef(false);
  const endPress = () => {
    if (press.current) clearTimeout(press.current.timer);
    press.current = null;
  };
  useEffect(() => endPress, []);
  // The page's props as of the last render. The handlers below read them at the call, so they are the same functions on every
  // render, and the rows and tiles that hold them are drawn again only when their own props change (decision 128).
  const latest = useRef(p);
  latest.current = p;
  // A press on a row focuses it, and its click selects it: one update (usePressFocus). A preset list's focus is the shared
  // selection's; My files' is the page's.
  const pf = usePressFocus((id) => {
    const cur = latest.current;
    if (isItems(cur)) {
      sel.setFocus(id);
      return;
    }
    const i = cur.items.findIndex((x) => x.id === id);
    if (i >= 0) cur.onFocusItem(i);
  });
  const H = useMemo<FileHandlers>(() => {
    const files = () => latest.current as ListingProps;
    const at = (id: string) => (files().items as Node[]).findIndex((x) => x.id === id);
    return {
      // A click that selects sets the focus itself, so the focus a press held is dropped.
      click: (id, e) => {
        pf.consume(id);
        const i = at(id);
        if (i >= 0) files().onRowClick(i, e);
      },
      dbl: (id) => files().onRowDoubleClick(id),
      renameChange: (value) => files().onRenameChange(value),
      rename: (commit) => files().onRename(commit),
      keyDown: (e) => files().onKeyDown(e),
      settle: (id) => pf.settle(id),
      // The menu's origin is passed only for a key's press, as the page took it before (four arguments otherwise).
      context: (id, x, y, origin) => {
        const i = at(id);
        if (i < 0) return;
        if (origin) files().onRowContext?.(id, i, x, y, origin);
        else files().onRowContext?.(id, i, x, y);
      },
      acts: (n) => files().rowActs?.(n) ?? [],
      act: (n, key) => files().rowActs?.(n).find((a) => a.key === key && a.enabled)?.run(),
    };
  }, [pf]);
  const PH = useMemo<PresetHandlers>(() => {
    const items = () => latest.current as unknown as ItemListingProps<{ id: string }>;
    // entriesOf is a row's ⋮ menu: the row's actions and the page's menu entries, all of them.
    const entriesOf = (row: { id: string }): ListMenuEntry[] => [
      ...(items().actions?.(row) ?? []).map((a) => ({ key: `act-${a.key}`, label: a.label, icon: a.icon, danger: a.danger, disabled: a.disabled, href: a.href, onSelect: a.run })),
      ...(items().menu?.(row) ?? []),
    ];
    return {
      act: (row, key) => {
        const a = items().actions?.(row).find((x) => x.key === key);
        if (a && !a.disabled && !a.href) a.run();
      },
      entry: (row, key) => entriesOf(row).find((x) => x.key === key)?.onSelect(),
      // A checkbox's pick sets the focus itself, so the focus a press held is dropped.
      pick: (id, how) => {
        pf.consume(id);
        sel.pick(id, how);
      },
      settle: (id) => pf.settle(id),
    };
    // sel.pick is the same function at every render.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [pf]);
  // RH are the DOM handlers of My files' rows and tiles. They name the item by the element's data-id, so one set serves every
  // row and tile, and a row or tile that holds them is not drawn again when the page renders.
  const RH = useMemo<RowHandlers>(() => {
    const files = () => latest.current as ListingProps;
    const idOf = (e: { currentTarget: HTMLElement }) => e.currentTarget.dataset.id ?? "";
    // inMark: a key from the row's mark belongs to the mark, except the arrows that move the selection. The mark's own click
    // stops at the mark, so it never reaches the row's click.
    const inCheck = (e: { target: EventTarget }) => !!(e.target as HTMLElement).closest(".row-mark");
    return {
      // The browser's own drag of an image or a link inside the row is off: an item is dragged by the page's pointer gesture.
      // A visitor's row is no drag source for the page (pb1): it has no drop attributes and no gesture, but the browser's
      // own drag is prevented for it as for every row.
      onDragStart: (e: ReactDragEvent<HTMLElement>) => e.preventDefault(),
      onPointerDown: (e: ReactPointerEvent<HTMLElement>) => {
        const id = idOf(e);
        pf.down(id, e.pointerType);
        pointer.current = e.pointerType;
        pressed.current = false;
        endPress();
        if (e.pointerType !== "touch" || files().renameID === id || files().visitor) return;
        const { clientX: x, clientY: y } = e;
        press.current = {
          x,
          y,
          timer: setTimeout(() => {
            press.current = null;
            pressed.current = true;
            liftPending.current = true;
            // A finger's long press selects the row, as a Ctrl click does; the menu is the ⋮ button's and the right click's.
            H.click(id, CTRL_CLICK);
          }, LONG_PRESS_MS),
        };
      },
      onPointerMove: (e: ReactPointerEvent<HTMLElement>) => {
        const cur = press.current;
        if (cur && Math.hypot(e.clientX - cur.x, e.clientY - cur.y) > PRESS_SLOP_PX) endPress();
      },
      onPointerUp: endPress,
      onPointerCancel: endPress,
      // The row under a mouse is known to the icons that draw at its end; a touch does not hover.
      onPointerEnter: (e: ReactPointerEvent<HTMLElement>) => {
        if (e.pointerType === "mouse") setRowHover(idOf(e));
      },
      onPointerLeave: (e: ReactPointerEvent<HTMLElement>) => {
        if (e.pointerType === "mouse" && getRowHover() === idOf(e)) setRowHover(null);
      },
      onContextMenu: (e: ReactMouseEvent<HTMLElement>) => {
        // The browser's own menu stays in a field the person types in, and on a row being renamed.
        if (files().renameID === idOf(e) || isTextField(e.target)) return;
        e.preventDefault();
        endPress();
        H.context(idOf(e), e.clientX, e.clientY);
      },
      onClick: (e: ReactMouseEvent<HTMLElement>) => {
        // The focus of a press is applied here, in the batch of the click's own update, so the two are one render.
        pf.settle(idOf(e));
        // A click in the row's own rename field places the cursor there; the item is not opened by it.
        if (files().renameID === idOf(e)) return;
        // The tap that ends a long press is part of the press: it selects and opens nothing.
        if (pressed.current) {
          pressed.current = false;
          return;
        }
        // A finger's tap toggles the row while anything is selected, as a Ctrl click does, and opens nothing; with nothing
        // selected it opens the item (the page decides). A visitor's tap always opens (pb1).
        if (pointer.current === "touch" && !files().visitor && files().selected.size > 0) {
          H.click(idOf(e), CTRL_CLICK);
          return;
        }
        H.click(idOf(e), { shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, metaKey: e.metaKey, touch: pointer.current === "touch" });
      },
      onDoubleClick: (e: ReactMouseEvent<HTMLElement>) => {
        // A finger's double tap has opened the item already, on its first tap. A double click in the rename field selects
        // a word in it, and does not open the item.
        if (pointer.current === "touch" || files().renameID === idOf(e)) return;
        // The item is the one the event reached: its ID is read from the row, not from its place in the list.
        const id = idOf(e);
        if (id) H.dbl(id);
      },
      // A key from the row's checkbox belongs to the checkbox, except the arrows that move the selection.
      onKeyDown: (e: ReactKeyboardEvent<HTMLElement>) => {
        if (inCheck(e) && !e.key.startsWith("Arrow")) return;
        H.keyDown(e);
      },
      // A focus that comes from a press is the click's (settle); any other focus is the row's own.
      onFocus: (e: ReactFocusEvent<HTMLElement>) => pf.focus(idOf(e)),
    };
    // pointer, press, pressed, liftPending and endPress are refs and a function over them: the same at every render. The page's
    // props are read at the call through latest, so the handlers do not depend on the selection or the pointer kind.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [H, pf]);
  // liftPending says a long press has opened its menu and no new press has begun since. Until one does, the mousedowns the
  // browser sends for the lifted finger are stopped, since the menu closes on a mousedown and would close at the lift. A
  // real mouse press always begins with a pointerdown, which clears the flag first. The listeners are added at mount, so
  // they run before the menu's own listener, which is added when the menu opens.
  const liftPending = useRef(false);
  useEffect(() => {
    const newPress = () => {
      liftPending.current = false;
    };
    const stopLiftMouseDown = (e: MouseEvent) => {
      if (liftPending.current) e.stopImmediatePropagation();
    };
    window.addEventListener("pointerdown", newPress, true);
    window.addEventListener("mousedown", stopLiftMouseDown, true);
    return () => {
      window.removeEventListener("pointerdown", newPress, true);
      window.removeEventListener("mousedown", stopLiftMouseDown, true);
    };
  }, []);

  // ---- The window (decision 183): My files' list draws the rows near the screen once more than 150 rows are loaded. ----
  const filesList = !isItems(p) && p.view === "list";
  const extRef = isItems(p) ? undefined : p.containerRef;
  const pinned = !isItems(p) && p.renameID ? rows.findIndex((r) => r.id === p.renameID) : -1;
  const winActive = filesList && rows.length > WINDOW_MIN_ROWS;
  const regionRef = useRef<HTMLDivElement | null>(null);
  const setRegion = useCallback(
    (el: HTMLDivElement | null) => {
      regionRef.current = el;
      setRef(extRef, el);
    },
    [extRef],
  );
  // The inputs and the state are read by the page's listeners, which are set up once per windowing, so they are kept in refs.
  const winInputs = useRef<WindowInputs>({ active: false, count: 0, coarse, density, pinned: -1, stickyTop, ids: [] });
  winInputs.current = { active: winActive, count: rows.length, coarse, density, pinned, stickyTop, ids };
  const [win, setWin] = useState<WindowState>({ range: { start: 0, end: 0 }, rowH: 0 });
  const winRef = useRef(win);
  winRef.current = win;
  // Every commit, the window is worked out again from the page, and state changes only when its rows do: a row's height is
  // measured on the first commit that draws one, so the window settles in a commit or two, before the browser paints.
  useLayoutEffect(() => {
    const el = regionRef.current;
    if (!el || !winInputs.current.active) return;
    const next = computeWindow(el, winInputs.current);
    const cur = winRef.current;
    if (!sameRange(cur.range, next.range) || cur.rowH !== next.rowH) setWin(next);
  });
  // A scroll or a resize moves the window, at most once a frame. A passive listener, on the window and the capture phase, so a
  // scroll of any element reaches it.
  useEffect(() => {
    if (!winActive) return;
    let frame = 0;
    const follow = () => {
      frame = 0;
      const el = regionRef.current;
      if (!el) return;
      const g = winInputs.current;
      const next = computeWindow(el, g);
      const cur = winRef.current;
      if (sameRange(cur.range, next.range) && cur.rowH === next.rowH) return;
      handOffFocus(el, next.range, g.ids);
      setWin(next);
    };
    const onMove = () => {
      if (!frame) frame = requestAnimationFrame(follow);
    };
    window.addEventListener("scroll", onMove, { capture: true, passive: true });
    window.addEventListener("resize", onMove, { passive: true });
    return () => {
      window.removeEventListener("scroll", onMove, true);
      window.removeEventListener("resize", onMove);
      if (frame) cancelAnimationFrame(frame);
    };
  }, [winActive]);
  // A focus request (a key moved the focus) names a row. A row that is drawn takes the focus at once; one that is not is scrolled
  // to, its window is worked out from the new place, and the next commit draws it and focuses it. pendingFocus is the row still owed.
  const focusReq = isItems(p) ? undefined : p.focusRequest;
  const pendingFocus = useRef<string | null>(null);
  useLayoutEffect(() => {
    if (focusReq) pendingFocus.current = focusReq.id;
  }, [focusReq]);
  useLayoutEffect(() => {
    const id = pendingFocus.current;
    const el = regionRef.current;
    if (id === null || !el) return;
    const row = [...el.querySelectorAll<HTMLElement>("[data-id]")].find((r) => r.dataset.id === id);
    if (row) {
      pendingFocus.current = null;
      row.focus();
      return;
    }
    const index = ids.indexOf(id);
    const g = winInputs.current;
    const rowH = rowHeights[heightKey(g)];
    if (!g.active || index < 0) {
      pendingFocus.current = null;
      return;
    }
    if (!(rowH > 0)) return;
    const top = el.getBoundingClientRect().top + headHeight(el) + index * rowH;
    const bottom = top + rowH;
    // The row's place under the pinned header, as a keyboard scroll to a row keeps it (scrollMarginTop).
    const viewTop = g.stickyTop + THEAD_H + 4;
    const viewBottom = window.innerHeight;
    const dy = top < viewTop ? top - viewTop : bottom > viewBottom ? bottom - viewBottom : 0;
    if (dy !== 0) window.scrollTo({ top: window.scrollY + dy });
    const next = computeWindow(el, g);
    if (!sameRange(winRef.current.range, next.range)) setWin(next);
  });

  // ---- The frame both presets draw in. ----

  // The bands read their colours from these variables on the list: the theme's tokens are passed in here, as the app does not
  // define antd's CSS variables on the page, so a rule that reads them would paint nothing.
  // The list's variables: its bands, its header's background, and the place the header is pinned at (under the toolbar).
  const bandVars = {
    "--sc-row-hover": token.colorFillSecondary,
    "--sc-row-selected": token.colorPrimaryBg,
    "--sc-accent": token.colorPrimary,
    "--sc-list-head": token.colorBgContainer,
    "--sc-list-head-tint": token.colorFillAlter,
    "--sc-list-top": `${stickyTop}px`,
  } as CSSProperties;

  // A column's header. A sortable one is a button with the direction's arrow after its label; the arrow is decorative
  // (aria-hidden), so the button's name stays the label alone, and its box is always there, so the label does not move when
  // the sort changes. dir is the list's direction when it is sorted by this column, null when it is not.
  const headCell = (label: string, dir: "asc" | "desc" | null, onSort: () => void) => ({
    title: (
      <Button type="text" size="small" onClick={onSort} style={{ fontWeight: 500, padding: 0, display: "inline-flex", alignItems: "center", gap: 4, minHeight: coarse ? 44 : undefined }}>
        {label}
        <span aria-hidden="true" style={{ display: "inline-flex", width: 14, height: 14, flex: "none" }}>
          {dir === null ? null : dir === "asc" ? <ArrowUp size={14} /> : <ArrowDown size={14} />}
        </span>
      </Button>
    ),
    onHeaderCell: () => ({ "aria-sort": ariaSortOf(dir) }),
  });

  // The actions column's header: a name for a screen reader, shown to no one.
  const actionsHead = <span className="sr-only">{t("storage.actions")}</span>;

  // A row's icons and More button, and a tile's menu button, are IconButtons (above): 40 pixels with a mouse and 44 on a finger
  // (index.css). A button is a tab stop only in the focused row or tile.

  // ---- The preset list. ----

  const renderItems = (q: ItemListingProps<T>) => {
    const [nameCol, ...others] = q.columns;
    // Below md the secondary columns are the name's second line; a phone or a finger has the ⋮ menu alone (the design, not in this repository, section 4).
    // A page that asks for the phone layout (a narrow panel) has it at any width.
    const phoneHere = narrow || q.phone === true;
    const seconds = phoneHere ? others.filter((c) => c.secondary) : [];
    const shown = phoneHere ? others.filter((c) => !c.secondary) : others;
    const menuOnly = phoneHere || coarse;
    const selectable = q.selectable === true;
    const picked = selectable ? sel.selected : NO_IDS;
    const anyPicked = picked.size > 0;
    const allPicked = q.items.length > 0 && q.items.every((r) => picked.has(r.id));
    // Each row's end: up to three icons inline, and the rest with the page's menu in the ⋮ menu.
    const ends = new Map(
      q.items.map((r) => {
        const acts = q.actions?.(r) ?? [];
        const inline = menuOnly ? [] : acts.slice(0, 3);
        const rest = menuOnly ? acts : acts.slice(3);
        const entries: ListMenuEntry[] = [
          ...rest.map((a) => ({ key: `act-${a.key}`, label: a.label, icon: a.icon, danger: a.danger, disabled: a.disabled, href: a.href, onSelect: a.run })),
          ...(q.menu?.(r) ?? []),
        ];
        return [r.id, { inline, entries, sig: presetSig(inline, entries) }] as const;
      }),
    );
    const slots = Math.max(0, ...[...ends.values()].map((e) => e.inline.length));
    const anyMenu = [...ends.values()].some((e) => e.entries.length > 0);
    // The end column keeps room for the most icons any row has and the ⋮, so the rows' ends line up.
    const endWidth = (slots + (anyMenu ? 1 : 0)) * ROW_ICON_PX + 16;
    const head = (c: ListColumn<T>) => {
      const sort = q.sort;
      if (c.sortKey && sort) {
        const key = c.sortKey;
        return headCell(c.title, sort.key === key ? sort.dir : null, () => sort.onSort(key));
      }
      return { title: <span style={{ fontWeight: 500 }}>{c.title}</span>, onHeaderCell: () => (c.ariaSort ? { "aria-sort": c.ariaSort } : {}) };
    };

    // A mouse's checkbox takes the icon's place, as in My files: on a hovered or selected row, and on every row while
    // anything is selected (the shared CSS decides). A finger keeps the checkbox column instead.
    const mark = (r: T) => <PresetMark id={r.id} checked={picked.has(r.id)} name={q.rowLabel(r)} renderIcon={q.icon ? () => q.icon!(r) : undefined} pick={PH.pick} />;
    const nameCell = (r: T) => {
      // The phone's second line is one wrapping row of inline items (mob4): each non-empty cell is a span, and the dot between
      // two items is the stylesheet's (.row-second-item + .row-second-item::before), so no text node holds a separator and an
      // empty cell draws nothing.
      const cells = seconds.map((c) => ({ key: c.key, node: c.render(r) })).filter((c) => !emptySecondCell(c.node));
      return (
        <div style={{ minWidth: 0 }}>
          <span style={{ display: "inline-flex", alignItems: "center", gap: 8, minWidth: 0, maxWidth: "100%", verticalAlign: "middle" }}>
            {selectable && !coarse ? (
              mark(r)
            ) : selectable ? (
              // A finger's row keeps its icon plain; a long press selects it, and the badge then shows on the icon.
              <span style={{ position: "relative", display: "inline-flex", flex: "none", minWidth: 22, height: 22 }}>
                {q.icon && <span className="row-icon">{q.icon(r)}</span>}
                {picked.has(r.id) && <CheckBadge size={17} ring={token.colorPrimaryBg} pos={{ insetBlockEnd: -3, insetInlineEnd: -3 }} />}
              </span>
            ) : (
              q.icon?.(r)
            )}
            <span style={{ minWidth: 0, overflowWrap: "anywhere" }}>{nameCol.render(r)}</span>
          </span>
          {cells.length > 0 && (
            <div className="row-second" style={{ color: token.colorTextSecondary, fontSize: token.fontSizeSM, overflowWrap: "anywhere" }}>
              {cells.map((c) => (
                <span key={c.key} className="row-second-item">{c.node}</span>
              ))}
            </div>
          )}
        </div>
      );
    };
    const rowEnd = (r: T) => {
      const end = ends.get(r.id);
      if (!end) return null;
      return (
        <PresetEnd
          row={r}
          sig={end.sig}
          inline={end.inline}
          entries={end.entries}
          moreLabel={q.moreLabel?.(r) ?? t("files.tile.menu", { name: q.rowLabel(r) })}
          moreTip={t("files.selection.more")}
          PH={PH}
          coarse={coarse}
        />
      );
    };

    const nameHead = head(nameCol);
    const columns: ColumnsType<T> = [
      {
        key: nameCol.key,
        ...nameHead,
        title: selectable ? (
          <span style={{ display: "inline-flex", alignItems: "center", gap: 8, verticalAlign: "middle" }}>
            <HeadMark state={allPicked ? "all" : anyPicked ? "some" : "none"} onSelectAll={(checked) => (checked ? sel.selectAll() : sel.clear())} />
            {nameHead.title}
          </span>
        ) : (
          nameHead.title
        ),
        render: (_v, r) => nameCell(r),
      },
      ...shown.map<ColumnsType<T>[number]>((c) => ({
        key: c.key,
        ...head(c),
        align: c.align,
        width: c.width,
        render: (_v: unknown, r: T) => c.render(r),
      })),
      ...(slots > 0 || anyMenu
        ? [{ key: "actions", title: actionsHead, width: endWidth, align: "end" as const, render: (_v: unknown, r: T) => rowEnd(r) }]
        : []),
    ];

    // A row is a roving tab stop, as in My files: the focused row takes Tab, and the arrows move between rows. A click on a
    // control in the row is the control's; a click elsewhere selects (a list with a selection) or opens the row. React
    // passes up clicks in a portal the row opened (its ⋮ menu), which are not the row's.
    const isControl = (e: ReactMouseEvent<HTMLElement>) =>
      !e.currentTarget.contains(e.target as globalThis.Node) ||
      !!(e.target as HTMLElement).closest("a, button, input, label, .row-mark");
    // A row's ⋮ menu holds the row's actions and the page's menu entries, all of them: a right click and a finger's long press open
    // it at the pointer (the same menu as the ⋮ button's, tl4).
    const entriesOf = (r: T): ListMenuEntry[] => [
      ...(q.actions?.(r) ?? []).map((a) => ({ key: `act-${a.key}`, label: a.label, icon: a.icon, danger: a.danger, disabled: a.disabled, href: a.href, onSelect: a.run })),
      ...(q.menu?.(r) ?? []),
    ];
    const rowProps = (r: T) => ({
      role: "row",
      "aria-label": q.rowLabel(r),
      className: q.rowClassName?.(r),
      ...(selectable ? { "aria-selected": picked.has(r.id) } : {}),
      tabIndex: sel.focusID === r.id ? 0 : -1,
      "data-id": r.id,
      "data-testid": q.rowTestID?.(r),
      // The focus that comes from a press is the click's (settle), so a press that selects is one update; any other focus is the
      // row's own.
      onFocus: () => pf.focus(r.id),
      onKeyDown: onItemKey,
      onClick: (e: ReactMouseEvent<HTMLElement>) => {
        pf.settle(r.id);
        if (isControl(e)) return;
        // The tap that ends a long press is part of the press: it opens and selects nothing.
        if (pressed.current) {
          pressed.current = false;
          return;
        }
        // A finger's tap toggles the row while anything is selected, and opens nothing; with nothing selected it opens the row
        // (or selects it where the list has no open).
        if (selectable && pointer.current === "touch" && picked.size > 0) {
          PH.pick(r.id, { toggle: true });
          return;
        }
        if (selectable && (e.ctrlKey || e.metaKey || e.shiftKey)) sel.pick(r.id, { toggle: e.ctrlKey || e.metaKey, range: e.shiftKey });
        else if (q.onOpen) q.onOpen(r);
        else if (selectable) sel.pick(r.id);
      },
      onPointerDown: (e: ReactPointerEvent<HTMLElement>) => {
        pf.down(r.id, e.pointerType);
        pointer.current = e.pointerType;
        endPress();
        pressed.current = false;
        if (e.pointerType !== "touch") return;
        const hasMenu = entriesOf(r).length > 0;
        if (!selectable && !hasMenu) return;
        const { clientX: x, clientY: y } = e;
        press.current = {
          x,
          y,
          timer: setTimeout(() => {
            press.current = null;
            pressed.current = true;
            liftPending.current = true;
            // A finger's long press selects the row where the list has a selection; elsewhere it opens the row's menu. The menu on a
            // selectable row opens from its ⋮ button on a finger (a mouse's right click still opens it).
            if (selectable) PH.pick(r.id, { toggle: true });
            else setRowMenu({ id: r.id, x, y });
          }, LONG_PRESS_MS),
        };
      },
      onPointerMove: (e: ReactPointerEvent<HTMLElement>) => {
        const cur = press.current;
        if (cur && Math.hypot(e.clientX - cur.x, e.clientY - cur.y) > PRESS_SLOP_PX) endPress();
      },
      onPointerUp: endPress,
      onPointerCancel: endPress,
      onContextMenu: (e: ReactMouseEvent<HTMLElement>) => {
        if (entriesOf(r).length === 0) return;
        e.preventDefault();
        endPress();
        setRowMenu({ id: r.id, x: e.clientX, y: e.clientY });
      },
      style: selectable ? { userSelect: "none" as const, scrollMarginTop } : { scrollMarginTop },
    });
    // The menu the right click or long press opened: it is drawn at the pointer, by an empty anchor there.
    const menuRow = rowMenu ? q.items.find((r) => r.id === rowMenu.id) : undefined;
    const menuList = menuRow ? entriesOf(menuRow) : [];

    return (
      <div role="region" aria-label={q.label} ref={itemRef} className={`item-list${compact ? " list--compact" : ""}`} data-has-selection={anyPicked ? "true" : "false"} style={bandVars}>
        {selectable && anyPicked && (
          <SelectionBar
            label={t("files.selection.label")}
            countText={translatePlural(lang, "files.selected", picked.size)}
            clearLabel={t("files.act.clear")}
            onClear={sel.clear}
            actions={q.bulk?.([...picked]) ?? []}
            coarse={coarse}
          />
        )}
        <Table<T>
          rowKey="id"
          size={compact ? "small" : "middle"}
          pagination={false}
          // The table is as wide as the page: a column that does not fit wraps, and the page never scrolls sideways.
          tableLayout="fixed"
          columns={columns}
          dataSource={q.items}
          locale={{
            emptyText: (
              <EmptyState role="status" size="table" className={q.empty.className} text={q.empty.title} description={q.empty.description}>
                {q.empty.action && (
                  // The mockup's action: 36 high, padded 16 at each side with no border (antd's is 15 inside a 1-pixel border).
                  <Button
                    type="primary"
                    icon={<Plus size={16} aria-hidden="true" focusable="false" />}
                    style={{ height: 36, paddingInline: token.padding, borderWidth: 0 }}
                    onClick={q.empty.action.onClick}
                  >
                    {q.empty.action.label}
                  </Button>
                )}
              </EmptyState>
            ),
          }}
          onRow={(r) => rowProps(r) as Record<string, unknown>}
        />
        <Dropdown
          open={menuList.length > 0}
          onOpenChange={(open) => {
            if (!open) setRowMenu(null);
          }}
          trigger={[]}
          menu={{ items: menuList.map((e) => menuItemOf(e, e.onSelect, coarse)), onClick: () => setRowMenu(null) }}
        >
          {/* The anchor sits at the pointer, which is a place in the window (clientX from the left), so its inset is physical. */}
          <span aria-hidden="true" style={{ position: "fixed", inset: `${rowMenu?.y ?? 0}px auto auto ${rowMenu?.x ?? 0}px`, width: 0, height: 0 }} />
        </Dropdown>
      </div>
    );
  };

  if (isItems(p)) return renderItems(p);

  // ---- The files preset (My files), as it was. A visitor of a link (pb1) gets the same table and tiles without the
  // owner's selection, marks, thumbnails and drag; the page's row icons are drawn on every row at once. ----

  const visitor = p.visitor === true;

  // contentOf is the props of a row's or a tile's content (FileCellProps). The rename state is passed only to the row being renamed,
  // so the other rows' content is not drawn again when a rename starts or changes.
  const contentOf = (n: Node): FileCellProps => {
    const renaming = p.renameID === n.id;
    return {
      n,
      selected: p.selected.has(n.id),
      coarse,
      renaming,
      renameValue: renaming ? p.renameValue : "",
      renameError: renaming ? p.renameError : null,
      visitor,
      H,
    };
  };
  // The select-all mark is always in the name's header, on every pointer; a visitor has none (pb1). It reads every loaded row,
  // not the drawn slice (decision 183).
  const someSelected = p.items.some((n) => p.selected.has(n.id));
  const allSelected = p.items.length > 0 && p.items.every((n) => p.selected.has(n.id));
  const selectAllBox = <HeadMark state={allSelected ? "all" : someSelected ? "some" : "none"} onSelectAll={p.onSelectAll} />;

  // The active column shows its direction as an arrow after the label (headCell, the frame's header).
  const sortHead = (key: SortKey, label: string) => headCell(label, p.sortKey === key ? p.sortDir : null, () => p.onSort(key));

  // The row end: the four icons of the page's row actions (drawn with a mouse, where the row is hovered or selected) and the
  // More button, which every row has. The icons keep their room, so a row does not move when they appear. The signature is of the
  // actions the row draws now: only a selected or focused row draws them without a hover. A visitor's row draws its icons always,
  // so its signature is always of them (pb1).
  const rowEnd = (n: Node) => {
    const focused = p.focusID === n.id;
    const selected = p.selected.has(n.id);
    const sig = p.rowActs && (visitor || focused || selected) ? actsSig(H.acts(n)) : "";
    return <FileRowEnd n={n} selected={selected} focused={focused} coarse={coarse} visitor={visitor} sig={sig} H={H} />;
  };
  const nameHead = sortHead("name", t("files.col.name"));

  if (p.view === "list") {
    // A visitor's actions column keeps room for the most icons any row has (at least one), not the owner's four and the ⋮ (pb1).
    const visitorSlots = visitor ? Math.max(1, ...p.items.map((n) => H.acts(n).filter((a) => a.enabled).length)) : 0;
    const columns: ColumnsType<Node> = [
      {
        key: "name",
        ...nameHead,
        // The select-all mark sits before the label on every pointer. A visitor has none (pb1).
        title: visitor ? (
          nameHead.title
        ) : (
          <span style={{ display: "inline-flex", alignItems: "center", gap: 8, verticalAlign: "middle" }}>
            {selectAllBox}
            {nameHead.title}
          </span>
        ),
        render: (_v, n) => <FileNameCell {...contentOf(n)} />,
      },
      {
        key: "size",
        ...sortHead("size", t("files.col.size")),
        align: "end",
        width: 96,
        render: (_v, n) => <span style={{ fontVariantNumeric: "tabular-nums", fontSize: token.fontSizeSM }}>{sizeOf(n, lang)}</span>,
      },
      ...(narrow
        ? []
        : [
            {
              key: "modified",
              ...sortHead("modified", t("files.col.modified")),
              width: 170,
              render: (_v: unknown, n: Node) => <span style={{ fontSize: token.fontSizeSM }}>{formatDate(n.modified_at, lang)}</span>,
            },
            // The role column describes the owner's access; a visitor of a link has none to show (pb1).
            ...(visitor
              ? []
              : [
                  {
                    key: "role",
                    // Not sortable, so not a button; the same weight as the sortable headers beside it.
                    title: <span style={{ fontWeight: 500 }}>{t("files.col.role")}</span>,
                    width: 120,
                    render: (_v: unknown, n: Node) => (
                      <span style={{ color: token.colorTextSecondary, fontSize: token.fontSizeSM }}>{n.role ? t(`role.${n.role}`) : ""}</span>
                    ),
                  },
                ]),
          ]),
      {
        key: "actions",
        title: actionsHead,
        width: visitor ? ROW_ICON_PX * visitorSlots : coarse ? ROW_ICON_PX : ROW_ICON_PX * (ROW_ICONS + 1),
        align: "end",
        render: (_v, n) => rowEnd(n),
      },
    ];
    // The rows drawn: every row, or the window's slice with the row being renamed in it (decision 183). The spacers hold the place
    // of the rows that are not drawn, and aria-rowcount counts every loaded row.
    const shown = winActive ? shownRange(win.range, p.items.length, pinned) : { start: 0, end: p.items.length };
    const drawn = shown.start === 0 && shown.end === p.items.length ? p.items : p.items.slice(shown.start, shown.end);
    const gap = spacers(shown, p.items.length, win.rowH);
    return (
      <div
        role="region"
        aria-label={p.label}
        ref={setRegion}
        className={`files-list${compact ? " list--compact" : ""}${visitor ? " files-list--visitor" : ""}`}
        data-has-selection={someSelected ? "true" : "false"}
        style={bandVars}
        // A windowed list takes the focus when a row scrolls out of the window, so the arrow keys still reach it (decision 183).
        tabIndex={winActive ? -1 : undefined}
        onKeyDown={(e) => {
          if (e.target === e.currentTarget) H.keyDown(e);
        }}
      >
        <WindowCtx.Provider value={{ windowed: winActive, top: gap.top, bottom: gap.bottom, rows: p.items.length + 1, cols: columns.length }}>
        <Table<Node>
          rowKey="id"
          size={compact ? "small" : "middle"}
          pagination={false}
          // The column header is in this table, not in a table of its own (antd's sticky holder), so it shares the rows' columns in
          // every frame. The stylesheet pins its cells under the toolbar, at --sc-list-top (decision 77, item fu3b).
          tableLayout="fixed"
          columns={columns}
          dataSource={drawn}
          components={FILES_TABLE_COMPONENTS}
          onRow={(n) => {
            // The selected band and the hovered band are painted on the cells (index.css), as antd paints a row's cells.
            const style = {
              ...(p.cutIDs?.has(n.id) ? { opacity: CUT_OPACITY } : {}),
              // A row cannot take a text selection, as a tile cannot, so Shift+click does not leave text for Ctrl+C to copy.
              userSelect: "none" as const,
              scrollMarginTop,
            };
            // The row's place is its index in the whole list, not in the drawn slice (decision 183); aria-rowindex counts the header as 1.
            const i = indexOf.get(n.id) ?? 0;
            return { ...rowDomAttrs(n, p.selected.has(n.id), p.focusID === n.id, RH, visitor), "aria-rowindex": i + 2, style } as Record<string, unknown>;
          }}
        />
        </WindowCtx.Provider>
      </div>
    );
  }

  // The tile view has two bands, the folders first and then the files, each a grid of its own with the same column widths, so
  // a folder row and a file row line up. The flat order (the page's ids) is the items' order; the folders come first in it.
  const folders = p.items.filter((n) => n.kind === "dir");
  const files = p.items.filter((n) => n.kind !== "dir");
  const band = { display: "grid", gridTemplateColumns: `repeat(auto-fill, minmax(min(100%, ${TILE_MIN_PX}px), 1fr))`, gap: 12 } as const;
  // A folder's chip or a file's card: a memoised tile, drawn again only when its own state changes.
  const tile = (n: Node, kind: "chip" | "card") => (
    <FileTile key={n.id} kind={kind} focused={p.focusID === n.id} cut={p.cutIDs?.has(n.id) ?? false} scrollMarginTop={scrollMarginTop} RH={RH} {...contentOf(n)} />
  );
  const chip = (n: Node) => tile(n, "chip");
  const card = (n: Node) => tile(n, "card");
  return (
    // The padding is the gutter a rubber band starts in, as the grid had before the bands.
    <div role="listbox" aria-label={p.label} aria-multiselectable={visitor ? "false" : "true"} tabIndex={-1} ref={setRegion} style={{ display: "flex", flexDirection: "column", gap: 16, padding: 12, outline: "none" }}>
      {folders.length > 0 && <div role="presentation" style={band}>{folders.map(chip)}</div>}
      {files.length > 0 && <div role="presentation" style={band}>{files.map(card)}</div>}
    </div>
  );
}

// thumbable is an image the server can thumbnail: the server sends has_thumbnail on those, and on no other item.
function thumbable(n: Node): boolean {
  return n.kind === "file" && n.has_thumbnail !== undefined;
}

// sizeOf is a file's size. A folder and a shortcut have none to show.
function sizeOf(n: Node, lang: string): string {
  return n.kind === "file" ? formatBytes(n.size, lang) : "";
}

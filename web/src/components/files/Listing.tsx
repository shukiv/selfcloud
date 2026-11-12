// SPDX-License-Identifier: AGPL-3.0-or-later

import {
  Fragment,
  memo,
  useCallback,
  useEffect,
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
import { Button, Checkbox, Dropdown, Grid, Input, Table, theme as antdTheme, type MenuProps } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ArrowDown, ArrowUp, EllipsisVertical, Link2, Plus, Star } from "lucide-react";
import EmptyState from "../EmptyState";
import IconButton from "../IconButton";
import { translatePlural, useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import type { SortDir, SortKey } from "../../lib/files/sort";
import type { Node } from "../../lib/files/types";
import type { ViewMode } from "../../lib/files/viewPrefs";
import { isTextField } from "../../lib/files/keys";
import { useCoarsePointer } from "../../lib/pointer";
import { HEADER_H, THEAD_H } from "../../lib/files/layout";
import { useListKeys, useListSelection, type ListSelection } from "../../lib/lists";
import { getRowHover, setRowHover, subscribeRowHover } from "./rowHover";
import Icon from "./Icon";
import KindMarks from "./KindMarks";
import SelectionBar, { type SelectionAction } from "./SelectionBar";
import Thumb from "./Thumb";

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
  // rowActs gives the icons at a list row's end for one item. They are drawn with a mouse, where the row is hovered or selected.
  rowActs?: (n: Node) => RowAct[];
  // onRowContext asks for the item's menu: a right-click, or a finger held on the item for a moment. It gets the pointer's
  // place in the window, so the menu opens there.
  // keyboardOrigin is the ⋮ button when a key pressed it: the menu then takes the focus, and gives it back to the button.
  onRowContext?: (id: string, index: number, x: number, y: number, keyboardOrigin?: HTMLElement) => void;
  // cutIDs are the items a cut has taken: they are shown dimmed until they are pasted or the cut is cancelled.
  cutIDs?: Set<string>;
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
  // href makes the action a real link (round 2 of tl3): inline it is an anchor the browser follows, so a middle click opens a
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

type AriaSort = "ascending" | "descending" | "none";

// ariaSortOf is a header's aria-sort: the list's direction when it is sorted by the header's column, none when it is not.
function ariaSortOf(dir: "asc" | "desc" | null): AriaSort {
  if (dir === null) return "none";
  return dir === "asc" ? "ascending" : "descending";
}

// RowActs draws a list row's icons only while the row is hovered by a mouse, or selected, or has the focus (review fix 7): the icons
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
// tab, and a plain click or a key runs the entry here, in this tab (review L2).
function menuItemOf(e: ListMenuEntry, select: () => void): NonNullable<MenuProps["items"]>[number] {
  return {
    key: e.key,
    icon: e.icon,
    danger: e.danger,
    disabled: e.disabled,
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
  H: FileHandlers;
}

const CTRL_CLICK: Click = { shiftKey: false, ctrlKey: true, metaKey: false };

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
      style={{ flex: "1 1 160px", minWidth: 0, minHeight: 44 }}
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
        // The menu opens at the button's start edge: its left in English, its right in Hebrew (review fix 11). A key's click has
        // no pointer, so its detail is 0: the menu then takes the focus (review fix 1).
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

// FileNameCell is the name cell of a list row: the row's icon (or a mouse's checkbox in its place), the name, its marks.
const FileNameCell = memo(function FileNameCell({ n, selected, coarse, renaming, renameValue, renameError, H }: FileCellProps) {
  const { t } = useT();
  return (
    // verticalAlign middle: the line box does not take the baseline of the icon or of the checkbox, which swap on hover
    // (decision 85, fu5). Without it the name moved 0.75 px up on a hovered row.
    <span style={{ display: "inline-flex", alignItems: "center", gap: 8, flexWrap: "wrap", minWidth: 0, maxWidth: "100%", verticalAlign: "middle" }}>
      {coarse ? (
        <Icon kind={kindOf(n)} size={22} colour={n.color} node={n} />
      ) : (
        <span style={{ display: "inline-flex", alignItems: "center", justifyContent: "center", flex: "none", width: 22, height: 22 }}>
          <span className="row-icon">
            <Icon kind={kindOf(n)} size={22} colour={n.color} node={n} />
          </span>
          {/* A mouse's checkbox takes the file icon's place when the row is hovered, or selected, or when anything is selected (the CSS
              in index.css decides); it is not a tab stop, and a click on it selects by the box alone. */}
          <span className="row-check" onClick={(e) => e.stopPropagation()} style={{ flex: "none" }}>
            <Checkbox checked={selected} tabIndex={-1} aria-label={t("files.select.item", { name: n.name })} onChange={() => H.click(n.id, CTRL_CLICK)} />
          </span>
        </span>
      )}
      {renaming ? <FileRename value={renameValue} error={renameError} H={H} /> : <bdi style={{ overflowWrap: "anywhere", minWidth: 0 }}>{n.name}</bdi>}
      <LinkBadge node={n} />
      <KindMarks node={n} />
      <StarMark node={n} />
      <RenameError error={renaming ? renameError : null} />
    </span>
  );
});

// FileRowEnd is a list row's end: the page's row icons (drawn with a mouse, where the row is hovered, selected or focused) and the
// ⋮ button. sig is the row's actions as text, given for a selected or focused row: it is not read, but a change in it draws the
// row's end again, so an icon follows the page's actions. A row that is only hovered reads the actions when it draws its icons.
const FileRowEnd = memo(function FileRowEnd({ n, selected, focused, coarse, H }: { n: Node; selected: boolean; focused: boolean; coarse: boolean; sig: string; H: FileHandlers }) {
  const tab = focused ? 0 : -1;
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "nowrap" }}>
      {!coarse && (
        <span className="row-acts" style={{ display: "inline-flex", justifyContent: "flex-end", minWidth: ROW_ICON_PX * ROW_ICONS }}>
          <RowActs id={n.id} show={selected || focused}>
            {() =>
              // A files action that is not enabled is one the item's menu does not offer, so it is not drawn (unlike a
              // preset list's disabled action, which is one on its way).
              H.acts(n)
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
                ))
            }
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

// TypeIcon is a file's type icon, which keeps its size when the name is long.
function TypeIcon({ n }: { n: Node }) {
  return (
    <span style={{ display: "inline-flex", flex: "none" }}>
      <Icon kind={kindOf(n)} size={20} colour={n.color} node={n} />
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

// TileCheck is a finger's checkbox on a tile: a tile has no click that selects it, so its checkbox does (a list row has one already).
// The wrapper stops the click: the checkbox toggles the item, and the item does not also open. A mouse has none.
function TileCheck({ n, selected, H }: { n: Node; selected: boolean; H: FileHandlers }) {
  const { t } = useT();
  return (
    <span onClick={(e) => e.stopPropagation()} style={{ display: "inline-flex", flex: "none" }}>
      <Checkbox checked={selected} tabIndex={-1} aria-label={t("files.select.item", { name: n.name })} onChange={() => H.click(n.id, CTRL_CLICK)} />
    </span>
  );
}

// rowDomAttrs gives a row or a tile its role, name, selection state, roving focus and handlers. Both views use it, so the keyboard
// model is the same in each.
function rowDomAttrs(n: Node, selected: boolean, focused: boolean, RH: RowHandlers) {
  return {
    role: "row",
    "aria-label": n.name,
    "aria-selected": selected,
    tabIndex: focused ? 0 : -1,
    "data-id": n.id,
    // A folder is a place an item can be dropped on: the page reads where from these (see useGestures). Nothing else is.
    ...(n.kind === "dir" ? { "data-drop-folder": n.folder_id, "data-drop-dir": n.id, "data-drop-role": n.role ?? "", "data-drop-name": n.name } : {}),
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
const FileTile = memo(function FileTile({ kind, n, selected, focused, cut, coarse, renaming, renameValue, renameError, scrollMarginTop, H, RH }: TileProps) {
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
        {...rowDomAttrs(n, selected, focused, RH)}
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
        {coarse && <TileCheck n={n} selected={selected} H={H} />}
        <TypeIcon n={n} />
        <OneLine n={n} renaming={renaming} renameValue={renameValue} renameError={renameError} H={H} />
        <KindMarks node={n} />
        <StarMark node={n} />
        <FileMore n={n} tab={-1} H={H} />
        <RenameError error={renaming ? renameError : null} />
      </div>
    );
  }
  return (
    <div
      {...rowDomAttrs(n, selected, focused, RH)}
      role="option"
      style={{
        ...outer,
        flexDirection: "column",
        overflow: "hidden",
        background: selected ? token.colorPrimaryBg : token.colorBgContainer,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, minHeight: coarse ? 44 : 40, paddingInlineStart: 10, paddingInlineEnd: 4 }}>
        {coarse && <TileCheck n={n} selected={selected} H={H} />}
        <TypeIcon n={n} />
        <OneLine n={n} renaming={renaming} renameValue={renameValue} renameError={renameError} H={H} />
        <StarMark node={n} />
        <FileMore n={n} tab={-1} H={H} />
      </div>
      {/* The preview is a fixed height, so every card is as tall as the others whatever its name. The thumbnail is shown where
          there is one; the rest show the type's icon on a tinted area. The play mark and the star sit in its corners. */}
      <div style={{ position: "relative", display: "flex", alignItems: "center", justifyContent: "center", height: PREVIEW_H_PX, minWidth: 0, background: token.colorFillQuaternary }}>
        {thumbable(n) ? (
          <Thumb nod={n.id} version={n.sha256 ?? n.etag ?? ""} height={PREVIEW_H_PX} fallback={<Icon kind={kindOf(n)} size={48} colour={n.color} node={n} />} />
        ) : (
          <Icon kind={kindOf(n)} size={48} colour={n.color} node={n} />
        )}
        <span style={{ position: "absolute", insetBlockEnd: 6, insetInlineStart: 6, display: "inline-flex", alignItems: "center", maxWidth: "calc(100% - 60px)" }}>
          <KindMarks node={n} />
          <LinkBadge node={n} />
        </span>
      </div>
      <RenameError error={renaming ? renameError : null} />
    </div>
  );
});

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

// PresetCheck is a preset row's checkbox, which takes the icon's place on a mouse's screen.
const PresetCheck = memo(function PresetCheck({ id, checked, name, pick }: { id: string; checked: boolean; name: string; pick: ListSelection["pick"] }) {
  const { t } = useT();
  return (
    <span className="row-check" onClick={(e) => e.stopPropagation()} style={{ flex: "none" }}>
      <Checkbox checked={checked} tabIndex={-1} aria-label={t("files.select.item", { name })} onChange={() => pick(id, { toggle: true })} />
    </span>
  );
});

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
}

// PresetEnd is a preset row's end: up to three icons inline, and the ⋮ menu with the rest of the actions and the page's entries.
// A row is drawn again only when its item or its actions change. An icon and an entry run the page's latest action at the click.
const PresetEnd = memo(
  function PresetEnd({ row, inline, entries, moreLabel, moreTip, PH }: PresetEndProps) {
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
          <Dropdown menu={{ items: entries.map((e) => menuItemOf(e, () => PH.entry(row, e.key))) }} trigger={["click"]}>
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
  (a, b) => a.row === b.row && a.sig === b.sig && a.moreLabel === b.moreLabel && a.moreTip === b.moreTip && a.PH === b.PH,
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
// table at the middle size with a fixed layout, the header cell, the row's icons and ⋮ button, and the shared CSS group
// (.files-list, .item-list in index.css). A preset list never takes the files-list class, which e2e waits on.
export default function Listing<T extends { id: string }>(p: ListingProps | ItemListingProps<T>) {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  // Date and role columns hide below the md breakpoint (768px), as the Svelte listing did below 767px.
  const screens = Grid.useBreakpoint();
  const narrow = !screens.md;
  const coarse = useCoarsePointer();
  const stickyTop = p.stickyTop ?? (isItems(p) ? HEADER_H : DEFAULT_STICKY_TOP);
  // A row or tile that the keyboard scrolls to keeps clear of the sticky header, the toolbar and, in the list, the column header.
  const scrollMarginTop = stickyTop + (isItems(p) || p.view === "list" ? THEAD_H : 0) + 4;
  // A preset list's selection and keys (the design, not in this repository, section 2.4). My files keeps its own in FilesPage, and these are not used for it.
  const rows = p.items as { id: string }[];
  const ids = useMemo(() => rows.map((r) => r.id), [rows]);
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
    // inSelectionColumn: a key or a click from the row's checkbox belongs to the checkbox.
    const inCheck = (e: { target: EventTarget }) => !!(e.target as HTMLElement).closest(".ant-table-selection-column, .row-check");
    return {
      // The browser's own drag of an image or a link inside the row is off: an item is dragged by the page's pointer gesture.
      onDragStart: (e: ReactDragEvent<HTMLElement>) => e.preventDefault(),
      onPointerDown: (e: ReactPointerEvent<HTMLElement>) => {
        const id = idOf(e);
        pf.down(id, e.pointerType);
        pointer.current = e.pointerType;
        pressed.current = false;
        endPress();
        if (e.pointerType !== "touch" || files().renameID === id || !files().onRowContext) return;
        const { clientX: x, clientY: y } = e;
        press.current = {
          x,
          y,
          timer: setTimeout(() => {
            press.current = null;
            pressed.current = true;
            liftPending.current = true;
            H.context(id, x, y);
          }, LONG_PRESS_MS),
        };
      },
      onPointerMove: (e: ReactPointerEvent<HTMLElement>) => {
        const cur = press.current;
        if (cur && Math.hypot(e.clientX - cur.x, e.clientY - cur.y) > PRESS_SLOP_PX) endPress();
      },
      onPointerUp: endPress,
      onPointerCancel: endPress,
      // The row under a mouse is known to the icons that draw at its end (review fix 7); a touch does not hover.
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
        // The checkbox column has its own handler, so a click on it must not also select the row.
        if (inCheck(e)) return;
        // A click in the row's own rename field places the cursor there; the item is not opened by it.
        if (files().renameID === idOf(e)) return;
        // The tap that ends a long press is part of the press: it selects and opens nothing.
        if (pressed.current) {
          pressed.current = false;
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
    // pointer, press, pressed, liftPending and endPress are refs and a function over them: the same at every render.
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
      <Button type="text" size="small" onClick={onSort} style={{ fontWeight: 500, padding: 0, display: "inline-flex", alignItems: "center", gap: 4 }}>
        {label}
        <span aria-hidden="true" style={{ display: "inline-flex", width: 14, height: 14, flex: "none" }}>
          {dir === null ? null : dir === "asc" ? <ArrowUp size={14} /> : <ArrowDown size={14} />}
        </span>
      </Button>
    ),
    onHeaderCell: () => ({ "aria-sort": ariaSortOf(dir) }),
  });

  // The actions column's header: a name for a screen reader, shown to no one (review L4).
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
    const check = (r: T) => <PresetCheck id={r.id} checked={picked.has(r.id)} name={q.rowLabel(r)} pick={PH.pick} />;
    const nameCell = (r: T) => (
      <div style={{ minWidth: 0 }}>
        <span style={{ display: "inline-flex", alignItems: "center", gap: 8, minWidth: 0, maxWidth: "100%", verticalAlign: "middle" }}>
          {selectable && !coarse ? (
            <span style={{ display: "inline-flex", alignItems: "center", justifyContent: "center", flex: "none", minWidth: 22, height: 22 }}>
              {q.icon && <span className="row-icon">{q.icon(r)}</span>}
              {check(r)}
            </span>
          ) : (
            q.icon?.(r)
          )}
          <span style={{ minWidth: 0, overflowWrap: "anywhere" }}>{nameCol.render(r)}</span>
        </span>
        {seconds.length > 0 && (
          <div className="row-second" style={{ color: token.colorTextSecondary, fontSize: token.fontSizeSM, overflowWrap: "anywhere" }}>
            {seconds.map((c, i) => (
              <Fragment key={c.key}>
                {i > 0 ? " · " : ""}
                {c.render(r)}
              </Fragment>
            ))}
          </div>
        )}
      </div>
    );
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
        />
      );
    };

    const nameHead = head(nameCol);
    const allBox = (
      <span className="head-check" onClick={(e) => e.stopPropagation()} style={{ flex: "none" }}>
        <Checkbox checked={allPicked} indeterminate={anyPicked && !allPicked} aria-label={t("files.selectAll.box")} onChange={(e) => (e.target.checked ? sel.selectAll() : sel.clear())} />
      </span>
    );
    const columns: ColumnsType<T> = [
      {
        key: nameCol.key,
        ...nameHead,
        title:
          selectable && !coarse ? (
            <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
              {allBox}
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
      !!(e.target as HTMLElement).closest("a, button, input, label, .row-check, .ant-table-selection-column");
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
        if (selectable && (e.ctrlKey || e.metaKey || e.shiftKey)) sel.pick(r.id, { toggle: e.ctrlKey || e.metaKey, range: e.shiftKey });
        else if (q.onOpen) q.onOpen(r);
        else if (selectable) sel.pick(r.id);
      },
      onPointerDown: (e: ReactPointerEvent<HTMLElement>) => {
        pf.down(r.id, e.pointerType);
        endPress();
        pressed.current = false;
        if (e.pointerType !== "touch" || entriesOf(r).length === 0) return;
        const { clientX: x, clientY: y } = e;
        press.current = {
          x,
          y,
          timer: setTimeout(() => {
            press.current = null;
            pressed.current = true;
            liftPending.current = true;
            setRowMenu({ id: r.id, x, y });
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
      <div role="region" aria-label={q.label} ref={itemRef} className="item-list" data-has-selection={anyPicked ? "true" : "false"} style={bandVars}>
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
          size="middle"
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
          rowSelection={
            selectable && coarse
              ? {
                  type: "checkbox",
                  columnWidth: 44,
                  columnTitle: (selectAll: ReactNode) => (
                    <>
                      {selectAll}
                      <span className="sr-only">{t("files.col.select")}</span>
                    </>
                  ),
                  getCheckboxProps: () => ({ tabIndex: -1 }),
                  selectedRowKeys: [...picked],
                  onSelect: (r) => PH.pick(r.id, { toggle: true }),
                  onSelectAll: (checked) => (checked ? sel.selectAll() : sel.clear()),
                }
              : undefined
          }
          onRow={(r) => rowProps(r) as Record<string, unknown>}
        />
        <Dropdown
          open={menuList.length > 0}
          onOpenChange={(open) => {
            if (!open) setRowMenu(null);
          }}
          trigger={[]}
          menu={{ items: menuList.map((e) => menuItemOf(e, e.onSelect)), onClick: () => setRowMenu(null) }}
        >
          {/* The anchor sits at the pointer, which is a place in the window (clientX from the left), so its inset is physical. */}
          <span aria-hidden="true" style={{ position: "fixed", inset: `${rowMenu?.y ?? 0}px auto auto ${rowMenu?.x ?? 0}px`, width: 0, height: 0 }} />
        </Dropdown>
      </div>
    );
  };

  if (isItems(p)) return renderItems(p);

  // ---- The files preset (My files), as it was. ----

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
      H,
    };
  };
  // The select-all box sits in the name's header on a mouse; it shows when something is selected or on the header's hover.
  const someSelected = p.items.some((n) => p.selected.has(n.id));
  const allSelected = p.items.length > 0 && p.items.every((n) => p.selected.has(n.id));
  const selectAllBox = (
    <span className="head-check" onClick={(e) => e.stopPropagation()} style={{ flex: "none" }}>
      <Checkbox checked={allSelected} indeterminate={someSelected && !allSelected} aria-label={t("files.selectAll.box")} onChange={(e) => p.onSelectAll(e.target.checked)} />
    </span>
  );

  // The active column shows its direction as an arrow after the label (headCell, the frame's header).
  const sortHead = (key: SortKey, label: string) => headCell(label, p.sortKey === key ? p.sortDir : null, () => p.onSort(key));

  // The row end: the four icons of the page's row actions (drawn with a mouse, where the row is hovered or selected) and the
  // More button, which every row has. The icons keep their room, so a row does not move when they appear. The signature is of the
  // actions the row draws now: only a selected or focused row draws them without a hover.
  const rowEnd = (n: Node) => {
    const focused = p.focusID === n.id;
    const selected = p.selected.has(n.id);
    const sig = p.rowActs && (focused || selected) ? actsSig(H.acts(n)) : "";
    return <FileRowEnd n={n} selected={selected} focused={focused} coarse={coarse} sig={sig} H={H} />;
  };
  const nameHead = sortHead("name", t("files.col.name"));

  if (p.view === "list") {
    const columns: ColumnsType<Node> = [
      {
        key: "name",
        ...nameHead,
        // A mouse's select-all box sits before the label; a finger keeps the checkbox column instead.
        title: (
          <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
            {!coarse && selectAllBox}
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
      {
        key: "actions",
        title: actionsHead,
        width: coarse ? ROW_ICON_PX : ROW_ICON_PX * (ROW_ICONS + 1),
        align: "end",
        render: (_v, n) => rowEnd(n),
      },
    ];
    return (
      <div role="region" aria-label={p.label} ref={p.containerRef} className="files-list" data-has-selection={someSelected ? "true" : "false"} style={bandVars}>
        <Table<Node>
          rowKey="id"
          size="middle"
          pagination={false}
          // The column header is in this table, not in a table of its own (antd's sticky holder), so it shares the rows' columns in
          // every frame. The stylesheet pins its cells under the toolbar, at --sc-list-top (decision 77, item fu3b).
          tableLayout="fixed"
          columns={columns}
          dataSource={p.items}
          // A mouse has no checkbox column: each row's checkbox is in its icon's place (see FileNameCell). A finger keeps the column.
          rowSelection={coarse ? {
            type: "checkbox",
            columnWidth: 44,
            // antd replaces its select-all box when a column title is given, so the title is a function that keeps it.
            columnTitle: (selectAll: ReactNode) => (
              <>
                {selectAll}
                <span className="sr-only">{t("files.col.select")}</span>
              </>
            ),
            // Each row's checkbox is not a tab stop: the row itself takes focus, and the arrow keys move between rows.
            getCheckboxProps: () => ({ tabIndex: -1 }),
            selectedRowKeys: p.items.filter((n) => p.selected.has(n.id)).map((n) => n.id),
            // A checkbox toggles its row, as Ctrl or Cmd does; the header box selects or clears all.
            onSelect: (record) => H.click(record.id, CTRL_CLICK),
            onSelectAll: (checked) => p.onSelectAll(checked),
          } : undefined}
          onRow={(n) => {
            // The selected band and the hovered band are painted on the cells (index.css), as antd paints a row's cells.
            const style = {
              ...(p.cutIDs?.has(n.id) ? { opacity: CUT_OPACITY } : {}),
              // A row cannot take a text selection, as a tile cannot, so Shift+click does not leave text for Ctrl+C to copy.
              userSelect: "none" as const,
              scrollMarginTop,
            };
            return { ...rowDomAttrs(n, p.selected.has(n.id), p.focusID === n.id, RH), style } as Record<string, unknown>;
          }}
        />
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
    <div role="listbox" aria-label={p.label} aria-multiselectable="true" tabIndex={-1} ref={p.containerRef} style={{ display: "flex", flexDirection: "column", gap: 16, padding: 12, outline: "none" }}>
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

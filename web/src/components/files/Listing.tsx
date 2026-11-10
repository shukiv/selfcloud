// SPDX-License-Identifier: AGPL-3.0-or-later

import {
  Fragment,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type DragEvent as ReactDragEvent,
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
import { useListKeys, useListSelection } from "../../lib/lists";
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

// ListColumn is one column of a preset list (design-tl 2.1). The first column is the name cell: the row's icon, then what
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
  // as aria-pressed (true or false). Left out, the icon has no pressed state. A menu entry has none (design-tl 7).
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
  // selectable gives the list a selection: checkboxes, the selection keys, and a selection bar with the bulk icons.
  selectable?: boolean;
  bulk?: (ids: string[]) => SelectionAction[];
  sort?: ListSort;
  // onOpen opens a row: Enter on the row, or a plain click on it. On a list with a selection, Ctrl, Cmd and Shift clicks and the
  // checkboxes select instead (tl4, design-tl 2.2).
  onOpen?: (row: T) => void;
  // empty is what the table's body shows when there are no rows, under the header row, which stays (design-tl 2.5).
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

// Listing is the one list of the web interface (design-tl, decision 114). Without columns it is the files preset, My files:
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
  // A preset list's selection and keys (design-tl 2.4). My files keeps its own in FilesPage, and these are not used for it.
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

  // A row's icons and More button, and a tile's menu button, are IconButtons: 40 pixels with a mouse and 44 on a finger (index.css).
  // Enter and Space stop at a button, so its own click is the one activation; the arrow keys reach the item's handler, so they move
  // on from the button. A button is a tab stop only in the focused row or tile (the caller's tab value).
  const keysStop = (e: { key: string; stopPropagation: () => void }) => {
    if (e.key === "Enter" || e.key === " ") e.stopPropagation();
  };
  // rowIcon is one of a row's icons. Its click is its own: it does not reach the row. A disabled one keeps its place and the
  // focus: it is aria-disabled, drawn in the disabled colour, and its click and keys do nothing. It is not a disabled button,
  // since a browser takes the focus from a button that turns disabled (the re-check's e2e found the focus on BODY), and the
  // row's icons turn disabled under the pressed one while its request is on its way (decision 114). index.css passes its
  // pointer to the wrapper, as for a disabled one, so it has no hover background and shows the not-allowed cursor; its colour is
  // set here too, so it wins over a danger icon's, which IconButton sets inline.
  const rowIcon = (a: ListAction, tab?: number) => (
    <IconButton
      key={a.key}
      label={a.label}
      icon={a.icon}
      tabIndex={tab}
      pressed={a.pressed}
      href={a.href}
      aria-disabled={a.disabled ? true : undefined}
      danger={a.danger}
      style={a.disabled ? { color: token.colorTextDisabled } : undefined}
      onClick={(e) => {
        e.stopPropagation();
        if (a.disabled) return;
        // A link follows its own address: the browser's click does that, so run is not called for it.
        if (a.href) return;
        a.run();
      }}
      onKeyDown={keysStop}
      onDoubleClick={(e) => e.stopPropagation()}
    />
  );
  const moreIcon = <EllipsisVertical size={18} aria-hidden="true" focusable="false" />;

  // ---- The preset list. ----

  // menuItem is a ⋮ entry. An entry with an address is a real link: the browser opens a middle or a modified click in a new
  // tab, and a plain click or a key runs the entry here, in this tab (review L2).
  const menuItem = (e: ListMenuEntry): NonNullable<MenuProps["items"]>[number] => ({
    key: e.key,
    icon: e.icon,
    danger: e.danger,
    disabled: e.disabled,
    label: e.href ? <a href={e.href}>{e.label}</a> : e.label,
    onClick: ({ domEvent }) => {
      if (e.href && "button" in domEvent && (domEvent.button !== 0 || domEvent.metaKey || domEvent.ctrlKey || domEvent.shiftKey || domEvent.altKey)) return;
      domEvent.preventDefault();
      e.onSelect();
    },
  });

  const renderItems = (q: ItemListingProps<T>) => {
    const [nameCol, ...others] = q.columns;
    // Below md the secondary columns are the name's second line; a phone or a finger has the ⋮ menu alone (design-tl 4).
    const seconds = narrow ? others.filter((c) => c.secondary) : [];
    const shown = narrow ? others.filter((c) => !c.secondary) : others;
    const menuOnly = narrow || coarse;
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
        return [r.id, { inline, entries }] as const;
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
    const check = (r: T) => (
      <span className="row-check" onClick={(e) => e.stopPropagation()} style={{ flex: "none" }}>
        <Checkbox checked={picked.has(r.id)} tabIndex={-1} aria-label={t("files.select.item", { name: q.rowLabel(r) })} onChange={() => sel.pick(r.id, { toggle: true })} />
      </span>
    );
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
        <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "nowrap" }}>
          {end.inline.length > 0 && (
            <span className="row-acts" style={{ display: "inline-flex", justifyContent: "flex-end" }}>
              {end.inline.map((a) => rowIcon(a))}
            </span>
          )}
          {end.entries.length > 0 && (
            <Dropdown menu={{ items: end.entries.map(menuItem) }} trigger={["click"]}>
              <IconButton
                aria-haspopup="menu"
                label={q.moreLabel?.(r) ?? t("files.tile.menu", { name: q.rowLabel(r) })}
                tip={t("files.selection.more")}
                icon={moreIcon}
                onClick={(e) => e.stopPropagation()}
                onDoubleClick={(e) => e.stopPropagation()}
              />
            </Dropdown>
          )}
        </div>
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
      onFocus: () => sel.setFocus(r.id),
      onKeyDown: onItemKey,
      onClick: (e: ReactMouseEvent<HTMLElement>) => {
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
                  onSelect: (r) => sel.pick(r.id, { toggle: true }),
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
          menu={{ items: menuList.map(menuItem), onClick: () => setRowMenu(null) }}
        >
          {/* The anchor sits at the pointer, which is a place in the window (clientX from the left), so its inset is physical. */}
          <span aria-hidden="true" style={{ position: "fixed", inset: `${rowMenu?.y ?? 0}px auto auto ${rowMenu?.x ?? 0}px`, width: 0, height: 0 }} />
        </Dropdown>
      </div>
    );
  };

  if (isItems(p)) return renderItems(p);

  // ---- The files preset (My files), as it was. ----

  // rowAttrs gives a row or a tile its role, name, selection state, roving focus and handlers. Both views use
  // it, so the keyboard model is the same in each.
  const rowAttrs = (n: Node, i: number) => ({
    role: "row",
    "aria-label": n.name,
    "aria-selected": p.selected.has(n.id),
    tabIndex: p.focusID === n.id ? 0 : -1,
    "data-id": n.id,
    // A folder is a place an item can be dropped on: the page reads where from these (see useGestures). Nothing else is.
    ...(n.kind === "dir" ? { "data-drop-folder": n.folder_id, "data-drop-dir": n.id, "data-drop-role": n.role ?? "", "data-drop-name": n.name } : {}),
    // The browser's own drag of an image or a link inside the row is off: an item is dragged by the page's pointer gesture.
    onDragStart: (e: ReactDragEvent<HTMLElement>) => e.preventDefault(),
    onPointerDown: (e: ReactPointerEvent<HTMLElement>) => {
      pointer.current = e.pointerType;
      pressed.current = false;
      endPress();
      if (e.pointerType !== "touch" || p.renameID === n.id || !p.onRowContext) return;
      const { clientX: x, clientY: y } = e;
      press.current = {
        x,
        y,
        timer: setTimeout(() => {
          press.current = null;
          pressed.current = true;
          liftPending.current = true;
          p.onRowContext?.(n.id, i, x, y);
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
      if (e.pointerType === "mouse") setRowHover(n.id);
    },
    onPointerLeave: (e: ReactPointerEvent<HTMLElement>) => {
      if (e.pointerType === "mouse" && getRowHover() === n.id) setRowHover(null);
    },
    onContextMenu: (e: ReactMouseEvent<HTMLElement>) => {
      // The browser's own menu stays in a field the person types in, and on a row being renamed.
      if (p.renameID === n.id || isTextField(e.target)) return;
      e.preventDefault();
      endPress();
      p.onRowContext?.(n.id, i, e.clientX, e.clientY);
    },
    onClick: (e: ReactMouseEvent<HTMLElement>) => {
      // The checkbox column has its own handler, so a click on it must not also select the row.
      if ((e.target as HTMLElement).closest(".ant-table-selection-column, .row-check")) return;
      // A click in the row's own rename field places the cursor there; the item is not opened by it.
      if (p.renameID === n.id) return;
      // The tap that ends a long press is part of the press: it selects and opens nothing.
      if (pressed.current) {
        pressed.current = false;
        return;
      }
      p.onRowClick(i, { shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, metaKey: e.metaKey, touch: pointer.current === "touch" });
    },
    onDoubleClick: (e: ReactMouseEvent<HTMLElement>) => {
      // A finger's double tap has opened the item already, on its first tap. A double click in the rename field selects
      // a word in it, and does not open the item.
      if (pointer.current === "touch" || p.renameID === n.id) return;
      // The item is the one the event reached: its ID is read from the row, not from its place in the list.
      const id = e.currentTarget.dataset.id;
      if (id) p.onRowDoubleClick(id);
    },
    // A key from the row's checkbox belongs to the checkbox, except the arrows that move the selection.
    onKeyDown: (e: ReactKeyboardEvent<HTMLElement>) => {
      if ((e.target as HTMLElement).closest(".ant-table-selection-column, .row-check") && !e.key.startsWith("Arrow")) return;
      p.onKeyDown(e);
    },
    onFocus: () => p.onFocusItem(i),
  });

  const nameField = (n: Node) =>
    p.renameID === n.id ? (
      <Input
        className="rename"
        aria-label={t("files.rename.label")}
        aria-invalid={p.renameError ? true : undefined}
        aria-describedby={p.renameError ? "rename-error" : undefined}
        value={p.renameValue}
        onChange={(e) => p.onRenameChange(e.target.value)}
        onKeyDown={(e) => {
          // Keys typed in the name field stay in the field: Enter must not also open the row.
          e.stopPropagation();
          if (e.key === "Enter") {
            e.preventDefault();
            p.onRename(true);
          } else if (e.key === "Escape") {
            e.preventDefault();
            p.onRename(false);
          }
        }}
        onBlur={() => p.onRename(true)}
        ref={(el) => {
          if (el) {
            el.focus();
            el.select();
          }
        }}
        style={{ flex: "1 1 160px", minWidth: 0, minHeight: 44 }}
      />
    ) : (
      <bdi style={{ overflowWrap: "anywhere", minWidth: 0 }}>{n.name}</bdi>
    );

  const linkBadge = (n: Node) =>
    n.has_link ? (
      <span style={{ display: "inline-flex", marginInlineStart: 8, color: token.colorTextSecondary, verticalAlign: "middle" }}>
        <Link2 size={14} aria-hidden="true" focusable="false" />
        <span className="sr-only">{t("files.linkBadge")}</span>
      </span>
    ) : null;

  // A starred item shows a small filled star after its name. The star is changed from the hover icon, the ⋮ menu or the
  // Starred page; the mark is not a button.
  const starMark = (n: Node) =>
    n.starred ? (
      <span style={{ display: "inline-flex", marginInlineStart: 4, color: token.colorWarning, verticalAlign: "middle", flex: "none" }}>
        <Star size={14} fill="currentColor" aria-hidden="true" focusable="false" />
        <span className="sr-only">{t("star.mark")}</span>
      </span>
    ) : null;

  const renameErr = (n: Node) =>
    p.renameID === n.id && p.renameError ? (
      <div id="rename-error" role="alert" style={{ flexBasis: "100%", color: token.colorError, fontSize: token.fontSizeSM }}>
        {p.renameError}
      </div>
    ) : null;

  // indexOf is each item's place in the page's order, for the handlers that name a row by its index.
  const indexOf = new Map(p.items.map((n, i) => [n.id, i]));
  // rowCheck is a mouse's checkbox for a row. It takes the file icon's place when the row is hovered, or selected, or when
  // anything is selected (the CSS in index.css decides); it is not a tab stop, and a click on it selects by the box alone.
  const rowCheck = (n: Node) => (
    <span className="row-check" onClick={(e) => e.stopPropagation()} style={{ flex: "none" }}>
      <Checkbox
        checked={p.selected.has(n.id)}
        tabIndex={-1}
        aria-label={t("files.select.item", { name: n.name })}
        onChange={() => p.onRowClick(indexOf.get(n.id) ?? 0, { shiftKey: false, ctrlKey: true, metaKey: false })}
      />
    </span>
  );
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

  // The More button opens the item's menu, where the right-click menu opens: at the button. A list row and a tile both have it.
  const moreButton = (n: Node, i: number, tab = -1) => (
    <IconButton
      tabIndex={tab}
      label={t("files.tile.menu", { name: n.name })}
      tip={t("files.selection.more")}
      aria-haspopup="menu"
      onClick={(e) => {
        e.stopPropagation();
        const r = e.currentTarget.getBoundingClientRect();
        // The menu opens at the button's start edge: its left in English, its right in Hebrew (review fix 11). A key's click has
        // no pointer, so its detail is 0: the menu then takes the focus (review fix 1).
        // The start edge is the button's x in English and its x plus its width in Hebrew, where the start is on the right.
        const rtl = document.documentElement.dir === "rtl";
        const startX = rtl ? r.x + r.width : r.x;
        p.onRowContext?.(n.id, i, startX, r.bottom, e.detail === 0 ? e.currentTarget : undefined);
      }}
      onKeyDown={keysStop}
      onDoubleClick={(e) => e.stopPropagation()}
      icon={moreIcon}
    />
  );
  const tileMenu = (n: Node, i: number) => moreButton(n, i);

  // The row end: the four icons of the page's row actions (drawn with a mouse, where the row is hovered or selected) and the
  // More button, which every row has. The icons keep their room, so a row does not move when they appear.
  const rowEnd = (n: Node) => {
    const focused = p.focusID === n.id;
    return (
      <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "nowrap" }}>
        {!coarse && (
          <span className="row-acts" style={{ display: "inline-flex", justifyContent: "flex-end", minWidth: ROW_ICON_PX * ROW_ICONS }}>
            <RowActs id={n.id} show={p.selected.has(n.id) || focused}>
              {() =>
                // A files action that is not enabled is one the item's menu does not offer, so it is not drawn (unlike a
                // preset list's disabled action, which is one on its way).
                (p.rowActs?.(n) ?? []).filter((a) => a.enabled).map((a) => rowIcon(a, focused ? 0 : -1))
              }
            </RowActs>
          </span>
        )}
        {moreButton(n, indexOf.get(n.id) ?? 0, focused ? 0 : -1)}
      </div>
    );
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
        render: (_v, n) => (
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
                {rowCheck(n)}
              </span>
            )}
            {nameField(n)}
            {linkBadge(n)}
            <KindMarks node={n} />
            {starMark(n)}
            {renameErr(n)}
          </span>
        ),
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
          // A mouse has no checkbox column: each row's checkbox is in its icon's place (see rowCheck). A finger keeps the column.
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
            onSelect: (record) => p.onRowClick(p.items.findIndex((x) => x.id === record.id), { shiftKey: false, ctrlKey: true, metaKey: false }),
            onSelectAll: (checked) => p.onSelectAll(checked),
          } : undefined}
          onRow={(n, i) => {
            // The selected band and the hovered band are painted on the cells (index.css), as antd paints a row's cells.
            const style = {
              ...(p.cutIDs?.has(n.id) ? { opacity: CUT_OPACITY } : {}),
              // A row cannot take a text selection, as a tile cannot, so Shift+click does not leave text for Ctrl+C to copy.
              userSelect: "none" as const,
              scrollMarginTop,
            };
            return { ...rowAttrs(n, i ?? 0), style: Object.keys(style).length ? style : undefined } as Record<string, unknown>;
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
  // The checkbox of a finger's screen: a tile has no click that selects it, so its checkbox does (a list row has one already).
  // The wrapper stops the click: the checkbox toggles the item, and the item does not also open.
  const tileCheck = (n: Node, i: number) =>
    coarse ? (
      <span onClick={(e) => e.stopPropagation()} style={{ display: "inline-flex", flex: "none" }}>
        <Checkbox
          checked={p.selected.has(n.id)}
          tabIndex={-1}
          aria-label={t("files.select.item", { name: n.name })}
          onChange={() => p.onRowClick(i, { shiftKey: false, ctrlKey: true, metaKey: false })}
        />
      </span>
    ) : null;
  // The type's icon keeps its size when the name is long: a flex item would shrink to nothing beside it.
  const typeIcon = (n: Node) => (
    <span style={{ display: "inline-flex", flex: "none" }}>
      <Icon kind={kindOf(n)} size={20} colour={n.color} node={n} />
    </span>
  );
  // The name is one line: it is cut with an ellipsis, and its full text is in the title. The box takes the row's direction
  // and sits at its start, so a name in another script does not drift to the far end; the bdi keeps the name's own direction.
  const oneLine = (n: Node) =>
    p.renameID === n.id ? (
      nameField(n)
    ) : (
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
  const chip = (n: Node) => {
    const i = indexOf.get(n.id) ?? 0;
    const isSel = p.selected.has(n.id);
    return (
      <div
        key={n.id}
        {...rowAttrs(n, i)}
        role="option"
        style={{
          position: "relative",
          display: "flex",
          alignItems: "center",
          gap: 6,
          minWidth: 0,
          minHeight: coarse ? 44 : 40,
          paddingInlineStart: 10,
          paddingInlineEnd: 4,
          boxSizing: "border-box",
          border: `1px solid ${isSel ? token.colorPrimary : token.colorBorderSecondary}`,
          borderRadius: token.borderRadiusLG,
          background: isSel ? token.colorPrimaryBg : undefined,
          outline: isSel ? `2px solid ${token.colorPrimary}` : undefined,
          outlineOffset: isSel ? -2 : undefined,
          cursor: "default",
          userSelect: "none",
          scrollMarginTop,
          opacity: p.cutIDs?.has(n.id) ? CUT_OPACITY : undefined,
        }}
      >
        {tileCheck(n, i)}
        {typeIcon(n)}
        {oneLine(n)}
        <KindMarks node={n} />
        {starMark(n)}
        {tileMenu(n, i)}
        {renameErr(n)}
      </div>
    );
  };
  const card = (n: Node) => {
    const i = indexOf.get(n.id) ?? 0;
    const isSel = p.selected.has(n.id);
    return (
      <div
        key={n.id}
        {...rowAttrs(n, i)}
        role="option"
        style={{
          position: "relative",
          display: "flex",
          flexDirection: "column",
          minWidth: 0,
          overflow: "hidden",
          border: `1px solid ${isSel ? token.colorPrimary : token.colorBorderSecondary}`,
          borderRadius: token.borderRadiusLG,
          background: isSel ? token.colorPrimaryBg : token.colorBgContainer,
          // A selected tile has an outline as well as its tint, so the selection shows at a glance.
          outline: isSel ? `2px solid ${token.colorPrimary}` : undefined,
          outlineOffset: isSel ? -2 : undefined,
          cursor: "default",
          userSelect: "none",
          scrollMarginTop,
          opacity: p.cutIDs?.has(n.id) ? CUT_OPACITY : undefined,
        }}
      >
        <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, minHeight: coarse ? 44 : 40, paddingInlineStart: 10, paddingInlineEnd: 4 }}>
          {tileCheck(n, i)}
          {typeIcon(n)}
          {oneLine(n)}
          {starMark(n)}
          {tileMenu(n, i)}
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
            {linkBadge(n)}
          </span>
        </div>
        {renameErr(n)}
      </div>
    );
  };
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

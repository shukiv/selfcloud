// SPDX-License-Identifier: AGPL-3.0-or-later

import {
  createContext,
  memo,
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
  type PointerEvent as ReactPointerEvent,
  type CSSProperties,
  type MutableRefObject,
  type ReactNode,
  type Ref,
} from "react";
import { Button, Checkbox, Grid, Input, Table, theme as antdTheme } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ArrowDown, ArrowUp, EllipsisVertical, Link2, Star } from "lucide-react";
import IconButton from "../IconButton";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import type { SortDir, SortKey } from "../../lib/files/sort";
import type { Node } from "../../lib/files/types";
import type { ViewMode } from "../../lib/files/viewPrefs";
import { isTextField } from "../../lib/files/keys";
import { useCoarsePointer } from "../../lib/pointer";
import { THEAD_H } from "../../lib/files/layout";
import { getRowHover, setRowHover, subscribeRowHover } from "./rowHover";
import { BIT_CUT, BIT_FOCUSED, BIT_SELECTED, createRowStore, useRename, useRowBits, type RenameState, type RowStore } from "./rowState";
import Icon from "./Icon";
import KindMarks from "./KindMarks";
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

export interface ListingProps {
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
// NO_IDS is the cut set when the page gives none, so the store keeps one identity.
const NO_IDS: Set<string> = new Set();

type AriaSort = "ascending" | "descending" | "none";

function ariaSort(current: SortKey, dir: SortDir, key: SortKey): AriaSort {
  if (current !== key) return "none";
  return dir === "asc" ? "ascending" : "descending";
}

// RowActs draws a list row's icons only while the row is hovered by a mouse, or selected, or has the focus (review fix 7): the icons
// are built by `children` only then, so a folder of 500 rows draws the few rows the person is on, not all of them.
function RowActs({ id, show, children }: { id: string; show: boolean; children: () => ReactNode }) {
  const hovered = useSyncExternalStore(subscribeRowHover, () => getRowHover() === id);
  return hovered || show ? <>{children()}</> : null;
}

// Live is the latest props of the listing, with the places of its items. A row keeps one identity while the page renders again,
// so the page's callbacks, which it makes anew on each render, do not draw the rows again; each event reads the latest ones.
interface Live {
  p: ListingProps;
  index: Map<string, number>;
}

// Gestures are the touch and pointer states of a listing: a finger's long press, and the lift that follows it.
interface Gestures {
  pointer: MutableRefObject<string>;
  press: MutableRefObject<{ x: number; y: number; timer: ReturnType<typeof setTimeout> } | null>;
  pressed: MutableRefObject<boolean>;
  liftPending: MutableRefObject<boolean>;
}

// Cells is what a row reads: the latest props, the row state, the gestures, and the text, language and theme. A row's cells
// are drawn once and read these through the context, so a row changes only when its own state or one of these does.
interface Cells {
  live: MutableRefObject<Live>;
  store: RowStore;
  gestures: Gestures;
  coarse: boolean;
  lang: ReturnType<typeof useLang>;
  t: ReturnType<typeof useT>["t"];
  token: ReturnType<typeof antdTheme.useToken>["token"];
  scrollMarginTop: number;
}

const CellsContext = createContext<Cells | null>(null);

function useCells(): Cells {
  const c = useContext(CellsContext);
  if (c === null) throw new Error("a row is drawn outside its listing");
  return c;
}

// recordChanged is antd's test of a cell: a cell is drawn again only when its item is another object. The cell reads everything else
// through the context and the row store, so its own render function never needs to run again.
const recordChanged = (next: Node, prev: Node) => next !== prev;

// indexIn is an item's place in the page's order, read when an event fires.
const indexIn = (c: Cells, id: string) => c.live.current.index.get(id) ?? 0;

function clearPress(g: Gestures) {
  if (g.press.current) clearTimeout(g.press.current.timer);
  g.press.current = null;
}

// rowAttrs gives a row or a tile its role, name, selection state, roving focus and handlers. Both views use it, so the keyboard
// model is the same in each. bits is the row's state from the store.
function rowAttrs(c: Cells, n: Node, bits: number) {
  const g = c.gestures;
  const p = () => c.live.current.p;
  return {
    role: "row",
    "aria-label": n.name,
    "aria-selected": (bits & BIT_SELECTED) !== 0,
    tabIndex: (bits & BIT_FOCUSED) !== 0 ? 0 : -1,
    "data-id": n.id,
    // A folder is a place an item can be dropped on: the page reads where from these (see useGestures). Nothing else is.
    ...(n.kind === "dir" ? { "data-drop-folder": n.folder_id, "data-drop-dir": n.id, "data-drop-role": n.role ?? "", "data-drop-name": n.name } : {}),
    // The browser's own drag of an image or a link inside the row is off: an item is dragged by the page's pointer gesture.
    onDragStart: (e: ReactDragEvent<HTMLElement>) => e.preventDefault(),
    onPointerDown: (e: ReactPointerEvent<HTMLElement>) => {
      g.pointer.current = e.pointerType;
      g.pressed.current = false;
      clearPress(g);
      if (e.pointerType !== "touch" || p().renameID === n.id || !p().onRowContext) return;
      const { clientX: x, clientY: y } = e;
      g.press.current = {
        x,
        y,
        timer: setTimeout(() => {
          g.press.current = null;
          g.pressed.current = true;
          g.liftPending.current = true;
          p().onRowContext?.(n.id, indexIn(c, n.id), x, y);
        }, LONG_PRESS_MS),
      };
    },
    onPointerMove: (e: ReactPointerEvent<HTMLElement>) => {
      const cur = g.press.current;
      if (cur && Math.hypot(e.clientX - cur.x, e.clientY - cur.y) > PRESS_SLOP_PX) clearPress(g);
    },
    onPointerUp: () => clearPress(g),
    onPointerCancel: () => clearPress(g),
    // The row under a mouse is known to the icons that draw at its end (review fix 7); a touch does not hover.
    onPointerEnter: (e: ReactPointerEvent<HTMLElement>) => {
      if (e.pointerType === "mouse") setRowHover(n.id);
    },
    onPointerLeave: (e: ReactPointerEvent<HTMLElement>) => {
      if (e.pointerType === "mouse" && getRowHover() === n.id) setRowHover(null);
    },
    onContextMenu: (e: ReactMouseEvent<HTMLElement>) => {
      // The browser's own menu stays in a field the person types in, and on a row being renamed.
      if (p().renameID === n.id || isTextField(e.target)) return;
      e.preventDefault();
      clearPress(g);
      p().onRowContext?.(n.id, indexIn(c, n.id), e.clientX, e.clientY);
    },
    onClick: (e: ReactMouseEvent<HTMLElement>) => {
      // The checkbox column has its own handler, so a click on it must not also select the row.
      if ((e.target as HTMLElement).closest(".ant-table-selection-column, .row-check")) return;
      // A click in the row's own rename field places the cursor there; the item is not opened by it.
      if (p().renameID === n.id) return;
      // The tap that ends a long press is part of the press: it selects and opens nothing.
      if (g.pressed.current) {
        g.pressed.current = false;
        return;
      }
      p().onRowClick(indexIn(c, n.id), { shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, metaKey: e.metaKey, touch: g.pointer.current === "touch" });
    },
    onDoubleClick: (e: ReactMouseEvent<HTMLElement>) => {
      // A finger's double tap has opened the item already, on its first tap. A double click in the rename field selects
      // a word in it, and does not open the item.
      if (g.pointer.current === "touch" || p().renameID === n.id) return;
      // The item is the one the event reached: its ID is read from the row, not from its place in the list.
      const id = e.currentTarget.dataset.id;
      if (id) p().onRowDoubleClick(id);
    },
    // A key from the row's checkbox belongs to the checkbox, except the arrows that move the selection.
    onKeyDown: (e: ReactKeyboardEvent<HTMLElement>) => {
      if ((e.target as HTMLElement).closest(".ant-table-selection-column, .row-check") && !e.key.startsWith("Arrow")) return;
      p().onKeyDown(e);
    },
    onFocus: () => p().onFocusItem(indexIn(c, n.id)),
  };
}

// sizeOf is a file's size. A folder and a shortcut have none to show.
function sizeOf(n: Node, lang: string): string {
  return n.kind === "file" ? formatBytes(n.size, lang) : "";
}

// keysStop stops Enter and Space at a button, so its own click is the one activation.
const keysStop = (e: { key: string; stopPropagation: () => void }) => {
  if (e.key === "Enter" || e.key === " ") e.stopPropagation();
};

// SelectBox is a row's checkbox. It is not a tab stop: the row takes the focus, and the arrows move between rows.
function SelectBox({ node }: { node: Node }) {
  const c = useCells();
  const sel = (useRowBits(c.store, node.id) & BIT_SELECTED) !== 0;
  return (
    <Checkbox
      checked={sel}
      tabIndex={-1}
      aria-label={c.t("files.select.item", { name: node.name })}
      onChange={() => c.live.current.p.onRowClick(indexIn(c, node.id), { shiftKey: false, ctrlKey: true, metaKey: false })}
    />
  );
}

// rowCheck is a mouse's checkbox for a row. It takes the file icon's place when the row is hovered, or selected, or when
// anything is selected (the CSS in index.css decides); it is not a tab stop, and a click on it selects by the box alone.
function rowCheck(node: Node) {
  return (
    <span className="row-check" onClick={(e) => e.stopPropagation()} style={{ flex: "none" }}>
      <SelectBox node={node} />
    </span>
  );
}

// The More button opens the item's menu, where the right-click menu opens: at the button. A list row and a tile both have it.
function moreButton(c: Cells, n: Node, tab = -1) {
  return (
    <IconButton
      tabIndex={tab}
      label={c.t("files.tile.menu", { name: n.name })}
      tip={c.t("files.selection.more")}
      aria-haspopup="menu"
      onClick={(e) => {
        e.stopPropagation();
        const r = e.currentTarget.getBoundingClientRect();
        // The menu opens at the button's start edge: its left in English, its right in Hebrew (review fix 11). A key's click has
        // no pointer, so its detail is 0: the menu then takes the focus (review fix 1).
        // The start edge is the button's x in English and its x plus its width in Hebrew, where the start is on the right.
        const rtl = document.documentElement.dir === "rtl";
        const startX = rtl ? r.x + r.width : r.x;
        c.live.current.p.onRowContext?.(n.id, indexIn(c, n.id), startX, r.bottom, e.detail === 0 ? e.currentTarget : undefined);
      }}
      onKeyDown={keysStop}
      onDoubleClick={(e) => e.stopPropagation()}
      icon={<EllipsisVertical size={18} aria-hidden="true" focusable="false" />}
    />
  );
}

function linkBadge(c: Cells, n: Node) {
  return n.has_link ? (
    <span style={{ display: "inline-flex", marginInlineStart: 8, color: c.token.colorTextSecondary, verticalAlign: "middle" }}>
      <Link2 size={14} aria-hidden="true" focusable="false" />
      <span className="sr-only">{c.t("files.linkBadge")}</span>
    </span>
  ) : null;
}

// A starred item shows a small filled star after its name. The star is changed from the hover icon, the ⋮ menu or the
// Starred page; the mark is not a button.
function starMark(c: Cells, n: Node) {
  return n.starred ? (
    <span style={{ display: "inline-flex", marginInlineStart: 4, color: c.token.colorWarning, verticalAlign: "middle", flex: "none" }}>
      <Star size={14} fill="currentColor" aria-hidden="true" focusable="false" />
      <span className="sr-only">{c.t("star.mark")}</span>
    </span>
  ) : null;
}

function renameErr(c: Cells, n: Node, ren: RenameState | null) {
  return ren && ren.id === n.id && ren.error ? (
    <div id="rename-error" role="alert" style={{ flexBasis: "100%", color: c.token.colorError, fontSize: c.token.fontSizeSM }}>
      {ren.error}
    </div>
  ) : null;
}

function nameField(c: Cells, n: Node, ren: RenameState | null) {
  if (ren === null || ren.id !== n.id) return <bdi style={{ overflowWrap: "anywhere", minWidth: 0 }}>{n.name}</bdi>;
  return (
    <Input
      className="rename"
      aria-label={c.t("files.rename.label")}
      aria-invalid={ren.error ? true : undefined}
      aria-describedby={ren.error ? "rename-error" : undefined}
      value={ren.value}
      onChange={(e) => c.live.current.p.onRenameChange(e.target.value)}
      onKeyDown={(e) => {
        // Keys typed in the name field stay in the field: Enter must not also open the row.
        e.stopPropagation();
        if (e.key === "Enter") {
          e.preventDefault();
          c.live.current.p.onRename(true);
        } else if (e.key === "Escape") {
          e.preventDefault();
          c.live.current.p.onRename(false);
        }
      }}
      onBlur={() => c.live.current.p.onRename(true)}
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

// NameCell is a list row's name: its icon, its checkbox (a mouse's), its name or rename field, and its marks.
function NameCell({ node }: { node: Node }) {
  const c = useCells();
  const ren = useRename(c.store, node.id);
  return (
    <span style={{ display: "inline-flex", alignItems: "center", gap: 8, flexWrap: "wrap", minWidth: 0, maxWidth: "100%" }}>
      {c.coarse ? (
        <Icon kind={kindOf(node)} size={22} colour={node.color} node={node} />
      ) : (
        <span style={{ display: "inline-flex", alignItems: "center", justifyContent: "center", flex: "none", width: 22, height: 22 }}>
          <span className="row-icon">
            <Icon kind={kindOf(node)} size={22} colour={node.color} node={node} />
          </span>
          {rowCheck(node)}
        </span>
      )}
      {nameField(c, node, ren)}
      {linkBadge(c, node)}
      <KindMarks node={node} />
      {starMark(c, node)}
      {renameErr(c, node, ren)}
    </span>
  );
}

function SizeCell({ node }: { node: Node }) {
  const c = useCells();
  return <span style={{ fontVariantNumeric: "tabular-nums", fontSize: c.token.fontSizeSM }}>{sizeOf(node, c.lang)}</span>;
}

function ModifiedCell({ node }: { node: Node }) {
  const c = useCells();
  return <span style={{ fontSize: c.token.fontSizeSM }}>{formatDate(node.modified_at, c.lang)}</span>;
}

function RoleCell({ node }: { node: Node }) {
  const c = useCells();
  return <span style={{ color: c.token.colorTextSecondary, fontSize: c.token.fontSizeSM }}>{node.role ? c.t(`role.${node.role}`) : ""}</span>;
}

// ActionsCell is a list row's end: the four icons of the page's row actions (drawn with a mouse, where the row is hovered or
// selected) and the More button, which every row has. The icons keep their room, so a row does not move when they appear.
function ActionsCell({ node }: { node: Node }) {
  const c = useCells();
  const bits = useRowBits(c.store, node.id);
  const focused = (bits & BIT_FOCUSED) !== 0;
  const selected = (bits & BIT_SELECTED) !== 0;
  return (
    <div style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", flexWrap: "nowrap" }}>
      {!c.coarse && (
        <span className="row-acts" style={{ display: "inline-flex", justifyContent: "flex-end", minWidth: ROW_ICON_PX * ROW_ICONS }}>
          <RowActs id={node.id} show={selected || focused}>
            {() =>
              (c.live.current.p.rowActs?.(node) ?? [])
                .filter((a) => a.enabled)
                .map((a) => (
                  <IconButton
                    key={a.key}
                    label={a.label}
                    icon={a.icon}
                    tabIndex={focused ? 0 : -1}
                    onClick={(e) => {
                      e.stopPropagation();
                      a.run();
                    }}
                    onKeyDown={keysStop}
                    onDoubleClick={(e) => e.stopPropagation()}
                  />
                ))
            }
          </RowActs>
        </span>
      )}
      {moreButton(c, node, focused ? 0 : -1)}
    </div>
  );
}

// The type's icon keeps its size when the name is long: a flex item would shrink to nothing beside it.
function typeIcon(n: Node, size: number) {
  return (
    <span style={{ display: "inline-flex", flex: "none" }}>
      <Icon kind={kindOf(n)} size={size} colour={n.color} node={n} />
    </span>
  );
}

// The name is one line: it is cut with an ellipsis, and its full text is in the title. The box takes the row's direction
// and sits at its start, so a name in another script does not drift to the far end; the bdi keeps the name's own direction.
function oneLine(c: Cells, n: Node, ren: RenameState | null) {
  if (ren !== null && ren.id === n.id) return nameField(c, n, ren);
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

// The tile's checkbox, on a finger's screen: a tile has no click that selects it, so its checkbox does (a list row has one
// already). The wrapper stops the click: the checkbox toggles the item, and the item does not also open.
function tileCheck(c: Cells, n: Node) {
  return c.coarse ? (
    <span onClick={(e) => e.stopPropagation()} style={{ display: "inline-flex", flex: "none" }}>
      <SelectBox node={n} />
    </span>
  ) : null;
}

// ChipTile is a folder in the tile view: a row of one line. Its state comes as bits from the listing's render, so a tile's
// focus and selection show in the same pass as the rest of the listing (its attributes are read by the page's focus code).
const ChipTile = memo(function ChipTile({ node, bits }: { node: Node; bits: number }) {
  const c = useCells();
  const ren = useRename(c.store, node.id);
  const isSel = (bits & BIT_SELECTED) !== 0;
  return (
    <div
      {...rowAttrs(c, node, bits)}
      role="option"
      style={{
        position: "relative",
        display: "flex",
        alignItems: "center",
        gap: 6,
        minWidth: 0,
        minHeight: c.coarse ? 44 : 40,
        paddingInlineStart: 10,
        paddingInlineEnd: 4,
        boxSizing: "border-box",
        border: `1px solid ${isSel ? c.token.colorPrimary : c.token.colorBorderSecondary}`,
        borderRadius: c.token.borderRadiusLG,
        background: isSel ? c.token.colorPrimaryBg : undefined,
        outline: isSel ? `2px solid ${c.token.colorPrimary}` : undefined,
        outlineOffset: isSel ? -2 : undefined,
        cursor: "default",
        userSelect: "none",
        scrollMarginTop: c.scrollMarginTop,
        opacity: (bits & BIT_CUT) !== 0 ? CUT_OPACITY : undefined,
      }}
    >
      {tileCheck(c, node)}
      {typeIcon(node, 20)}
      {oneLine(c, node, ren)}
      <KindMarks node={node} />
      {starMark(c, node)}
      {moreButton(c, node)}
      {renameErr(c, node, ren)}
    </div>
  );
});

// CardTile is a file in the tile view: a card with a preview of a fixed height. Its state comes as bits, as ChipTile's does.
const CardTile = memo(function CardTile({ node, bits }: { node: Node; bits: number }) {
  const c = useCells();
  const ren = useRename(c.store, node.id);
  const isSel = (bits & BIT_SELECTED) !== 0;
  return (
    <div
      {...rowAttrs(c, node, bits)}
      role="option"
      style={{
        position: "relative",
        display: "flex",
        flexDirection: "column",
        minWidth: 0,
        overflow: "hidden",
        border: `1px solid ${isSel ? c.token.colorPrimary : c.token.colorBorderSecondary}`,
        borderRadius: c.token.borderRadiusLG,
        background: isSel ? c.token.colorPrimaryBg : c.token.colorBgContainer,
        // A selected tile has an outline as well as its tint, so the selection shows at a glance.
        outline: isSel ? `2px solid ${c.token.colorPrimary}` : undefined,
        outlineOffset: isSel ? -2 : undefined,
        cursor: "default",
        userSelect: "none",
        scrollMarginTop: c.scrollMarginTop,
        opacity: (bits & BIT_CUT) !== 0 ? CUT_OPACITY : undefined,
      }}
    >
      <div style={{ display: "flex", alignItems: "center", gap: 8, minWidth: 0, minHeight: c.coarse ? 44 : 40, paddingInlineStart: 10, paddingInlineEnd: 4 }}>
        {tileCheck(c, node)}
        {typeIcon(node, 20)}
        {oneLine(c, node, ren)}
        {starMark(c, node)}
        {moreButton(c, node)}
      </div>
      {/* The preview is a fixed height, so every card is as tall as the others whatever its name. The thumbnail is shown where
          there is one; the rest show the type's icon on a tinted area. The play mark and the star sit in its corners. */}
      <div style={{ position: "relative", display: "flex", alignItems: "center", justifyContent: "center", height: PREVIEW_H_PX, minWidth: 0, background: c.token.colorFillQuaternary }}>
        {thumbable(node) ? (
          <Thumb nod={node.id} version={node.sha256 ?? node.etag ?? ""} height={PREVIEW_H_PX} fallback={<Icon kind={kindOf(node)} size={48} colour={node.color} node={node} />} />
        ) : (
          <Icon kind={kindOf(node)} size={48} colour={node.color} node={node} />
        )}
        <span style={{ position: "absolute", insetBlockEnd: 6, insetInlineStart: 6, display: "inline-flex", alignItems: "center", maxWidth: "calc(100% - 60px)" }}>
          <KindMarks node={node} />
          {linkBadge(c, node)}
        </span>
      </div>
      {renameErr(c, node, ren)}
    </div>
  );
});

// Listing shows a folder's items: a table in list view, tiles in grid view. Keyboard handling belongs to
// the page; this component reports clicks, keys and focus, and marks what is selected.
export default function Listing(p: ListingProps) {
  const { t } = useT();
  const lang = useLang();
  const { token } = antdTheme.useToken();
  // Date and role columns hide below the md breakpoint (768px), as the Svelte listing did below 767px.
  const screens = Grid.useBreakpoint();
  const narrow = !screens.md;
  const coarse = useCoarsePointer();
  const stickyTop = p.stickyTop ?? DEFAULT_STICKY_TOP;
  // A row or tile that the keyboard scrolls to keeps clear of the sticky header, the toolbar and, in the list, the column header.
  const scrollMarginTop = stickyTop + (p.view === "list" ? THEAD_H : 0) + 4;
  // The row state is written here on each render, before the rows are drawn; the rows learn of a change after the commit.
  const [store] = useState<RowStore>(createRowStore);
  store.set({
    selected: p.selected,
    focusID: p.focusID,
    cutIDs: p.cutIDs ?? NO_IDS,
    renameID: p.renameID,
    renameValue: p.renameValue,
    renameError: p.renameError,
  });
  useLayoutEffect(() => {
    store.notify();
  });
  const indexMap = useMemo(() => new Map(p.items.map((n, i) => [n.id, i])), [p.items]);
  const live = useRef<Live>({ p, index: indexMap });
  live.current = { p, index: indexMap };
  // The kind of pointer that last went down on a row. Its click is the one that follows, so a finger's tap is known
  // to be a tap and a mouse's click is known to be a click, each by its own event.
  const gestures = useRef<Gestures>({
    pointer: { current: "mouse" },
    press: { current: null },
    pressed: { current: false },
    liftPending: { current: false },
  }).current;
  useEffect(() => () => clearPress(gestures), [gestures]);
  // liftPending says a long press has opened its menu and no new press has begun since. Until one does, the mousedowns the
  // browser sends for the lifted finger are stopped, since the menu closes on a mousedown and would close at the lift. A
  // real mouse press always begins with a pointerdown, which clears the flag first. The listeners are added at mount, so
  // they run before the menu's own listener, which is added when the menu opens.
  useEffect(() => {
    const newPress = () => {
      gestures.liftPending.current = false;
    };
    const stopLiftMouseDown = (e: MouseEvent) => {
      if (gestures.liftPending.current) e.stopImmediatePropagation();
    };
    window.addEventListener("pointerdown", newPress, true);
    window.addEventListener("mousedown", stopLiftMouseDown, true);
    return () => {
      window.removeEventListener("pointerdown", newPress, true);
      window.removeEventListener("mousedown", stopLiftMouseDown, true);
    };
  }, [gestures]);
  const cells = useMemo<Cells>(
    () => ({ live, store, gestures, coarse, lang, t, token, scrollMarginTop }),
    [live, store, gestures, coarse, lang, t, token, scrollMarginTop],
  );

  const nameLabel = t("files.col.name");
  // The column header's sort button: its active column shows its direction as an arrow after the label. The arrow is decorative
  // (aria-hidden), so the button's name stays the label alone; its box is always there, so the label does not move when the sort changes.
  const sortHead = (key: SortKey, label: string) => ({
    title: (
      <Button type="text" size="small" onClick={() => p.onSort(key)} style={{ fontWeight: 500, padding: 0, display: "inline-flex", alignItems: "center", gap: 4 }}>
        {label}
        <span aria-hidden="true" style={{ display: "inline-flex", width: 14, height: 14, flex: "none" }}>
          {p.sortKey === key ? (p.sortDir === "asc" ? <ArrowUp size={14} /> : <ArrowDown size={14} />) : null}
        </span>
      </Button>
    ),
    onHeaderCell: () => ({ "aria-sort": ariaSort(p.sortKey, p.sortDir, key) }),
  });

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
  // The select-all box sits in the name's header on a mouse; it shows when something is selected or on the header's hover.
  const someSelected = p.items.some((n) => p.selected.has(n.id));
  const allSelected = p.items.length > 0 && p.items.every((n) => p.selected.has(n.id));
  const selectAllBox = (
    <span className="head-check" onClick={(e) => e.stopPropagation()} style={{ flex: "none" }}>
      <Checkbox checked={allSelected} indeterminate={someSelected && !allSelected} aria-label={t("files.selectAll.box")} onChange={(e) => p.onSelectAll(e.target.checked)} />
    </span>
  );

  const nameHead = sortHead("name", nameLabel);

  if (p.view === "list") {
    const columns: ColumnsType<Node> = [
      // A finger keeps a checkbox column; a mouse has none, and each row's checkbox is in its icon's place (see rowCheck). The
      // column is the page's own, not antd's rowSelection: antd redraws every checkbox on each render of the table.
      ...(coarse
        ? [
            {
              key: "select",
              className: "ant-table-selection-column",
              width: 44,
              shouldCellUpdate: recordChanged,
              title: (
                <>
                  <Checkbox checked={allSelected} indeterminate={someSelected && !allSelected} aria-label={t("files.selectAll.box")} onChange={(e) => p.onSelectAll(e.target.checked)} />
                  <span className="sr-only">{t("files.col.select")}</span>
                </>
              ),
              render: (_v: unknown, n: Node) => <SelectBox node={n} />,
            },
          ]
        : []),
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
        shouldCellUpdate: recordChanged,
        render: (_v, n) => <NameCell node={n} />,
      },
      {
        key: "size",
        ...sortHead("size", t("files.col.size")),
        align: "end",
        width: 96,
        shouldCellUpdate: recordChanged,
        render: (_v, n) => <SizeCell node={n} />,
      },
      ...(narrow
        ? []
        : [
            {
              key: "modified",
              ...sortHead("modified", t("files.col.modified")),
              width: 170,
              shouldCellUpdate: recordChanged,
              render: (_v: unknown, n: Node) => <ModifiedCell node={n} />,
            },
            {
              key: "role",
              // Not sortable, so not a button; the same weight as the sortable headers beside it.
              title: <span style={{ fontWeight: 500 }}>{t("files.col.role")}</span>,
              width: 120,
              shouldCellUpdate: recordChanged,
              render: (_v: unknown, n: Node) => <RoleCell node={n} />,
            },
          ]),
      {
        key: "actions",
        title: null,
        width: coarse ? ROW_ICON_PX : ROW_ICON_PX * (ROW_ICONS + 1),
        align: "end",
        shouldCellUpdate: recordChanged,
        render: (_v, n) => <ActionsCell node={n} />,
      },
    ];
    return (
      <CellsContext.Provider value={cells}>
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
            onRow={(n) => {
              const bits = store.bits(n.id);
              // The selected band and the hovered band are painted on the cells (index.css), as antd paints a row's cells.
              const style = {
                ...((bits & BIT_CUT) !== 0 ? { opacity: CUT_OPACITY } : {}),
                // A row cannot take a text selection, as a tile cannot, so Shift+click does not leave text for Ctrl+C to copy.
                userSelect: "none" as const,
                scrollMarginTop,
              };
              return { ...rowAttrs(cells, n, bits), style: Object.keys(style).length ? style : undefined } as Record<string, unknown>;
            }}
          />
        </div>
      </CellsContext.Provider>
    );
  }

  // The tile view has two bands, the folders first and then the files, each a grid of its own with the same column widths, so
  // a folder row and a file row line up. The flat order (the page's ids) is the items' order; the folders come first in it.
  const folders = p.items.filter((n) => n.kind === "dir");
  const files = p.items.filter((n) => n.kind !== "dir");
  const band = { display: "grid", gridTemplateColumns: `repeat(auto-fill, minmax(min(100%, ${TILE_MIN_PX}px), 1fr))`, gap: 12 } as const;
  return (
    <CellsContext.Provider value={cells}>
      {/* The padding is the gutter a rubber band starts in, as the grid had before the bands. */}
      <div role="listbox" aria-label={p.label} aria-multiselectable="true" tabIndex={-1} ref={p.containerRef} style={{ display: "flex", flexDirection: "column", gap: 16, padding: 12, outline: "none" }}>
        {folders.length > 0 && (
          <div role="presentation" style={band}>
            {folders.map((n) => (
              <ChipTile key={n.id} node={n} bits={store.bits(n.id)} />
            ))}
          </div>
        )}
        {files.length > 0 && (
          <div role="presentation" style={band}>
            {files.map((n) => (
              <CardTile key={n.id} node={n} bits={store.bits(n.id)} />
            ))}
          </div>
        )}
      </div>
    </CellsContext.Provider>
  );
}

// thumbable is an image the server can thumbnail: the server sends has_thumbnail on those, and on no other item.
function thumbable(n: Node): boolean {
  return n.kind === "file" && n.has_thumbnail !== undefined;
}

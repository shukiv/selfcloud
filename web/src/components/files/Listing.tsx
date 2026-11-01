// SPDX-License-Identifier: AGPL-3.0-or-later

import {
  useEffect,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type ReactNode,
  type Ref,
} from "react";
import { Button, Checkbox, Grid, Input, Table, theme as antdTheme } from "antd";
import type { ColumnsType } from "antd/es/table";
import { ArrowDown, ArrowUp, Link2, Star } from "lucide-react";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import type { SortDir, SortKey } from "../../lib/files/sort";
import type { Node } from "../../lib/files/types";
import type { ViewMode } from "../../lib/files/viewPrefs";
import { isTextField } from "../../lib/files/keys";
import Icon from "./Icon";
import KindMarks from "./KindMarks";
import Thumb from "./Thumb";


// Click is what the page needs from a mouse or touch event: which modifiers were held, and whether a finger made it.
// A finger's tap opens the item; a mouse's click selects it, and its double click opens it (the page decides).
export interface Click {
  shiftKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
  touch?: boolean;
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
  // onStar toggles the caller's star on an item. Items without a star state show no star button.
  onStar?: (n: Node) => void;
  // onRowContext asks for the item's menu: a right-click, or a finger held on the item for a moment. It gets the pointer's
  // place in the window, so the menu opens there.
  onRowContext?: (id: string, index: number, x: number, y: number) => void;
  // cutIDs are the items a cut has taken: they are shown dimmed until they are pasted or the cut is cancelled.
  cutIDs?: Set<string>;
  containerRef?: Ref<HTMLDivElement>;
}

// LONG_PRESS_MS is how long a finger rests on an item before it opens the item's menu. PRESS_SLOP_PX is how far it may move
// meanwhile: a finger that moves is scrolling, not pressing.
const LONG_PRESS_MS = 500;
const PRESS_SLOP_PX = 10;
// CUT_OPACITY is how faint a cut item is shown, until it is pasted or the cut is cancelled.
const CUT_OPACITY = 0.5;
// SCROLL_MARGIN_TOP keeps a row or tile that the keyboard scrolls to clear of the sticky header (56 px) and the toolbar row
// (up to 52 px, with a 44 px touch target), so it is never hidden under them.
const SCROLL_MARGIN_TOP = 112;

type AriaSort = "ascending" | "descending" | "none";

function ariaSort(current: SortKey, dir: SortDir, key: SortKey): AriaSort {
  if (current !== key) return "none";
  return dir === "asc" ? "ascending" : "descending";
}

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

  // rowAttrs gives a row or a tile its role, name, selection state, roving focus and handlers. Both views use
  // it, so the keyboard model is the same in each.
  const rowAttrs = (n: Node, i: number) => ({
    role: "row",
    "aria-label": n.name,
    "aria-selected": p.selected.has(n.id),
    tabIndex: p.focusID === n.id ? 0 : -1,
    "data-id": n.id,
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
    onContextMenu: (e: ReactMouseEvent<HTMLElement>) => {
      // The browser's own menu stays in a field the person types in, and on a row being renamed.
      if (p.renameID === n.id || isTextField(e.target)) return;
      e.preventDefault();
      endPress();
      p.onRowContext?.(n.id, i, e.clientX, e.clientY);
    },
    onClick: (e: ReactMouseEvent<HTMLElement>) => {
      // The checkbox column has its own handler, so a click on it must not also select the row.
      if ((e.target as HTMLElement).closest(".ant-table-selection-column")) return;
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
      if ((e.target as HTMLElement).closest(".ant-table-selection-column") && !e.key.startsWith("Arrow")) return;
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

  const starMark = (n: Node) =>
    n.starred ? (
      <span style={{ display: "inline-flex", marginInlineStart: 4, color: token.colorWarning, verticalAlign: "middle" }}>
        <Star size={14} fill="currentColor" aria-hidden="true" focusable="false" />
        <span className="sr-only">{t("star.mark")}</span>
      </span>
    ) : null;

  // In a tile the star sits in the top right corner, a 44 pixel target: a tap on the tile's middle opens the file, not the star.
  const starButton = (n: Node, corner = false) =>
    n.starred !== undefined && p.onStar ? (
      <button
        type="button"
        aria-pressed={n.starred}
        aria-label={t("star.button", { name: n.name })}
        onClick={(e) => {
          e.stopPropagation();
          p.onStar?.(n);
        }}
        onKeyDown={(e) => e.stopPropagation()}
        onDoubleClick={(e) => e.stopPropagation()}
        style={{
          display: "inline-flex",
          alignItems: "center",
          justifyContent: "center",
          minWidth: 36,
          minHeight: 36,
          marginInlineStart: 4,
          padding: 0,
          color: n.starred ? token.colorWarning : token.colorTextSecondary,
          background: "transparent",
          border: 0,
          borderRadius: token.borderRadiusSM,
          cursor: "pointer",
          verticalAlign: "middle",
          ...(corner ? { position: "absolute", insetBlockStart: 0, insetInlineEnd: 0, minWidth: 44, minHeight: 44, marginInlineStart: 0 } : {}),
        }}
      >
        <Star size={16} fill={n.starred ? "currentColor" : "none"} aria-hidden="true" focusable="false" />
      </button>
    ) : null;

  const renameErr = (n: Node) =>
    p.renameID === n.id && p.renameError ? (
      <div id="rename-error" role="alert" style={{ flexBasis: "100%", color: token.colorError, fontSize: token.fontSizeSM }}>
        {p.renameError}
      </div>
    ) : null;

  // The active column shows its direction as an arrow after the label. The arrow is decorative (aria-hidden), so the button's
  // name stays the label alone; its box is always there, so the label does not move when the sort changes.
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

  if (p.view === "list") {
    const columns: ColumnsType<Node> = [
      {
        key: "name",
        ...sortHead("name", t("files.col.name")),
        render: (_v, n) => (
          <span style={{ display: "inline-flex", alignItems: "center", gap: 8, flexWrap: "wrap", minWidth: 0, maxWidth: "100%" }}>
            <Icon kind={kindOf(n)} size={22} colour={n.color} />
            {nameField(n)}
            {linkBadge(n)}
            <KindMarks node={n} />
            {starMark(n)}
            {starButton(n)}
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
              title: t("files.col.role"),
              width: 120,
              render: (_v: unknown, n: Node) => (
                <span style={{ color: token.colorTextSecondary, fontSize: token.fontSizeSM }}>{n.role ? t(`role.${n.role}`) : ""}</span>
              ),
            },
          ]),
    ];
    return (
      <div role="region" aria-label={p.label} ref={p.containerRef}>
        <Table<Node>
          rowKey="id"
          size="middle"
          pagination={false}
          columns={columns}
          dataSource={p.items}
          rowSelection={{
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
          }}
          onRow={(n, i) => {
            const sel = p.selected.has(n.id);
            const style = {
              ...(p.cutIDs?.has(n.id) ? { opacity: CUT_OPACITY } : {}),
              // A row cannot take a text selection, as a tile cannot, so Shift+click does not leave text for Ctrl+C to copy.
              userSelect: "none" as const,
              scrollMarginTop: SCROLL_MARGIN_TOP,
              // The selected row has an outline and a tint, as a selected tile does.
              ...(sel ? { background: token.colorPrimaryBg, outline: `2px solid ${token.colorPrimary}`, outlineOffset: -2 } : {}),
            };
            return { ...rowAttrs(n, i ?? 0), style: Object.keys(style).length ? style : undefined } as Record<string, unknown>;
          }}
        />
      </div>
    );
  }

  return (
    <div
      role="listbox"
      aria-label={p.label}
      aria-multiselectable="true"
      tabIndex={-1}
      ref={p.containerRef}



      style={{
        display: "grid",
        gridTemplateColumns: "repeat(auto-fill, minmax(144px, 1fr))",
        gap: 12,
        padding: 12,
        border: `1px solid ${token.colorBorderSecondary}`,
        borderRadius: token.borderRadiusLG,
        background: token.colorBgContainer,
        outline: "none",
      }}
    >
      {p.items.map((n, i) => {
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
              alignItems: "center",
              gap: 8,
              minHeight: 44,
              padding: 12,
              textAlign: "center",
              border: `1px solid ${isSel ? token.colorPrimary : token.colorBorderSecondary}`,
              borderRadius: token.borderRadiusLG,
              background: isSel ? token.colorPrimaryBg : undefined,
              // A selected tile has an outline as well as its tint, so the selection shows at a glance.
              outline: isSel ? `2px solid ${token.colorPrimary}` : undefined,
              outlineOffset: isSel ? -2 : undefined,
              cursor: "default",
              userSelect: "none",
              scrollMarginTop: SCROLL_MARGIN_TOP,
              opacity: p.cutIDs?.has(n.id) ? CUT_OPACITY : undefined,
            }}
          >
            {/* On a finger's screen a tile has no click that selects it, so its checkbox does. A list row has one already. */}
            {coarse && (
              // The wrapper stops the click: the checkbox toggles the tile, and the tile does not also open.
              <span onClick={(e) => e.stopPropagation()} style={{ position: "absolute", insetBlockStart: 6, insetInlineStart: 6 }}>
                <Checkbox
                  checked={isSel}
                  tabIndex={-1}
                  aria-label={t("files.select.item", { name: n.name })}
                  onChange={() => p.onRowClick(i, { shiftKey: false, ctrlKey: true, metaKey: false })}
                />
              </span>
            )}
            {thumbable(n) ? (
              <Thumb nod={n.id} version={n.sha256 ?? n.etag ?? ""} fallback={<Icon kind={kindOf(n)} size={40} colour={n.color} />} />
            ) : (
              <Icon kind={kindOf(n)} size={40} colour={n.color} />
            )}
            {p.renameID === n.id ? nameField(n) : <bdi style={{ overflowWrap: "anywhere" }}>{n.name}</bdi>}
            {linkBadge(n)}
            <KindMarks node={n} />
            {starMark(n)}
            {starButton(n, true)}
            <span style={{ color: token.colorTextSecondary, fontSize: token.fontSizeSM }}>{sizeOf(n, lang)}</span>
            {renameErr(n)}
          </div>
        );
      })}
    </div>
  );
}

// useCoarsePointer says whether the main pointer is a finger, so a screen without a mouse gets the checkboxes a mouse
// does without. It follows the media query, not the width of the window: a narrow desktop window keeps its mouse.
export function useCoarsePointer(): boolean {
  const supported = typeof window !== "undefined" && typeof window.matchMedia === "function";
  const [coarse, setCoarse] = useState(() => (supported ? window.matchMedia("(pointer: coarse)").matches : false));
  useEffect(() => {
    if (!supported) return;
    const mq = window.matchMedia("(pointer: coarse)");
    const onChange = () => setCoarse(mq.matches);
    mq.addEventListener("change", onChange);
    return () => mq.removeEventListener("change", onChange);
  }, [supported]);
  return coarse;
}

// thumbable is an image the server can thumbnail: the server sends has_thumbnail on those, and on no other item.
function thumbable(n: Node): boolean {
  return n.kind === "file" && n.has_thumbnail !== undefined;
}

// sizeOf is a file's size. A folder and a shortcut have none to show.
function sizeOf(n: Node, lang: string): string {
  return n.kind === "file" ? formatBytes(n.size, lang) : "";
}

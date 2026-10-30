// SPDX-License-Identifier: AGPL-3.0-or-later

import type { KeyboardEvent as ReactKeyboardEvent, MouseEvent as ReactMouseEvent, ReactNode, Ref } from "react";
import { Button, Grid, Input, Table, theme as antdTheme } from "antd";
import type { ColumnsType } from "antd/es/table";
import { Link2, Star } from "lucide-react";
import { useLang, useT } from "../../lib/i18n";
import { formatBytes, formatDate } from "../../lib/files/format";
import { kindOf } from "../../lib/files/kind";
import type { SortDir, SortKey } from "../../lib/files/sort";
import type { Node } from "../../lib/files/types";
import type { ViewMode } from "../../lib/files/viewPrefs";
import Icon from "./Icon";


// Click is what the page needs from a mouse event: which modifiers were held.
export interface Click {
  shiftKey: boolean;
  ctrlKey: boolean;
  metaKey: boolean;
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
  onRowDoubleClick: (index: number) => void;
  onSelectAll: (checked: boolean) => void;
  onSort: (key: SortKey) => void;
  onRenameChange: (value: string) => void;
  onRename: (commit: boolean) => void;
  onKeyDown: (e: ReactKeyboardEvent<HTMLElement>) => void;
  onFocusItem: (index: number) => void;
  // onStar toggles the caller's star on an item. Items without a star state show no star button.
  onStar?: (n: Node) => void;
  containerRef?: Ref<HTMLDivElement>;
}

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

  // rowAttrs gives a row or a tile its role, name, selection state, roving focus and handlers. Both views use
  // it, so the keyboard model is the same in each.
  const rowAttrs = (n: Node, i: number) => ({
    role: "row",
    "aria-label": n.name,
    "aria-selected": p.selected.has(n.id),
    tabIndex: p.focusID === n.id ? 0 : -1,
    "data-id": n.id,
    onClick: (e: ReactMouseEvent<HTMLElement>) => {
      // The checkbox column has its own handler, so a click on it must not also select the row.
      if ((e.target as HTMLElement).closest(".ant-table-selection-column")) return;
      p.onRowClick(i, e);
    },
    onDoubleClick: () => p.onRowDoubleClick(i),
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

  const starButton = (n: Node) =>
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

  const sortHead = (key: SortKey, label: string) => ({
    title: (
      <Button type="text" size="small" onClick={() => p.onSort(key)} style={{ fontWeight: 500, padding: 0 }}>
        {label}
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
            <Icon kind={kindOf(n)} size={22} />
            {nameField(n)}
            {linkBadge(n)}
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
          onRow={(n, i) => rowAttrs(n, i ?? 0) as Record<string, unknown>}
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
              cursor: "default",
              userSelect: "none",
            }}
          >
            <Icon kind={kindOf(n)} size={40} />
            {p.renameID === n.id ? nameField(n) : <bdi style={{ overflowWrap: "anywhere" }}>{n.name}</bdi>}
            {linkBadge(n)}
            {starMark(n)}
            {starButton(n)}
            <span style={{ color: token.colorTextSecondary, fontSize: token.fontSizeSM }}>{sizeOf(n, lang)}</span>
            {renameErr(n)}
          </div>
        );
      })}
    </div>
  );
}

function sizeOf(n: Node, lang: string): string {
  return n.kind === "dir" ? "" : formatBytes(n.size, lang);
}

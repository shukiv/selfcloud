// SPDX-License-Identifier: AGPL-3.0-or-later

// The visitor mode of the files listing (pb1): a link's visitor has no selection, no owner's data and no drag. The page's
// row icons are drawn on every row at once, so a phone reaches them without a hover.

import { render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { Download } from "lucide-react";
import type { Node } from "../../lib/files/types";
import Listing, { type ListingProps, type RowAct } from "./Listing";
import { desktopViewport } from "../../test/viewport";

function node(id: string, name: string, over: Partial<Node> = {}): Node {
  return {
    id,
    folder_id: "f1",
    parent_id: null,
    name,
    kind: "file",
    size: 2048,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
    starred: false,
    ...over,
  };
}

const ITEMS: Node[] = [
  node("n0", "Albums", { kind: "dir", mime_type: "" }),
  node("n1", "plan.txt"),
  node("n2", "notes.png", { mime_type: "image/png", has_link: true, starred: true, has_thumbnail: true }),
  node("n3", "clip.webm", { mime_type: "video/webm" }),
];

const rowActs = (n: Node): RowAct[] =>
  n.kind === "file"
    ? [{ key: "download", label: "Download", icon: <Download size={18} aria-hidden="true" focusable="false" />, enabled: true, run: vi.fn() }]
    : [];

function props(over: Partial<ListingProps> = {}): ListingProps {
  return {
    items: ITEMS,
    view: "list",
    selected: new Set(),
    focusID: null,
    sortKey: "name",
    sortDir: "asc",
    renameID: null,
    renameValue: "",
    renameError: null,
    label: "Shared folder",
    onRowClick: vi.fn(),
    onRowDoubleClick: vi.fn(),
    onSelectAll: vi.fn(),
    onSort: vi.fn(),
    onRenameChange: vi.fn(),
    onRename: vi.fn(),
    onKeyDown: vi.fn(),
    onFocusItem: vi.fn(),
    rowActs,
    ...over,
  };
}

const rowOf = (id: string) => document.querySelector(`[data-id="${id}"]`) as HTMLElement;

// coarseViewport is a wide window whose main pointer is a finger: the desktop columns with the touch rules.
function coarseViewport(): () => void {
  const before = window.matchMedia;
  window.matchMedia = ((query: string) => ({
    matches: query.includes("min-width") || query === "(pointer: coarse)",
    media: query,
    onchange: null,
    addListener() {},
    removeListener() {},
    addEventListener() {},
    removeEventListener() {},
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  return () => {
    window.matchMedia = before;
  };
}

let undo: (() => void) | null = null;
afterEach(() => {
  undo?.();
  undo = null;
  vi.clearAllMocks();
});

describe("Listing visitor: the list", () => {
  it("has no checkbox, no role column, no drop attributes and no More button", () => {
    undo = desktopViewport();
    const { container } = render(<Listing {...props()} visitor />);
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
    expect(screen.queryByRole("columnheader", { name: "Your access" })).toBeNull();
    expect(screen.getByRole("columnheader", { name: /Name/ })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: /Size/ })).toBeTruthy();
    expect(screen.getByRole("columnheader", { name: /Modified/ })).toBeTruthy();
    expect(container.querySelectorAll("[data-drop-folder], [data-drop-dir], [data-drop-role], [data-drop-name]")).toHaveLength(0);
    expect(screen.queryByRole("button", { name: /More actions/ })).toBeNull();
  });

  it("draws the page's row icon on every file row without a hover, and keeps the play mark", () => {
    undo = desktopViewport();
    render(<Listing {...props()} visitor />);
    expect(screen.getAllByRole("button", { name: "Download" })).toHaveLength(3);
    expect(within(rowOf("n0")).queryByRole("button", { name: "Download" })).toBeNull();
    expect(within(rowOf("n3")).getByTestId("play-mark")).toBeTruthy();
  });

  it("on a touch screen has no checkbox column and still draws the row icons", () => {
    undo = coarseViewport();
    const { container } = render(<Listing {...props()} visitor />);
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
    expect(container.querySelectorAll(".ant-table-selection-column")).toHaveLength(0);
    expect(screen.getAllByRole("button", { name: "Download" })).toHaveLength(3);
    expect(screen.queryByRole("button", { name: /More actions/ })).toBeNull();
  });

  it("a dragstart on a row is prevented: the browser's own drag of an image or a link is off for a visitor too", () => {
    undo = desktopViewport();
    render(<Listing {...props()} visitor />);
    const ev = new Event("dragstart", { bubbles: true, cancelable: true });
    rowOf("n1").dispatchEvent(ev);
    expect(ev.defaultPrevented).toBe(true);
  });
});

describe("Listing visitor: the tiles", () => {
  it("has no checkbox, no thumbnail, no star or link badge, and the row icons in the More button's place", () => {
    undo = desktopViewport();
    const { container } = render(<Listing {...props({ view: "grid" })} visitor />);
    expect(screen.getByRole("listbox").getAttribute("aria-multiselectable")).toBe("false");
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
    expect(rowOf("n2").querySelector("img")).toBeNull();
    expect(screen.queryByText("Starred")).toBeNull();
    expect(screen.queryByText("Has a link")).toBeNull();
    expect(screen.queryByRole("button", { name: /More actions/ })).toBeNull();
    expect(screen.getAllByRole("button", { name: "Download" })).toHaveLength(3);
    expect(within(rowOf("n2")).getByRole("button", { name: "Download" })).toBeTruthy();
    expect(container.querySelectorAll("[data-drop-folder], [data-drop-dir], [data-drop-role], [data-drop-name]")).toHaveLength(0);
  });

  it("on a touch screen has no tile checkbox", () => {
    undo = coarseViewport();
    render(<Listing {...props({ view: "grid" })} visitor />);
    expect(screen.queryAllByRole("checkbox")).toHaveLength(0);
    expect(screen.getAllByRole("button", { name: "Download" })).toHaveLength(3);
  });

  it("the focused tile's Download is a tab stop, and the other tiles' are not", () => {
    undo = desktopViewport();
    render(<Listing {...props({ view: "grid", focusID: "n2" })} visitor />);
    expect(within(rowOf("n2")).getByRole("button", { name: "Download" }).tabIndex).toBe(0);
    expect(within(rowOf("n1")).getByRole("button", { name: "Download" }).tabIndex).toBe(-1);
    expect(within(rowOf("n3")).getByRole("button", { name: "Download" }).tabIndex).toBe(-1);
  });
});

describe("Listing without visitor: today's behaviour", () => {
  it("keeps the role column, the More button and the folder's drop attributes", () => {
    undo = desktopViewport();
    render(<Listing {...props()} />);
    expect(screen.getByRole("columnheader", { name: "Your access" })).toBeTruthy();
    expect(screen.getAllByRole("button", { name: /More actions for/ }).length).toBeGreaterThan(0);
    expect(rowOf("n0").getAttribute("data-drop-dir")).toBe("n0");
    expect(screen.getAllByRole("checkbox").length).toBeGreaterThan(0);
  });
});

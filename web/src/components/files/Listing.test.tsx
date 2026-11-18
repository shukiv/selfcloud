// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { type Click, type ItemListingProps, type ListingProps, type RowAct } from "./Listing";

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
    role: "editor",
    no_download: false,
    editors_can_share: false,
    starred: false,
    ...over,
  };
}

function props(over: Partial<ListingProps> = {}): ListingProps {
  return {
    items: [node("n1", "plan.txt"), node("n2", "notes.txt", { has_link: true, starred: true })],
    view: "list",
    selected: new Set(),
    focusID: null,
    sortKey: "name",
    sortDir: "asc",
    renameID: null,
    renameValue: "",
    renameError: null,
    label: "Files",
    onRowClick: vi.fn(),
    onRowDoubleClick: vi.fn(),
    onSelectAll: vi.fn(),
    onSort: vi.fn(),
    onRenameChange: vi.fn(),
    onRename: vi.fn(),
    onKeyDown: vi.fn(),
    onFocusItem: vi.fn(),
    ...over,
  };
}

const rowOf = (id: string) => document.querySelector(`[data-id="${id}"]`) as HTMLElement;

// A starred item shows one star: the button where the person may star, and the mark only where no button is shown.
// The row's selection mark is a button with a pressed state too, and is not a star control.
const starControls = (el: HTMLElement) => ({
  buttons: el.querySelectorAll('button[aria-pressed]:not(.row-mark)').length,
  marks: Array.from(el.querySelectorAll(".sr-only")).filter((x) => x.textContent === "Starred").length,
});

// withStarButton is the listing as the page had it: an onStar handler, which made a star button on every starred item. The
// rule is that no star button is drawn after the name, whatever the handler.
const withStarButton = (over: Partial<ListingProps> = {}) => ({ ...props(over), onStar: vi.fn() }) as ListingProps;

describe("Listing: a starred item shows a filled star after its name, and no star button", () => {
  it("a list row shows the mark alone, not a star button", () => {
    render(<Listing {...withStarButton()} />);
    expect(starControls(rowOf("n2"))).toEqual({ buttons: 0, marks: 1 });
    expect(starControls(rowOf("n1"))).toEqual({ buttons: 0, marks: 0 });
  });

  it("a tile shows the mark alone, not a star button", () => {
    render(<Listing {...withStarButton({ view: "grid" })} />);
    expect(starControls(rowOf("n2"))).toEqual({ buttons: 0, marks: 1 });
    expect(starControls(rowOf("n1"))).toEqual({ buttons: 0, marks: 0 });
  });
});

describe("Listing: list view", () => {
  it("renders each item as a row with its name as the accessible name and its id", () => {
    render(<Listing {...props()} />);
    expect(rowOf("n1").getAttribute("role")).toBe("row");
    expect(rowOf("n1").getAttribute("aria-label")).toBe("plan.txt");
    expect(rowOf("n2").getAttribute("aria-label")).toBe("notes.txt");
    expect(screen.getByRole("row", { name: "plan.txt" })).toBeTruthy();
  });

  it("passes a plain click, a Ctrl click and a Shift click to onRowClick with their modifiers", () => {
    const p = props();
    render(<Listing {...p} />);
    const last = () => (p.onRowClick as ReturnType<typeof vi.fn>).mock.calls.at(-1) as [number, Click];
    const flags = (e: Click) => ({ shiftKey: e.shiftKey, ctrlKey: e.ctrlKey, metaKey: e.metaKey });
    fireEvent.click(rowOf("n1"));
    expect(last()[0]).toBe(0);
    expect(flags(last()[1])).toEqual({ shiftKey: false, ctrlKey: false, metaKey: false });
    fireEvent.click(rowOf("n2"), { ctrlKey: true });
    expect(last()[0]).toBe(1);
    expect(flags(last()[1])).toEqual({ shiftKey: false, ctrlKey: true, metaKey: false });
    fireEvent.click(rowOf("n2"), { shiftKey: true });
    expect(flags(last()[1])).toEqual({ shiftKey: true, ctrlKey: false, metaKey: false });
  });

  it("calls onSort from the header buttons, and marks the sorted column", () => {
    const p = props({ sortKey: "name", sortDir: "desc" });
    render(<Listing {...p} />);
    fireEvent.click(screen.getByRole("button", { name: "Size" }));
    expect(p.onSort).toHaveBeenLastCalledWith("size");
    fireEvent.click(screen.getByRole("button", { name: "Name" }));
    expect(p.onSort).toHaveBeenLastCalledWith("name");
    const nameHead = screen.getByRole("columnheader", { name: /Name/ });
    expect(nameHead.getAttribute("aria-sort")).toBe("descending");
  });

  // pending 266b: "Your access" is not sortable, so it was antd's bolder header text beside the sortable headers' 500.
  it("writes the plain header in the weight of the sortable headers", () => {
    // A wide window, so the columns past Name are shown (antd's breakpoints read min-width queries).
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query.includes("min-width"),
      media: query,
      addListener() {},
      removeListener() {},
      addEventListener() {},
      removeEventListener() {},
    }));
    try {
      render(<Listing {...props()} />);
      const size = screen.getByRole("button", { name: "Size" });
      const access = within(screen.getByRole("columnheader", { name: "Your access" })).getByText("Your access");
      expect(size.style.fontWeight).toBe("500");
      expect(access.style.fontWeight).toBe(size.style.fontWeight);
    } finally {
      vi.unstubAllGlobals();
    }
  });

  it("commits a rename on Enter and cancels on Escape, and the row's key handler does not run", () => {
    const p = props({ renameID: "n1", renameValue: "plan 2.txt" });
    render(<Listing {...p} />);
    const input = screen.getByRole("textbox", { name: "New name" });
    fireEvent.keyDown(input, { key: "Enter" });
    expect(p.onRename).toHaveBeenLastCalledWith(true);
    fireEvent.keyDown(input, { key: "Escape" });
    expect(p.onRename).toHaveBeenLastCalledWith(false);
    expect(p.onKeyDown).not.toHaveBeenCalled();
  });

  it("shows the link badge and the star mark with their hidden texts", () => {
    render(<Listing {...props()} />);
    const row = rowOf("n2");
    expect(within(row).getByText("Has a link")).toBeTruthy();
    expect(within(row).queryByRole("button", { name: "Star notes.txt" })).toBeNull();
    expect(within(rowOf("n1")).queryByText("Has a link")).toBeNull();
  });
});

describe("Listing: grid view", () => {
  it("renders the items as options in a listbox", () => {
    render(<Listing {...props({ view: "grid" })} />);
    const opts = screen.getAllByRole("option");
    expect(opts.length).toBe(2);
    expect(opts[0].getAttribute("aria-label")).toBe("plan.txt");
    expect(screen.getByRole("listbox", { name: "Files" })).toBeTruthy();
  });

  it("a tile has no star button: its star is the mark, and the star of the menu or the hover icon changes it", () => {
    render(<Listing {...withStarButton({ view: "grid" })} />);
    expect(screen.queryByRole("button", { name: "Star notes.txt" })).toBeNull();
  });
});

// The sort arrow: the sorted column shows its direction, the other columns keep an empty box of the same width, and the
// arrow is hidden from the header's name, which stays the label alone.
describe("the sort arrow on the list header", () => {
  const headerButtons = (c: HTMLElement) => Array.from(c.querySelectorAll<HTMLButtonElement>("thead button"));

  it("points up on the sorted column when the sort is ascending, and at no other column", () => {
    const { container } = render(<Listing {...props({ sortKey: "name", sortDir: "asc" })} />);
    const up = headerButtons(container).filter((b) => b.querySelector("svg.lucide-arrow-up"));
    expect(up.length).toBe(1);
    expect(up[0].textContent?.trim()).toBe("Name");
    expect(container.querySelectorAll("thead svg.lucide-arrow-down").length).toBe(0);
  });

  it("points down when the sort is descending", () => {
    const { container } = render(<Listing {...props({ sortKey: "size", sortDir: "desc" })} />);
    const down = headerButtons(container).filter((b) => b.querySelector("svg.lucide-arrow-down"));
    expect(down.length).toBe(1);
    expect(down[0].textContent?.trim()).toBe("Size");
    expect(container.querySelectorAll("thead svg.lucide-arrow-up").length).toBe(0);
  });

  it("keeps an empty box of the same width on every other column, so the labels do not move", () => {
    const { container } = render(<Listing {...props({ sortKey: "name", sortDir: "asc" })} />);
    const buttons = headerButtons(container);
    expect(buttons.length).toBeGreaterThan(1);
    for (const b of buttons) {
      const box = b.querySelector<HTMLElement>('span[aria-hidden="true"]');
      expect(box).not.toBeNull();
      expect(box?.style.width).toBe("14px");
    }
  });

  it("leaves the header's accessible name as the label alone", () => {
    render(<Listing {...props({ sortKey: "name", sortDir: "asc" })} />);
    expect(screen.getByRole("button", { name: "Name" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Size" })).toBeTruthy();
  });
});

describe("the list's column header (fu3b)", () => {
  // The header is in the same table as the rows, so the two share their columns in every frame. A second table (antd's sticky
  // holder) took its widths a frame later, and the first frame showed the header wrong.
  it("is one table with the rows: no sticky holder with a table of its own", () => {
    const { container } = render(<Listing {...props()} />);
    expect(container.querySelector(".ant-table-sticky-holder")).toBeNull();
    expect(container.querySelectorAll(".files-list table").length).toBe(1);
    const table = container.querySelector(".files-list table") as HTMLElement;
    expect(table.querySelector("thead th")).not.toBeNull();
    expect(table.querySelectorAll("tbody tr[data-id]").length).toBe(2);
  });

  // Item 3 (fu3b, decision 70's keyboard rules): a tile is one tab stop, its More button is not one, and an arrow key pressed on the
  // More button moves on as it does from the tile (only Enter and Space stop at the button).
  it("a tile is the one tab stop of its card: its More button is not one, and an arrow on it moves on", () => {
    const onKeyDown = vi.fn();
    const { container } = render(<Listing {...props({ view: "grid", focusID: "n1", onKeyDown })} />);
    const tile = container.querySelector('[data-id="n1"]') as HTMLElement;
    expect(tile.getAttribute("tabindex")).toBe("0");
    const more = tile.querySelector('button[aria-haspopup="menu"]') as HTMLElement;
    expect(more.getAttribute("tabindex")).toBe("-1");
    expect(tile.querySelectorAll('[tabindex="0"]').length).toBe(0);
    fireEvent.keyDown(more, { key: "ArrowRight" });
    expect(onKeyDown).toHaveBeenCalledWith(expect.objectContaining({ key: "ArrowRight" }));
  });

  it("a row's icon buttons keep the arrow keys for the list: an arrow on a row's More button reaches the list's handler", () => {
    const onKeyDown = vi.fn();
    const { container } = render(<Listing {...props({ focusID: "n1", onKeyDown })} />);
    const row = container.querySelector('tr[data-id="n1"]') as HTMLElement;
    const more = row.querySelector('button[aria-haspopup="menu"]') as HTMLElement;
    fireEvent.keyDown(more, { key: "ArrowDown" });
    expect(onKeyDown).toHaveBeenCalledWith(expect.objectContaining({ key: "ArrowDown" }));
  });

  it("gives its header cells the place under the toolbar as a variable, which the stylesheet pins to", () => {
    const { container } = render(<Listing {...props({ stickyTop: 120 })} />);
    const region = container.querySelector(".files-list") as HTMLElement;
    expect(region.style.getPropertyValue("--sc-list-top")).toBe("120px");
  });
});

describe("the table listing", () => {
  // Up to 150 loaded rows every row is drawn; above that the list draws a window (decision 183, Listing.window.test.tsx).
  it("renders every row of a folder of up to 150 rows, with no window that hides rows out of view", () => {
    const items = Array.from({ length: 150 }, (_, i) => node(`n${i}`, `file-${String(i).padStart(3, "0")}.txt`));
    const { container } = render(<Listing {...props({ items })} />);
    expect(container.querySelectorAll("tr[data-id]").length).toBe(150);
    expect(container.querySelector(".ant-table-tbody-virtual")).toBeNull();
  });

  it("does not hand Enter or Space from a row's mark to the row key handler", () => {
    const onKeyDown = vi.fn();
    render(<Listing {...props({ onKeyDown })} />);
    const row = screen.getByRole("row", { name: "plan.txt" });
    const mark = within(row).getByRole("button", { name: "Select plan.txt" });
    fireEvent.keyDown(mark, { key: "Enter" });
    fireEvent.keyDown(mark, { key: " " });
    expect(onKeyDown).not.toHaveBeenCalled();
  });

  it("still hands arrow keys from a row's mark to the list", () => {
    const onKeyDown = vi.fn();
    render(<Listing {...props({ onKeyDown })} />);
    const mark = within(screen.getByRole("row", { name: "plan.txt" })).getByRole("button", { name: "Select plan.txt" });
    fireEvent.keyDown(mark, { key: "ArrowDown" });
    expect(onKeyDown).toHaveBeenCalledTimes(1);
  });

  it("takes the row marks out of the tab order, and keeps the select-all mark in it", () => {
    render(<Listing {...props()} />);
    const row = screen.getByRole("row", { name: "plan.txt" });
    expect(within(row).getByRole("button", { name: "Select plan.txt" }).getAttribute("tabindex")).toBe("-1");
    // The select-all mark is the checkbox-role button in the header row, which has no data-id.
    const all = screen.getByRole("checkbox", { name: "Select all" });
    expect(all.closest("tr[data-id]")).toBeNull();
    expect(all.getAttribute("tabindex")).not.toBe("-1");
  });
});

describe("a mark click selects its row once", () => {
  it("passes one selection for a click on a row's mark, not one from the row as well, and keeps the rest", () => {
    const onRowClick = vi.fn();
    render(<Listing {...props({ onRowClick })} />);
    const mark = within(screen.getByRole("row", { name: "plan.txt" })).getByRole("button", { name: "Select plan.txt" });
    fireEvent.click(mark);
    expect(onRowClick).toHaveBeenCalledTimes(1);
    expect(onRowClick).toHaveBeenLastCalledWith(0, expect.objectContaining({ ctrlKey: true, shiftKey: false, metaKey: false }));
  });
});

describe("the row's mark is its icon", () => {
  it("wraps the file icon in the mark button, and no checkbox is drawn in the row or the header", () => {
    const { container } = render(<Listing {...props()} />);
    const row = rowOf("n1");
    const mark = row.querySelector("button.row-mark") as HTMLElement;
    expect(mark).not.toBeNull();
    expect(mark.querySelector("svg")).not.toBeNull();
    expect(row.querySelector(".ant-checkbox")).toBeNull();
    expect(container.querySelector(".ant-checkbox")).toBeNull();
    expect(container.querySelector(".ant-table-selection-column")).toBeNull();
  });

  it("a click on the icon inside the mark toggles once, and the row's own click does not also run", () => {
    const onRowClick = vi.fn();
    render(<Listing {...props({ onRowClick })} />);
    const icon = rowOf("n1").querySelector("button.row-mark svg") as HTMLElement;
    expect(icon).not.toBeNull();
    fireEvent.click(icon);
    expect(onRowClick).toHaveBeenCalledTimes(1);
  });

  it("the mark says its pressed state, and the row says its selection", () => {
    const { rerender } = render(<Listing {...props()} />);
    const mark = () => within(rowOf("n1")).getByRole("button", { name: "Select plan.txt" });
    expect(mark().getAttribute("aria-pressed")).toBe("false");
    expect(rowOf("n1").getAttribute("aria-selected")).toBe("false");
    rerender(<Listing {...props({ selected: new Set(["n1"]) })} />);
    expect(mark().getAttribute("aria-pressed")).toBe("true");
    expect(rowOf("n1").getAttribute("aria-selected")).toBe("true");
  });
});

// A double click opens the item the pointer was on: its ID is read from the row the event reached, so a list that
// has moved between the clicks cannot open a neighbour.
describe("a double click names the item it reached", () => {
  it("passes the row's ID, not its place, in both views", () => {
    const onRowDoubleClick = vi.fn();
    const { unmount } = render(<Listing {...props({ onRowDoubleClick })} />);
    fireEvent.doubleClick(rowOf("n2"));
    expect(onRowDoubleClick).toHaveBeenLastCalledWith("n2");
    unmount();
    render(<Listing {...props({ view: "grid", onRowDoubleClick })} />);
    fireEvent.doubleClick(rowOf("n1"));
    expect(onRowDoubleClick).toHaveBeenLastCalledWith("n1");
  });
});

// A finger's tap is a click the page reads as an open; a mouse's click is a selection. The kind of pointer is read from
// its own pointerdown, so a touch laptop follows each interaction.
describe("touch and mouse", () => {
  it("marks a click that a finger made, and not one a mouse made", () => {
    const onRowClick = vi.fn<(index: number, e: Click) => void>();
    render(<Listing {...props({ onRowClick })} />);
    fireEvent.pointerDown(rowOf("n2"), { pointerType: "touch" });
    fireEvent.click(rowOf("n2"));
    expect(onRowClick).toHaveBeenLastCalledWith(1, expect.objectContaining({ touch: true }));
    fireEvent.pointerDown(rowOf("n1"), { pointerType: "mouse" });
    fireEvent.click(rowOf("n1"));
    expect(onRowClick).toHaveBeenLastCalledWith(0, expect.objectContaining({ touch: false }));
  });

  it("does not open again on a finger's double tap, which opened the item on its first tap", () => {
    const onRowDoubleClick = vi.fn();
    render(<Listing {...props({ onRowDoubleClick })} />);
    fireEvent.pointerDown(rowOf("n1"), { pointerType: "touch" });
    fireEvent.doubleClick(rowOf("n1"));
    expect(onRowDoubleClick).not.toHaveBeenCalled();
  });

  it("gives a tile no checkbox on any pointer, and a finger's long press selects it", () => {
    vi.useFakeTimers();
    try {
      const stub = (coarse: boolean) =>
        vi.stubGlobal("matchMedia", (query: string) => ({ matches: coarse && query === "(pointer: coarse)", media: query, addEventListener() {}, removeEventListener() {} }));
      stub(true);
      const onRowClick = vi.fn<(index: number, e: Click) => void>();
      const { unmount } = render(<Listing {...props({ view: "grid", onRowClick })} />);
      expect(screen.queryByRole("checkbox", { name: "Select plan.txt" })).toBeNull();
      expect(document.querySelector(".ant-checkbox")).toBeNull();
      const tile = rowOf("n1");
      fireEvent.pointerDown(tile, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(onRowClick).toHaveBeenCalledTimes(1);
      expect(onRowClick).toHaveBeenLastCalledWith(0, expect.objectContaining({ ctrlKey: true }));
      // The tap that ends the long press is part of it: it selects nothing more.
      fireEvent.pointerUp(tile, { pointerType: "touch" });
      fireEvent.click(tile);
      expect(onRowClick).toHaveBeenCalledTimes(1);
      unmount();
      stub(false);
      render(<Listing {...props({ view: "grid" })} />);
      expect(document.querySelector(".ant-checkbox")).toBeNull();
      expect(screen.queryByRole("checkbox", { name: "Select plan.txt" })).toBeNull();
    } finally {
      vi.useRealTimers();
      vi.unstubAllGlobals();
    }
  });
});

// A rename field sits inside its row or tile. A tap or a double click that places the cursor in it is not a tap or a
// double click on the item, so it neither opens the item nor loses the rename.
describe("a rename field inside its row or tile", () => {
  for (const view of ["list", "grid"] as const) {
    it(`a tap in the field does not open the item (${view})`, () => {
      const onRowClick = vi.fn<(index: number, e: Click) => void>();
      render(<Listing {...props({ view, onRowClick, renameID: "n1", renameValue: "plan 2.txt" })} />);
      const input = screen.getByRole("textbox", { name: "New name" });
      fireEvent.pointerDown(input, { pointerType: "touch" });
      fireEvent.click(input);
      expect(onRowClick).not.toHaveBeenCalled();
    });

    it(`a double click in the field does not open the item (${view})`, () => {
      const onRowDoubleClick = vi.fn();
      render(<Listing {...props({ view, onRowDoubleClick, renameID: "n1", renameValue: "plan 2.txt" })} />);
      fireEvent.doubleClick(screen.getByRole("textbox", { name: "New name" }));
      expect(onRowDoubleClick).not.toHaveBeenCalled();
    });
  }
});

// The right-click menu and the cut state: the listing reports a right-click on a row and dims the cut items.
describe("the listing's right-click and cut state", () => {
  it("reports a right-click on a row with the row's index and the pointer's place, and takes the browser's menu away", () => {
    const onRowContext = vi.fn();
    render(<Listing {...props({ onRowContext } as Partial<ListingProps>)} />);
    const row = screen.getByRole("row", { name: "notes.txt" });
    const notPrevented = fireEvent.contextMenu(row, { clientX: 40, clientY: 90 });
    expect(onRowContext).toHaveBeenCalledWith("n2", 1, 40, 90);
    expect(notPrevented).toBe(false);
  });

  it("leaves a right-click in the rename field to the browser's own menu", () => {
    const onRowContext = vi.fn();
    render(<Listing {...props({ onRowContext, renameID: "n1", renameValue: "plan.txt" } as Partial<ListingProps>)} />);
    const field = screen.getByRole("textbox", { name: "New name" });
    const notPrevented = fireEvent.contextMenu(field, { clientX: 1, clientY: 1 });
    expect(onRowContext).not.toHaveBeenCalled();
    expect(notPrevented).toBe(true);
  });

  it("dims the items that are cut, in the list and in the tiles", () => {
    const cutIDs = new Set(["n1"]);
    const { unmount } = render(<Listing {...props({ cutIDs } as Partial<ListingProps>)} />);
    expect(screen.getByRole("row", { name: "plan.txt" }).style.opacity).toBe("0.5");
    expect(screen.getByRole("row", { name: "notes.txt" }).style.opacity).toBe("");
    unmount();
    render(<Listing {...props({ view: "grid", cutIDs } as Partial<ListingProps>)} />);
    expect(screen.getByRole("option", { name: "plan.txt" }).style.opacity).toBe("0.5");
  });

  it("a long press on a finger selects the row, and the tap that ends it opens nothing", () => {
    vi.useFakeTimers();
    try {
      const onRowContext = vi.fn();
      const onRowClick = vi.fn();
      render(<Listing {...props({ onRowContext, onRowClick } as Partial<ListingProps>)} />);
      const row = screen.getByRole("row", { name: "plan.txt" });
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 30, clientY: 60 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(onRowClick).toHaveBeenCalledTimes(1);
      expect(onRowClick).toHaveBeenLastCalledWith(0, expect.objectContaining({ ctrlKey: true }));
      expect(onRowContext).not.toHaveBeenCalled();
      fireEvent.click(row);
      expect(onRowClick).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("a finger that moves more than 10 pixels is scrolling, so nothing is selected; 10 pixels or less still holds the press", () => {
    vi.useFakeTimers();
    try {
      const onRowClick = vi.fn();
      render(<Listing {...props({ onRowClick } as Partial<ListingProps>)} />);
      const row = screen.getByRole("row", { name: "plan.txt" });
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 30, clientY: 60 });
      fireEvent.pointerMove(row, { pointerType: "touch", clientX: 30, clientY: 71 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(onRowClick).not.toHaveBeenCalled();
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 30, clientY: 60 });
      fireEvent.pointerMove(row, { pointerType: "touch", clientX: 30, clientY: 70 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(onRowClick).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("the mousedown after a long press is stopped before the window's later listeners", () => {
    vi.useFakeTimers();
    try {
      const onRowClick = vi.fn();
      render(<Listing {...props({ onRowClick } as Partial<ListingProps>)} />);
      const row = screen.getByRole("row", { name: "plan.txt" });
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 30, clientY: 60 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(onRowClick).toHaveBeenCalled();
      // A listener added after the listing, as the menu's outside-click listener is when the menu opens.
      const later = vi.fn();
      window.addEventListener("mousedown", later, true);
      try {
        fireEvent.mouseDown(row);
        expect(later).not.toHaveBeenCalled();
        // A new press begins with its pointerdown, and its mousedown is let through.
        fireEvent.pointerDown(row, { pointerType: "mouse" });
        fireEvent.mouseDown(row);
        expect(later).toHaveBeenCalledTimes(1);
      } finally {
        window.removeEventListener("mousedown", later, true);
      }
    } finally {
      vi.useRealTimers();
    }
  });
});

// The row end (decision 70): the ⋮ button is on every list row; the four icons (Share, Download, Rename, Star) come from the
// page's row actions, are drawn only where the pointer is a mouse, and are tab stops only in the focused row.
const rowAct = (key: string, label: string, run = vi.fn(), over: Partial<RowAct> = {}): RowAct => ({
  key,
  label,
  icon: <span />,
  enabled: true,
  run,
  ...over,
});
const rowActs = (n: Node) => (n.id === "n1" ? [rowAct("share", "Share"), rowAct("download", "Download"), rowAct("rename", "Rename"), rowAct("star", "Star")] : []);

const withPointer = (coarse: boolean) =>
  vi.stubGlobal("matchMedia", (query: string) => ({ matches: coarse && query === "(pointer: coarse)", media: query, addEventListener() {}, removeEventListener() {} }));

describe("the row end: the ⋮ button and the hover icons", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("every list row has a More actions button named for its item", () => {
    render(<Listing {...props()} />);
    expect(within(rowOf("n1")).getByRole("button", { name: "More actions for plan.txt" })).toBeTruthy();
    expect(within(rowOf("n2")).getByRole("button", { name: "More actions for notes.txt" })).toBeTruthy();
  });

  it("with a mouse a row renders its four icons, and each is named", () => {
    withPointer(false);
    render(<Listing {...props({ rowActs })} />);
    // The icons are drawn for a hovered row only: the mouse goes over n1 first.
    fireEvent.pointerEnter(rowOf("n1"), { pointerType: "mouse" });
    const row = rowOf("n1");
    for (const name of ["Share", "Download", "Rename", "Star"]) expect(within(row).getByRole("button", { name })).toBeTruthy();
    expect(within(rowOf("n2")).queryByRole("button", { name: "Rename" })).toBeNull();
  });

  it("on a finger the icons are not drawn, and the ⋮ button stays", () => {
    withPointer(true);
    render(<Listing {...props({ rowActs })} />);
    expect(within(rowOf("n1")).queryByRole("button", { name: "Rename" })).toBeNull();
    expect(within(rowOf("n1")).queryByRole("button", { name: "Star" })).toBeNull();
    expect(within(rowOf("n1")).getByRole("button", { name: "More actions for plan.txt" })).toBeTruthy();
  });

  it("an icon is a tab stop only in the focused row, and the others keep it out of the tab order", () => {
    withPointer(false);
    render(<Listing {...props({ rowActs, focusID: "n1" })} />);
    expect(within(rowOf("n1")).getByRole("button", { name: "Rename" }).getAttribute("tabindex")).toBe("0");
    expect(within(rowOf("n1")).getByRole("button", { name: "More actions for plan.txt" }).getAttribute("tabindex")).toBe("0");
    expect(within(rowOf("n2")).getByRole("button", { name: "More actions for notes.txt" }).getAttribute("tabindex")).toBe("-1");
  });

  it("an icon runs its action on its own row and selects nothing", () => {
    withPointer(false);
    const rename = vi.fn();
    const onRowClick = vi.fn();
    render(<Listing {...props({ rowActs: (n) => (n.id === "n1" ? [rowAct("rename", "Rename", rename)] : []), onRowClick })} />);
    fireEvent.pointerEnter(rowOf("n1"), { pointerType: "mouse" });
    fireEvent.click(within(rowOf("n1")).getByRole("button", { name: "Rename" }));
    expect(rename).toHaveBeenCalledTimes(1);
    expect(onRowClick).not.toHaveBeenCalled();
  });

  // A star icon is named for the action it does (Unstar on a starred item), and is not a toggle: no aria-pressed.
  it("a starred item's star icon is named for its action, with no pressed state, and an item with no action list shows none", () => {
    withPointer(false);
    const starred = (n: Node) => (n.id === "n2" ? [rowAct("star", "Unstar")] : []);
    render(<Listing {...props({ rowActs: starred })} />);
    fireEvent.pointerEnter(rowOf("n2"), { pointerType: "mouse" });
    const star = within(rowOf("n2")).getByRole("button", { name: "Unstar" });
    expect(star.hasAttribute("aria-pressed")).toBe(false);
    expect(within(rowOf("n1")).queryByRole("button", { name: /Star/ })).toBeNull();
  });

  // A row's four icons are drawn only for the row the mouse is on, the selected rows and the row with the focus; the other
  // rows keep only their ⋮.
  it("draws the icons only for a hovered, selected or focused row", () => {
    withPointer(false);
    render(<Listing {...props({ rowActs, focusID: null })} />);
    const iconsIn = (id: string) => within(rowOf(id)).queryAllByRole("button", { name: /^(Share|Download|Rename|Star|Unstar)$/ }).length;
    expect(iconsIn("n1")).toBe(0);
    expect(iconsIn("n2")).toBe(0);
    fireEvent.pointerEnter(rowOf("n1"), { pointerType: "mouse" });
    expect(iconsIn("n1")).toBe(4);
    expect(iconsIn("n2")).toBe(0);
    fireEvent.pointerLeave(rowOf("n1"), { pointerType: "mouse" });
    expect(iconsIn("n1")).toBe(0);
  });

  it("a selected row keeps its icons, and a row with the focus has them as tab stops", () => {
    withPointer(false);
    render(<Listing {...props({ rowActs, selected: new Set(["n1"]), focusID: "n1" })} />);
    expect(within(rowOf("n1")).queryAllByRole("button", { name: /^(Share|Download|Rename|Star|Unstar)$/ }).length).toBe(4);
    expect(within(rowOf("n1")).getByRole("button", { name: "Download" }).tabIndex).toBe(0);
  });

  // The menu opens at the ⋮'s start edge: its left in English, its right in Hebrew.
  it("the ⋮ menu opens at the button's start edge: its left in English and its right in Hebrew", () => {
    const onRowContext = vi.fn();
    render(<Listing {...props({ onRowContext })} />);
    const more = within(rowOf("n1")).getByRole("button", { name: "More actions for plan.txt" });
    more.getBoundingClientRect = () => ({ top: 40, bottom: 80, width: 40, height: 40, x: 100, y: 40, toJSON: () => ({}) }) as DOMRect;
    fireEvent.click(more, { detail: 1 });
    expect(onRowContext.mock.calls[0][2]).toBe(100);
    document.documentElement.dir = "rtl";
    try {
      fireEvent.click(more, { detail: 1 });
      expect(onRowContext.mock.calls[1][2]).toBe(140);
    } finally {
      document.documentElement.dir = "";
    }
  });

  // A key's click on the ⋮ passes the button as the origin the menu gives the focus back to; a mouse click does not.
  it("a key's click on the ⋮ passes the button as its keyboard origin, and a mouse click passes none", () => {
    const onRowContext = vi.fn();
    render(<Listing {...props({ onRowContext })} />);
    const more = within(rowOf("n1")).getByRole("button", { name: "More actions for plan.txt" });
    fireEvent.click(more, { detail: 0 });
    expect(onRowContext.mock.calls[0][4]).toBe(more);
    fireEvent.click(more, { detail: 1 });
    expect(onRowContext.mock.calls[1][4]).toBeUndefined();
  });

  it("the ⋮ button opens the item's menu at the button, and the row is not selected by it", () => {
    const onRowContext = vi.fn();
    const onRowClick = vi.fn();
    render(<Listing {...props({ onRowContext, onRowClick })} />);
    fireEvent.click(within(rowOf("n2")).getByRole("button", { name: "More actions for notes.txt" }));
    expect(onRowContext).toHaveBeenCalledTimes(1);
    expect(onRowContext.mock.calls[0][0]).toBe("n2");
    expect(onRowContext.mock.calls[0][1]).toBe(1);
    expect(onRowClick).not.toHaveBeenCalled();
  });
});

describe("the selection marks (the checkbox swap is gone)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("with a mouse the list has no checkbox column and no checkbox anywhere, and the row's mark is in its icon's place", () => {
    withPointer(false);
    const { container } = render(<Listing {...props()} />);
    expect(container.querySelector(".ant-table-selection-column")).toBeNull();
    expect(container.querySelector(".ant-checkbox")).toBeNull();
    const mark = within(rowOf("n1")).getByRole("button", { name: "Select plan.txt" });
    expect(mark.querySelector("svg")).not.toBeNull();
  });

  it("the list says whether anything is selected", () => {
    withPointer(false);
    const { container, rerender } = render(<Listing {...props()} />);
    expect(container.querySelector(".files-list")?.getAttribute("data-has-selection")).toBe("false");
    rerender(<Listing {...props({ selected: new Set(["n2"]) })} />);
    expect(container.querySelector(".files-list")?.getAttribute("data-has-selection")).toBe("true");
  });

  it("the select-all mark is always in the name's header, reads false, mixed and true, and selects all then clears", () => {
    withPointer(false);
    const onSelectAll = vi.fn();
    const { rerender } = render(<Listing {...props({ onSelectAll })} />);
    const all = () => screen.getByRole("checkbox", { name: "Select all" });
    expect(all().closest("thead")).not.toBeNull();
    expect(all().getAttribute("aria-checked")).toBe("false");
    fireEvent.click(all());
    expect(onSelectAll).toHaveBeenLastCalledWith(true);
    rerender(<Listing {...props({ onSelectAll, selected: new Set(["n1"]) })} />);
    expect(all().getAttribute("aria-checked")).toBe("mixed");
    rerender(<Listing {...props({ onSelectAll, selected: new Set(["n1", "n2"]) })} />);
    expect(all().getAttribute("aria-checked")).toBe("true");
    fireEvent.click(all());
    expect(onSelectAll).toHaveBeenLastCalledWith(false);
  });

  it("on a finger the list has no checkbox column either, and the select-all mark stays in the header", () => {
    withPointer(true);
    const { container } = render(<Listing {...props()} />);
    expect(container.querySelector(".ant-table-selection-column")).toBeNull();
    expect(container.querySelector(".ant-checkbox")).toBeNull();
    expect(screen.getByRole("checkbox", { name: "Select all" }).closest("thead")).not.toBeNull();
  });

  it("the badge is drawn only on a selected row, on its icon, and says nothing to a screen reader", () => {
    withPointer(false);
    const { rerender } = render(<Listing {...props()} />);
    expect(rowOf("n1").querySelector(".check-badge")).toBeNull();
    expect(rowOf("n2").querySelector(".check-badge")).toBeNull();
    rerender(<Listing {...props({ selected: new Set(["n1"]) })} />);
    const badge = rowOf("n1").querySelector(".check-badge") as HTMLElement;
    expect(badge).not.toBeNull();
    expect(badge.getAttribute("aria-hidden")).toBe("true");
    expect(getComputedStyle(badge).backgroundColor).toBe("rgb(254, 204, 0)");
    expect(badge.querySelector("svg")).not.toBeNull();
    // The icon stays, and the other row has no badge.
    expect(rowOf("n1").querySelector("button.row-mark svg")).not.toBeNull();
    expect(rowOf("n2").querySelector(".check-badge")).toBeNull();
  });

  it("the badge is drawn only on a selected tile, and no tile has a checkbox", () => {
    const { rerender } = render(<Listing {...props({ view: "grid" })} />);
    expect(rowOf("n1").querySelector(".check-badge")).toBeNull();
    rerender(<Listing {...props({ view: "grid", selected: new Set(["n1"]) })} />);
    expect(rowOf("n1").querySelector(".check-badge")).not.toBeNull();
    expect(rowOf("n2").querySelector(".check-badge")).toBeNull();
    expect(document.querySelector(".ant-checkbox")).toBeNull();
  });

  it("a hover changes nothing on the icon: no checkbox appears and the icon stays", () => {
    withPointer(false);
    const { container } = render(<Listing {...props()} />);
    fireEvent.pointerEnter(rowOf("n1"), { pointerType: "mouse" });
    expect(rowOf("n1").querySelector(".ant-checkbox")).toBeNull();
    expect(rowOf("n1").querySelector("button.row-mark svg")).not.toBeNull();
    expect(rowOf("n1").querySelector(".check-badge")).toBeNull();
    expect(container.querySelector(".ant-checkbox")).toBeNull();
  });
});

// The files preset's actions column has a name for a screen reader, shown to no one.
describe("the files preset's actions column", () => {
  it("has a visually hidden header name", () => {
    const { container } = render(<Listing {...props()} />);
    const heads = [...container.querySelectorAll("thead th")];
    const last = heads[heads.length - 1];
    expect(last.textContent).toBe("Actions");
    expect(last.querySelector(".sr-only")?.textContent).toBe("Actions");
  });
});

// A preset list: Listing with columns from its caller. It is the same component as My files, with the same
// frame: the region, antd's table at the middle size, the header cell, the row end and the band variables, under the shared
// CSS group (.files-list, .item-list). Its tests set the viewport themselves: the default mock is narrow.
type Row = { id: string; name: string; email: string; status: string; when: string };
const people: Row[] = [
  { id: "r1", name: "Acme Studio", email: "acme@example.com", status: "Active", when: "Mar 3, 2026" },
  { id: "r2", name: "Beta Clinic", email: "beta@example.com", status: "Suspended", when: "Jun 18, 2026" },
  { id: "r3", name: "Cedar Pine", email: "cedar@example.com", status: "Active", when: "Jan 9, 2026" },
];
const viewport = (px: number, coarse = false) =>
  vi.stubGlobal("matchMedia", (query: string) => {
    const m = /min-width: (\d+)px/.exec(query);
    const matches = m ? px >= Number(m[1]) : coarse && query === "(pointer: coarse)";
    return { matches, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
  });
const itemProps = (over: Partial<ItemListingProps<Row>> = {}): ItemListingProps<Row> => ({
  items: people,
  label: "Tenants",
  rowLabel: (r) => r.name,
  moreLabel: (r) => `More for ${r.name}`,
  columns: [
    { key: "name", title: "Name", render: (r) => <span>{r.name}</span>, sortKey: "name" },
    { key: "email", title: "Email", render: (r) => r.email, secondary: true },
    { key: "status", title: "Status", render: (r) => r.status },
    { key: "when", title: "Created", render: (r) => r.when, secondary: true, ariaSort: "descending" },
  ],
  empty: { title: "Nothing here" },
  ...over,
});
const itemRow = (name: string) => document.querySelector(`tbody tr[aria-label="${name}"]`) as HTMLElement;
const act4 = (over: Partial<{ disabled: boolean }> = {}) => [
  { key: "a", label: "Act A", icon: <span>a</span>, run: vi.fn(), ...over },
  { key: "b", label: "Act B", icon: <span>b</span>, run: vi.fn() },
  { key: "c", label: "Act C", icon: <span>c</span>, run: vi.fn() },
  { key: "d", label: "Act D", icon: <span>d</span>, run: vi.fn() },
];

describe("Listing with columns: a preset list in the one table", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("renders the shared frame: a region of class item-list (never files-list) with the list's band variables, and labelled rows", () => {
    viewport(1200);
    const { container } = render(<Listing {...itemProps()} />);
    const region = screen.getByRole("region", { name: "Tenants" });
    expect(region.classList.contains("item-list")).toBe(true);
    expect(region.classList.contains("files-list")).toBe(false);
    // The shared CSS reads these variables: without them the header tint and the hover band paint nothing.
    for (const v of ["--sc-row-hover", "--sc-row-selected", "--sc-list-head", "--sc-list-head-tint", "--sc-list-top"]) {
      expect(region.style.getPropertyValue(v), v).not.toBe("");
    }
    // Under the shell's header (56 px): a preset list has no toolbar row to pin under.
    expect(region.style.getPropertyValue("--sc-list-top")).toBe("56px");
    expect(container.querySelector(".ant-table-middle")).not.toBeNull();
    expect(container.querySelectorAll('tbody tr[role="row"][aria-label]').length).toBe(3);
    expect(screen.getByRole("columnheader", { name: "Status" })).toBeTruthy();
  });

  it("shows a secondary column as its own column on a desktop, and as the name's second line on a phone", () => {
    viewport(1200);
    const { unmount } = render(<Listing {...itemProps()} />);
    expect(screen.getByRole("columnheader", { name: "Email" })).toBeTruthy();
    expect(document.querySelector(".row-second")).toBeNull();
    unmount();
    viewport(375);
    render(<Listing {...itemProps()} />);
    expect(screen.queryByRole("columnheader", { name: "Email" })).toBeNull();
    const line = itemRow("Acme Studio").querySelector(".row-second");
    // The cells are inline items of one wrapping row; the separator between them is CSS, not a text node (mob4).
    expect([...(line?.querySelectorAll(".row-second-item") ?? [])].map((c) => c.textContent)).toEqual(["acme@example.com", "Mar 3, 2026"]);
    expect(line?.textContent).toBe("acme@example.comMar 3, 2026");
    expect(line?.textContent).not.toContain("·");
  });

  it("a sortable header is a button that reports its key, and carries aria-sort on its th", () => {
    viewport(1200);
    const onSort = vi.fn();
    render(<Listing {...itemProps({ sort: { key: "name", dir: "asc", onSort } })} />);
    const head = screen.getByRole("columnheader", { name: /Name/ });
    expect(head.getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(within(head).getByRole("button", { name: "Name" }));
    expect(onSort).toHaveBeenCalledWith("name");
  });

  it("a time column with no sort has aria-sort descending and no button", () => {
    viewport(1200);
    render(<Listing {...itemProps({ sort: { key: "name", dir: "asc", onSort: vi.fn() } })} />);
    const when = screen.getByRole("columnheader", { name: "Created" });
    expect(when.getAttribute("aria-sort")).toBe("descending");
    expect(within(when).queryByRole("button")).toBeNull();
  });

  it("shows up to three inline icons on a desktop, sends the rest to the ⋮ menu, and names the actions column", () => {
    viewport(1200);
    render(<Listing {...itemProps({ actions: () => act4(), menu: () => [{ key: "x", label: "Suspend", onSelect: vi.fn() }] })} />);
    expect(within(itemRow("Acme Studio")).getAllByRole("button", { name: /^Act [ABC]$/ })).toHaveLength(3);
    expect(screen.queryByRole("button", { name: /Act D/ })).toBeNull();
    const heads = screen.getAllByRole("columnheader");
    expect(heads[heads.length - 1].textContent).toBe("Actions");
    fireEvent.click(within(itemRow("Acme Studio")).getByRole("button", { name: "More for Acme Studio" }));
    expect(screen.getByRole("menuitem", { name: /Act D/ })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: /Suspend/ })).toBeTruthy();
  });

  it("a list with no actions and no menu has no actions column", () => {
    viewport(1200);
    render(<Listing {...itemProps()} />);
    expect(screen.getAllByRole("columnheader").map((h) => h.textContent)).not.toContain("Actions");
  });

  it("on a phone shows the ⋮ menu alone, and its entries run", () => {
    viewport(375);
    const onSelect = vi.fn();
    const run = vi.fn();
    render(<Listing {...itemProps({ actions: () => [{ key: "a", label: "Act A", icon: <span>a</span>, run }], menu: () => [{ key: "x", label: "Suspend", onSelect }] })} />);
    expect(screen.queryByRole("button", { name: /Act A/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "More for Acme Studio" }));
    fireEvent.click(screen.getByText("Suspend"));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it("on a finger the actions are in the ⋮ menu too, whatever the width", () => {
    viewport(1200, true);
    render(<Listing {...itemProps({ actions: () => [{ key: "a", label: "Act A", icon: <span>a</span>, run: vi.fn() }] })} />);
    expect(screen.queryByRole("button", { name: /Act A/ })).toBeNull();
    expect(screen.getAllByRole("button", { name: /^More for/ })).toHaveLength(3);
  });

  // A disabled action keeps its place. It is a disabled icon in the row, and a disabled entry in the ⋮ menu.
  // The re-check: a real browser takes the focus from a button that turns disabled (the e2e found BODY focused), so the
  // icon is aria-disabled, still focusable, and its click and its keys do nothing.
  it("a disabled action stays in its place, aria-disabled and focusable, inline and in the ⋮ menu", () => {
    viewport(1200);
    const acts = act4({ disabled: true });
    const { unmount } = render(<Listing {...itemProps({ actions: () => acts })} />);
    const a = within(itemRow("Acme Studio")).getByRole("button", { name: "Act A" }) as HTMLButtonElement;
    expect(a.getAttribute("aria-disabled")).toBe("true");
    expect(a.disabled).toBe(false);
    const order = within(itemRow("Acme Studio")).getAllByRole("button").map((b) => b.getAttribute("aria-label"));
    expect(order.slice(0, 3)).toEqual(["Act A", "Act B", "Act C"]);
    fireEvent.click(a);
    expect(acts[0].run).not.toHaveBeenCalled();
    unmount();
    viewport(375);
    render(<Listing {...itemProps({ actions: () => acts })} />);
    fireEvent.click(screen.getByRole("button", { name: "More for Acme Studio" }));
    const entry = screen.getByRole("menuitem", { name: /Act A/ });
    expect(entry.getAttribute("aria-disabled")).toBe("true");
  });

  // An entry with an address is a real link, so a middle or modified click opens it in a new tab.
  it("a ⋮ entry with an address is a link: a plain click runs the entry and keeps the page, a modified click is the browser's", () => {
    viewport(1200);
    const onSelect = vi.fn();
    render(<Listing {...itemProps({ menu: (r) => [{ key: "open", label: "Open", href: `/admin/tenants/${r.id}`, onSelect }] })} />);
    fireEvent.click(within(itemRow("Acme Studio")).getByRole("button", { name: "More for Acme Studio" }));
    const link = screen.getByRole("link", { name: "Open" });
    expect(link.getAttribute("href")).toBe("/admin/tenants/r1");
    expect(fireEvent.click(link, { ctrlKey: true })).toBe(true);
    expect(onSelect).not.toHaveBeenCalled();
    expect(fireEvent.click(link)).toBe(false);
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  // The owner's rule of 2026-11-09 (decision 114): an empty list keeps its table and its header row, with the
  // column titles and the sort mark; inside the body, antd's simple Empty, a bold title, one muted line, and the page's create
  // action, a primary button with a + icon.
  it("an empty list keeps its header row, and shows the Empty with its title, description and action inside the table body", () => {
    viewport(1200);
    const onClick = vi.fn();
    const { container } = render(
      <Listing
        {...itemProps({
          items: [],
          sort: { key: "name", dir: "asc", onSort: vi.fn() },
          empty: { title: "No tenants yet", description: "A tenant is an organisation.", action: { label: "New tenant", onClick } },
        })}
      />,
    );
    const table = container.querySelector(".item-list table") as HTMLElement;
    expect(within(table).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Email", "Status", "Created"]);
    expect(within(table).getByRole("columnheader", { name: "Name" }).getAttribute("aria-sort")).toBe("ascending");
    const body = container.querySelector(".item-list tbody") as HTMLElement;
    const empty = within(body).getByRole("status");
    expect(empty.querySelector(".ant-empty.ant-empty-normal .ant-empty-image svg")).not.toBeNull();
    const title = empty.querySelector(".empty-title") as HTMLElement;
    expect(title.textContent).toBe("No tenants yet");
    expect(title.style.fontWeight).toBe("600");
    expect((empty.querySelector(".empty-desc") as HTMLElement).textContent).toBe("A tenant is an organisation.");
    const button = within(empty).getByRole("button", { name: "New tenant" });
    expect(button.className).toContain("ant-btn-primary");
    expect(button.querySelector("svg.lucide-plus")).not.toBeNull();
    fireEvent.click(button);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("an empty list without a description or an action shows the title alone", () => {
    viewport(1200);
    const { container } = render(<Listing {...itemProps({ items: [], empty: { title: "Nothing recorded yet" } })} />);
    const body = container.querySelector(".item-list tbody") as HTMLElement;
    expect((body.querySelector(".empty-title") as HTMLElement).textContent).toBe("Nothing recorded yet");
    expect(body.querySelector(".empty-desc")).toBeNull();
    expect(within(body).queryByRole("button")).toBeNull();
  });

  it("Up and Down move the focus between rows, the focused row is the one tab stop, and Enter opens it", () => {
    viewport(1200);
    const onOpen = vi.fn();
    render(<Listing {...itemProps({ onOpen })} />);
    const [r1, r2, r3] = ["Acme Studio", "Beta Clinic", "Cedar Pine"].map(itemRow);
    expect([r1.tabIndex, r2.tabIndex, r3.tabIndex]).toEqual([0, -1, -1]);
    act(() => r1.focus());
    fireEvent.keyDown(r1, { key: "ArrowDown" });
    expect(document.activeElement).toBe(r2);
    expect([r1.tabIndex, r2.tabIndex]).toEqual([-1, 0]);
    fireEvent.keyDown(r2, { key: "End" });
    expect(document.activeElement).toBe(r3);
    fireEvent.keyDown(r3, { key: "Enter" });
    expect(onOpen).toHaveBeenCalledWith(people[2]);
  });

  it("a click on a row of a list that opens opens it; a click on a control in the row does not", () => {
    viewport(1200);
    const onOpen = vi.fn();
    const run = vi.fn();
    render(<Listing {...itemProps({ onOpen, actions: () => [{ key: "a", label: "Act A", icon: <span>a</span>, run }] })} />);
    fireEvent.click(within(itemRow("Beta Clinic")).getByRole("button", { name: "Act A" }));
    expect(run).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
    fireEvent.click(itemRow("Beta Clinic").querySelector("td") as HTMLElement);
    expect(onOpen).toHaveBeenCalledWith(people[1]);
  });

  // The ⋮ menu is a portal outside the row, but React passes its clicks up to the row: an entry's click is the entry's alone.
  it("a click on a ⋮ entry runs the entry and does not open the row", () => {
    viewport(1200);
    const onOpen = vi.fn();
    const onSelect = vi.fn();
    render(<Listing {...itemProps({ onOpen, menu: () => [{ key: "x", label: "Suspend", onSelect }] })} />);
    fireEvent.click(within(itemRow("Beta Clinic")).getByRole("button", { name: "More for Beta Clinic" }));
    fireEvent.click(screen.getByText("Suspend"));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("a list without selectable has no checkboxes, no marks and no aria-selected", () => {
    viewport(1200);
    const { container } = render(<Listing {...itemProps()} />);
    expect(container.querySelectorAll('input[type="checkbox"]').length).toBe(0);
    expect(container.querySelector(".row-mark")).toBeNull();
    expect(screen.queryByRole("checkbox")).toBeNull();
    expect(itemRow("Acme Studio").hasAttribute("aria-selected")).toBe(false);
  });

  it("a selectable list: Space toggles a row, Shift with Down extends, the bar shows the count and the page's icons, Escape clears", () => {
    viewport(1200);
    const unstar = vi.fn();
    render(<Listing {...itemProps({ selectable: true, bulk: (ids) => [{ key: "unstar", label: "Remove from Starred", icon: <span>u</span>, enabled: true, run: () => unstar(ids) }] })} />);
    const [r1, r2] = ["Acme Studio", "Beta Clinic"].map(itemRow);
    expect(r1.getAttribute("aria-selected")).toBe("false");
    act(() => r1.focus());
    fireEvent.keyDown(r1, { key: " " });
    expect(itemRow("Acme Studio").getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(r1, { key: "ArrowDown", shiftKey: true });
    expect(itemRow("Beta Clinic").getAttribute("aria-selected")).toBe("true");
    expect(document.activeElement).toBe(r2);
    const bar = screen.getByTestId("selection-panel");
    expect(bar.textContent).toContain("2 selected");
    fireEvent.click(within(bar).getByRole("button", { name: "Remove from Starred" }));
    expect(unstar).toHaveBeenCalledWith(["r1", "r2"]);
    fireEvent.keyDown(r2, { key: "Escape" });
    expect(screen.queryByTestId("selection-panel")).toBeNull();
    expect(itemRow("Acme Studio").getAttribute("aria-selected")).toBe("false");
  });

  it("a selectable list: a click selects the row, Ctrl adds one, and the mouse's mark toggles it", () => {
    viewport(1200);
    render(<Listing {...itemProps({ selectable: true })} />);
    fireEvent.click(itemRow("Acme Studio").querySelector("td") as HTMLElement);
    fireEvent.click(itemRow("Cedar Pine").querySelector("td") as HTMLElement, { ctrlKey: true });
    expect(["Acme Studio", "Beta Clinic", "Cedar Pine"].map((n) => itemRow(n).getAttribute("aria-selected"))).toEqual(["true", "false", "true"]);
    fireEvent.click(screen.getByRole("button", { name: "Select Beta Clinic" }));
    expect(itemRow("Beta Clinic").getAttribute("aria-selected")).toBe("true");
    // The mark is out of the tab order, and no checkbox is drawn anywhere.
    expect(screen.getByRole("button", { name: "Select Beta Clinic" }).getAttribute("tabindex")).toBe("-1");
    expect(document.querySelector(".ant-checkbox")).toBeNull();
  });

  it("a selectable preset list has the select-all mark in its header, reading false, mixed and true", () => {
    viewport(1200);
    render(<Listing {...itemProps({ selectable: true })} />);
    const all = () => screen.getByRole("checkbox", { name: "Select all" });
    expect(all().closest("thead")).not.toBeNull();
    expect(all().getAttribute("aria-checked")).toBe("false");
    fireEvent.click(itemRow("Acme Studio"));
    expect(all().getAttribute("aria-checked")).toBe("mixed");
    fireEvent.click(all());
    expect(all().getAttribute("aria-checked")).toBe("true");
    fireEvent.click(all());
    expect(all().getAttribute("aria-checked")).toBe("false");
  });

  it("a selectable preset list on a finger: a long press toggles the row, and a tap while something is selected toggles too", () => {
    vi.useFakeTimers();
    try {
      viewport(1200, true);
      render(<Listing {...itemProps({ selectable: true })} />);
      expect(document.querySelector(".ant-checkbox")).toBeNull();
      const row = itemRow("Acme Studio");
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(itemRow("Acme Studio").getAttribute("aria-selected")).toBe("true");
      fireEvent.pointerUp(row, { pointerType: "touch" });
      fireEvent.click(row);
      expect(itemRow("Acme Studio").getAttribute("aria-selected")).toBe("true");
      // With something selected, a plain tap on another row toggles it in.
      fireEvent.click(itemRow("Beta Clinic"));
      expect(itemRow("Beta Clinic").getAttribute("aria-selected")).toBe("true");
      // A long press on a selected row takes it out again.
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(itemRow("Acme Studio").getAttribute("aria-selected")).toBe("false");
    } finally {
      vi.useRealTimers();
    }
  });

  it("each row carries its label as aria-label", () => {
    viewport(1200);
    const { container } = render(<Listing {...itemProps()} />);
    expect(container.querySelector('tr[aria-label="Acme Studio"]')).not.toBeNull();
  });
});

// decision 114: a preset action can be the open one of its list (Groups' Members for the open group). Its icon
// button says so with aria-pressed; an action that is not pressed has no aria-pressed at all.
describe("a preset action's pressed state", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("an action with pressed is aria-pressed on its icon button, and one without has no aria-pressed", () => {
    viewport(1200);
    const actions = () => [
      { key: "m", label: "Members: Ops", icon: <span>m</span>, run: vi.fn(), pressed: true },
      { key: "r", label: "Rename: Ops", icon: <span>r</span>, run: vi.fn() },
    ];
    render(<Listing {...itemProps({ actions })} />);
    const row = itemRow("Acme Studio");
    expect(within(row).getByRole("button", { name: "Members: Ops" }).getAttribute("aria-pressed")).toBe("true");
    expect(within(row).getByRole("button", { name: "Rename: Ops" }).hasAttribute("aria-pressed")).toBe(false);
  });
});

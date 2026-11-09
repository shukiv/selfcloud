// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { ItemList, type Click, type ItemListProps, type ListingProps, type RowAct } from "./Listing";

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
const starControls = (el: HTMLElement) => ({
  buttons: el.querySelectorAll('button[aria-pressed]').length,
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

describe("the list's column header (fu3b, item 1)", () => {
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
  it("renders every row of a long folder, with no window that hides rows out of view", () => {
    const items = Array.from({ length: 250 }, (_, i) => node(`n${i}`, `file-${String(i).padStart(3, "0")}.txt`));
    const { container } = render(<Listing {...props({ items })} />);
    expect(container.querySelectorAll("tr[data-id]").length).toBe(250);
    expect(container.querySelector(".ant-table-tbody-virtual")).toBeNull();
  });

  it("does not hand Enter or Space from a row's checkbox to the row key handler", () => {
    const onKeyDown = vi.fn();
    render(<Listing {...props({ onKeyDown })} />);
    const row = screen.getByRole("row", { name: "plan.txt" });
    const box = within(row).getByRole("checkbox");
    fireEvent.keyDown(box, { key: "Enter" });
    fireEvent.keyDown(box, { key: " " });
    expect(onKeyDown).not.toHaveBeenCalled();
  });

  it("still hands arrow keys from a row's checkbox to the list", () => {
    const onKeyDown = vi.fn();
    render(<Listing {...props({ onKeyDown })} />);
    const box = within(screen.getByRole("row", { name: "plan.txt" })).getByRole("checkbox");
    fireEvent.keyDown(box, { key: "ArrowDown" });
    expect(onKeyDown).toHaveBeenCalledTimes(1);
  });

  it("takes the row checkboxes out of the tab order, and keeps the select-all box in it", () => {
    render(<Listing {...props()} />);
    const row = screen.getByRole("row", { name: "plan.txt" });
    expect(within(row).getByRole("checkbox").getAttribute("tabindex")).toBe("-1");
    // The select-all box is the checkbox in the header row, which has no data-id.
    const all = screen.getAllByRole("checkbox").find((c) => !c.closest("tr[data-id]")) as HTMLInputElement | undefined;
    expect(all).toBeDefined();
    expect(all?.getAttribute("tabindex")).not.toBe("-1");
  });
});

describe("a checkbox click selects its row once", () => {
  it("passes one selection for a click on a row's checkbox, not one from the row as well", () => {
    const onRowClick = vi.fn();
    render(<Listing {...props({ onRowClick })} />);
    const box = within(screen.getByRole("row", { name: "plan.txt" })).getByRole("checkbox");
    fireEvent.click(box);
    expect(onRowClick).toHaveBeenCalledTimes(1);
  });
});

describe("the selection cell outside its checkbox", () => {
  it("a click on the checkbox's own slot selects nothing, since only the checkbox selects", () => {
    const onRowClick = vi.fn();
    const { container } = render(<Listing {...props({ onRowClick })} />);
    const slot = container.querySelector("tr[data-id='n1'] .row-check") as HTMLElement;
    expect(slot).not.toBeNull();
    fireEvent.click(slot);
    expect(onRowClick).not.toHaveBeenCalled();
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

  it("gives a tile a checkbox when the main pointer is a finger, and none otherwise", () => {
    const stub = (coarse: boolean) =>
      vi.stubGlobal("matchMedia", (query: string) => ({ matches: coarse && query === "(pointer: coarse)", media: query, addEventListener() {}, removeEventListener() {} }));
    stub(true);
    const onRowClick = vi.fn<(index: number, e: Click) => void>();
    const { unmount } = render(<Listing {...props({ view: "grid", onRowClick })} />);
    const box = screen.getByRole("checkbox", { name: "Select plan.txt" });
    fireEvent.click(box);
    expect(onRowClick).toHaveBeenCalledTimes(1);
    expect(onRowClick).toHaveBeenLastCalledWith(0, expect.objectContaining({ ctrlKey: true }));
    // The checkbox is not a tab stop.
    expect(box.getAttribute("tabindex")).toBe("-1");
    unmount();
    stub(false);
    render(<Listing {...props({ view: "grid" })} />);
    expect(screen.queryByRole("checkbox", { name: "Select plan.txt" })).toBeNull();
    vi.unstubAllGlobals();
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

  it("a long press on a finger opens the menu at the press, and the tap that ends it opens nothing", () => {
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
      expect(onRowContext).toHaveBeenCalledWith("n1", 0, 30, 60);
      fireEvent.click(row);
      expect(onRowClick).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("a finger that moves more than 10 pixels is scrolling, so no menu opens; 10 pixels or less still holds the press", () => {
    vi.useFakeTimers();
    try {
      const onRowContext = vi.fn();
      render(<Listing {...props({ onRowContext } as Partial<ListingProps>)} />);
      const row = screen.getByRole("row", { name: "plan.txt" });
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 30, clientY: 60 });
      fireEvent.pointerMove(row, { pointerType: "touch", clientX: 30, clientY: 71 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(onRowContext).not.toHaveBeenCalled();
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 30, clientY: 60 });
      fireEvent.pointerMove(row, { pointerType: "touch", clientX: 30, clientY: 70 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(onRowContext).toHaveBeenCalledTimes(1);
    } finally {
      vi.useRealTimers();
    }
  });

  it("the mousedown after a long press is stopped before the window's later listeners, so the menu it opened stays", () => {
    vi.useFakeTimers();
    try {
      const onRowContext = vi.fn();
      render(<Listing {...props({ onRowContext } as Partial<ListingProps>)} />);
      const row = screen.getByRole("row", { name: "plan.txt" });
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 30, clientY: 60 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(onRowContext).toHaveBeenCalled();
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
    // The icons are drawn for a hovered row only (review fix 7): the mouse goes over n1 first.
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

  // A star icon is named for the action it does (Unstar on a starred item), and is not a toggle: no aria-pressed (review fix 6).
  it("a starred item's star icon is named for its action, with no pressed state, and an item with no action list shows none", () => {
    withPointer(false);
    const starred = (n: Node) => (n.id === "n2" ? [rowAct("star", "Unstar")] : []);
    render(<Listing {...props({ rowActs: starred })} />);
    fireEvent.pointerEnter(rowOf("n2"), { pointerType: "mouse" });
    const star = within(rowOf("n2")).getByRole("button", { name: "Unstar" });
    expect(star.hasAttribute("aria-pressed")).toBe(false);
    expect(within(rowOf("n1")).queryByRole("button", { name: /Star/ })).toBeNull();
  });

  // Review fix 7: a row's four icons are drawn only for the row the mouse is on, the selected rows and the row with the focus; the other
  // rows keep only their ⋮.
  it("draws the icons only for a hovered, selected or focused row (review fix 7)", () => {
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

  // Review fix 11: the menu opens at the ⋮'s start edge: its left in English, its right in Hebrew.
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

  // Review fix 1: a key's click on the ⋮ passes the button as the origin the menu gives the focus back to; a mouse click does not.
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

describe("checkboxes on demand (decision 70)", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("with a mouse the list has no checkbox column, and the row's checkbox is in its icon's place", () => {
    withPointer(false);
    const { container } = render(<Listing {...props()} />);
    expect(container.querySelector(".ant-table-selection-column")).toBeNull();
    expect(within(rowOf("n1")).getByRole("checkbox", { name: "Select plan.txt" })).toBeTruthy();
  });

  it("the list says whether anything is selected, so the checkboxes of every row can show", () => {
    withPointer(false);
    const { container, rerender } = render(<Listing {...props()} />);
    expect(container.querySelector(".files-list")?.getAttribute("data-has-selection")).toBe("false");
    rerender(<Listing {...props({ selected: new Set(["n2"]) })} />);
    expect(container.querySelector(".files-list")?.getAttribute("data-has-selection")).toBe("true");
  });

  it("with a mouse the select-all box is in the name's header, and selects every item", () => {
    withPointer(false);
    const onSelectAll = vi.fn();
    render(<Listing {...props({ onSelectAll })} />);
    const all = screen.getByRole("checkbox", { name: "Select all" });
    fireEvent.click(all);
    expect(onSelectAll).toHaveBeenCalledWith(true);
  });

  it("on a finger the list keeps its checkbox column, as it has", () => {
    withPointer(true);
    const { container } = render(<Listing {...props()} />);
    expect(container.querySelector(".ant-table-selection-column")).not.toBeNull();
  });
});

// The generic table (ItemList) is the one list of the admin pages and the other preset lists (tl1). It shares the look and
// the phone rules of My files, and none of its behaviour. Its tests set the viewport themselves: the default mock is narrow.
type Row = { id: string; name: string; email: string; status: string; when: string };
const people: Row[] = [
  { id: "r1", name: "Acme Studio", email: "acme@example.com", status: "Active", when: "Mar 3, 2026" },
  { id: "r2", name: "Beta Clinic", email: "beta@example.com", status: "Suspended", when: "Jun 18, 2026" },
];
const viewport = (px: number) =>
  vi.stubGlobal("matchMedia", (query: string) => {
    const m = /min-width: (\d+)px/.exec(query);
    return { matches: m ? px >= Number(m[1]) : false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
  });
const itemProps = (over: Partial<ItemListProps<Row>> = {}): ItemListProps<Row> => ({
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
  empty: <p>Nothing here</p>,
  ...over,
});

describe("ItemList: one table for the admin and preset lists", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("renders a region with the rows, the headers and no files-list class", () => {
    viewport(1200);
    const { container } = render(<ItemList {...itemProps()} />);
    const region = screen.getByRole("region", { name: "Tenants" });
    expect(region.classList.contains("item-list")).toBe(true);
    expect(region.classList.contains("files-list")).toBe(false);
    expect(container.querySelectorAll("tbody tr[aria-label]").length).toBe(2);
    expect(screen.getByRole("columnheader", { name: "Status" })).toBeTruthy();
  });

  it("shows a secondary column as its own column on a desktop, and as the name's second line on a phone", () => {
    viewport(1200);
    const { unmount } = render(<ItemList {...itemProps()} />);
    expect(screen.getByRole("columnheader", { name: "Email" })).toBeTruthy();
    unmount();
    viewport(375);
    render(<ItemList {...itemProps()} />);
    expect(screen.queryByRole("columnheader", { name: "Email" })).toBeNull();
    const line = document.querySelector(".item-second");
    expect(line?.textContent).toContain("acme@example.com");
    expect(line?.textContent).toContain("Mar 3, 2026");
  });

  it("a sortable header is a button that reports its key, and carries aria-sort on its th", () => {
    viewport(1200);
    const onSort = vi.fn();
    render(<ItemList {...itemProps({ sort: { key: "name", dir: "asc", onSort } })} />);
    const head = screen.getByRole("columnheader", { name: /Name/ });
    expect(head.getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(within(head).getByRole("button", { name: "Name" }));
    expect(onSort).toHaveBeenCalledWith("name");
  });

  it("a time column with no sort has aria-sort descending and no button", () => {
    viewport(1200);
    render(<ItemList {...itemProps({ sort: { key: "name", dir: "asc", onSort: vi.fn() } })} />);
    const when = screen.getByRole("columnheader", { name: "Created" });
    expect(when.getAttribute("aria-sort")).toBe("descending");
    expect(within(when).queryByRole("button")).toBeNull();
  });

  it("shows up to three inline icons on a desktop and sends the rest to the more menu", () => {
    viewport(1200);
    const acts = () => [
      { key: "a", label: "Act A", icon: <span>a</span>, run: vi.fn() },
      { key: "b", label: "Act B", icon: <span>b</span>, run: vi.fn() },
      { key: "c", label: "Act C", icon: <span>c</span>, run: vi.fn() },
      { key: "d", label: "Act D", icon: <span>d</span>, run: vi.fn() },
    ];
    render(<ItemList {...itemProps({ actions: () => acts().map((a) => ({ ...a, enabled: true })), menu: () => [{ key: "x", label: "Suspend", onSelect: vi.fn() }] })} />);
    expect(screen.getAllByRole("button", { name: /^Act [ABC]$/ })).toHaveLength(6);
    expect(screen.queryByRole("button", { name: /Act D/ })).toBeNull();
  });

  it("on a phone shows the more menu alone, and its entries run", () => {
    viewport(375);
    const onSelect = vi.fn();
    render(<ItemList {...itemProps({ actions: () => [{ key: "a", label: "Act A", icon: <span>a</span>, enabled: true, run: vi.fn() }], menu: () => [{ key: "x", label: "Suspend", onSelect }] })} />);
    expect(screen.queryByRole("button", { name: /Act A/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "More for Acme Studio" }));
    fireEvent.click(screen.getByText("Suspend"));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it("an empty list shows the empty node the page passed, in the body", () => {
    viewport(1200);
    render(<ItemList {...itemProps({ items: [], empty: <div role="status">Nothing here</div> })} />);
    expect(screen.getByRole("status").textContent).toContain("Nothing here");
  });

  it("each row carries its label as aria-label", () => {
    viewport(1200);
    const { container } = render(<ItemList {...itemProps()} />);
    expect(container.querySelector('tr[aria-label="Acme Studio"]')).not.toBeNull();
  });
});

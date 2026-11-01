// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { type Click, type ListingProps } from "./Listing";

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
    onStar: vi.fn(),
    ...over,
  };
}

const rowOf = (id: string) => document.querySelector(`[data-id="${id}"]`) as HTMLElement;

// A starred item shows one star: the button where the person may star, and the mark only where no button is shown.
const starControls = (el: HTMLElement) => ({
  buttons: el.querySelectorAll('button[aria-pressed]').length,
  marks: Array.from(el.querySelectorAll(".sr-only")).filter((x) => x.textContent === "Starred").length,
});

describe("Listing: one star for a starred item", () => {
  it("a list row shows the star button alone when the person may star", () => {
    render(<Listing {...props()} />);
    expect(starControls(rowOf("n2"))).toEqual({ buttons: 1, marks: 0 });
  });

  it("a list row shows the mark alone where no star button is shown", () => {
    render(<Listing {...props({ onStar: undefined })} />);
    expect(starControls(rowOf("n2"))).toEqual({ buttons: 0, marks: 1 });
  });

  it("a tile shows the star button alone when the person may star", () => {
    render(<Listing {...props({ view: "grid" })} />);
    expect(starControls(rowOf("n2"))).toEqual({ buttons: 1, marks: 0 });
  });

  it("a tile shows the mark alone where no star button is shown", () => {
    render(<Listing {...props({ view: "grid", onStar: undefined })} />);
    expect(starControls(rowOf("n2"))).toEqual({ buttons: 0, marks: 1 });
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
    expect(within(row).getByRole("button", { name: "Star notes.txt" }).getAttribute("aria-pressed")).toBe("true");
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

  it("the star button calls onStar and does not select the tile", () => {
    const p = props({ view: "grid" });
    render(<Listing {...p} />);
    fireEvent.click(screen.getByRole("button", { name: "Star plan.txt" }));
    expect((p.onStar as ReturnType<typeof vi.fn>).mock.calls[0][0].id).toBe("n1");
    expect(p.onRowClick).not.toHaveBeenCalled();
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
  it("a click on the cell itself selects nothing, since only the checkbox selects", () => {
    const onRowClick = vi.fn();
    const { container } = render(<Listing {...props({ onRowClick })} />);
    const cell = container.querySelector("tr[data-id='n1'] td.ant-table-selection-column") as HTMLElement;
    expect(cell).not.toBeNull();
    fireEvent.click(cell);
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

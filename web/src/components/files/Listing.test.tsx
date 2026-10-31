// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, within } from "@testing-library/react";
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
    expect(within(row).getByText("Starred")).toBeTruthy();
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

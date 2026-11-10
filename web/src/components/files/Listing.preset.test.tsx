// SPDX-License-Identifier: AGPL-3.0-or-later

// The preset list's shared behaviour (tl4): the ⋮ menu on a right click and on a finger's long press, the row's
// class and the empty body's class the page gives, and a click that opens a row while Ctrl and Shift select.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import Listing, { type ItemListingProps } from "./Listing";

type Row = { id: string; name: string };
const rows: Row[] = [
  { id: "a", name: "Alpha" },
  { id: "b", name: "Beta" },
];
const columns: ItemListingProps<Row>["columns"] = [
  { key: "name", title: "Name", render: (r) => r.name },
  { key: "when", title: "When", secondary: true, ariaSort: "descending", render: () => "today" },
];

function list(over: Partial<ItemListingProps<Row>> = {}) {
  return render(<Listing<Row> label="Things" items={rows} rowLabel={(r) => r.name} columns={columns} empty={{ title: "Nothing here" }} {...over} />);
}

const rowOf = (name: string) => screen.getByRole("row", { name });

afterEach(() => {
  vi.useRealTimers();
});

describe("a preset row's ⋮ menu opens on a right click and on a finger's long press", () => {
  it("a right click on a row with entries opens its menu, and the browser's own menu does not", async () => {
    const run = vi.fn();
    list({ menu: () => [{ key: "open", label: "Open it", onSelect: run }] });
    const notPrevented = fireEvent.contextMenu(rowOf("Alpha"));
    expect(notPrevented).toBe(false);
    fireEvent.click(await screen.findByText("Open it"));
    expect(run).toHaveBeenCalledTimes(1);
  });

  it("a right click on a row with no entries keeps the browser's own menu", () => {
    list({ menu: () => [] });
    expect(fireEvent.contextMenu(rowOf("Alpha"))).toBe(true);
  });

  it("a finger's long press opens the menu, and the tap that ends it opens and selects nothing", () => {
    vi.useFakeTimers();
    const open = vi.fn();
    list({ selectable: true, onOpen: open, menu: () => [{ key: "open", label: "Open it", onSelect: () => undefined }] });
    const row = rowOf("Alpha");
    fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
    act(() => {
      vi.advanceTimersByTime(600);
    });
    expect(screen.getByText("Open it")).toBeTruthy();
    fireEvent.pointerUp(row, { pointerType: "touch" });
    fireEvent.click(row);
    expect(open).not.toHaveBeenCalled();
    expect(row.getAttribute("aria-selected")).toBe("false");
  });

  it("a finger that moves is scrolling, not a press, and opens no menu", () => {
    vi.useFakeTimers();
    list({ menu: () => [{ key: "open", label: "Open it", onSelect: () => undefined }] });
    const row = rowOf("Alpha");
    fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
    fireEvent.pointerMove(row, { pointerType: "touch", clientX: 40, clientY: 5 });
    act(() => {
      vi.advanceTimersByTime(600);
    });
    expect(screen.queryByText("Open it")).toBeNull();
  });
});

describe("a preset row's class and the empty body's class come from the page", () => {
  it("puts the page's class on each row", () => {
    list({ rowClassName: (r) => `row-${r.id}` });
    expect(rowOf("Alpha").className).toContain("row-a");
    expect(rowOf("Beta").className).toContain("row-b");
  });

  it("puts the page's class on the empty body, inside the table", () => {
    const { container } = list({ items: [], empty: { title: "Nothing here", className: "empty" } });
    expect(container.querySelector("tbody .empty")?.textContent).toContain("Nothing here");
  });
});

describe("a preset row opens on a click, and Ctrl or Shift select", () => {
  it("a plain click on a selectable row opens it and selects nothing", () => {
    const open = vi.fn();
    list({ selectable: true, onOpen: open });
    fireEvent.click(rowOf("Alpha"));
    expect(open).toHaveBeenCalledWith(rows[0]);
    expect(rowOf("Alpha").getAttribute("aria-selected")).toBe("false");
  });

  it("a Ctrl click selects the row and opens nothing", () => {
    const open = vi.fn();
    list({ selectable: true, onOpen: open });
    fireEvent.click(rowOf("Alpha"), { ctrlKey: true });
    expect(open).not.toHaveBeenCalled();
    expect(rowOf("Alpha").getAttribute("aria-selected")).toBe("true");
  });

  it("a plain click on a selectable row with no open action selects it", () => {
    list({ selectable: true });
    fireEvent.click(rowOf("Beta"));
    expect(rowOf("Beta").getAttribute("aria-selected")).toBe("true");
  });

  it("Enter on a focused row opens it (tl4 r1: the keyboard path, design 2.2)", () => {
    const open = vi.fn();
    list({ selectable: true, onOpen: open });
    // Tab focuses the row, which the list then knows as its focused row, and Enter opens that one.
    fireEvent.focus(rowOf("Beta"));
    fireEvent.keyDown(rowOf("Beta"), { key: "Enter" });
    expect(open).toHaveBeenCalledWith(rows[1]);
  });

  it("a Shift click selects the rows from the last Ctrl-picked row to the clicked one", () => {
    const three: Row[] = [...rows, { id: "c", name: "Gamma" }];
    list({ items: three, selectable: true });
    fireEvent.click(rowOf("Alpha"), { ctrlKey: true });
    fireEvent.click(rowOf("Gamma"), { shiftKey: true });
    expect(rowOf("Alpha").getAttribute("aria-selected")).toBe("true");
    expect(rowOf("Beta").getAttribute("aria-selected")).toBe("true");
    expect(rowOf("Gamma").getAttribute("aria-selected")).toBe("true");
  });
});

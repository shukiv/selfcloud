// SPDX-License-Identifier: AGPL-3.0-or-later

// The preset list's shared behaviour (tl4): the ⋮ menu on a right click and on a finger's long press, the row's
// class and the empty body's class the page gives, and a click that opens a row while Ctrl and Shift select.
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactNode } from "react";
import Listing, { type ItemListingProps } from "./Listing";
import { desktopViewport } from "../../test/viewport";

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

describe("a preset row's ⋮ menu opens on a right click and from its ⋮ button on a finger", () => {
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

  it("a finger's long press selects the row and opens no menu; the tap that ends it selects nothing more, and the ⋮ opens the menu", async () => {
    vi.useFakeTimers();
    const open = vi.fn();
    list({ selectable: true, onOpen: open, menu: () => [{ key: "open", label: "Open it", onSelect: () => undefined }] });
    const row = rowOf("Alpha");
    fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
    act(() => {
      vi.advanceTimersByTime(600);
    });
    expect(row.getAttribute("aria-selected")).toBe("true");
    expect(screen.queryByText("Open it")).toBeNull();
    fireEvent.pointerUp(row, { pointerType: "touch" });
    fireEvent.click(row);
    expect(open).not.toHaveBeenCalled();
    expect(row.getAttribute("aria-selected")).toBe("true");
    vi.useRealTimers();
    fireEvent.click(screen.getByRole("button", { name: "More actions for Alpha" }));
    expect(await screen.findByText("Open it")).toBeTruthy();
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

  it("Enter on a focused row opens it (the keyboard path, the slice's design, not in this repository, section 2.2)", () => {
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

// The phone's second line (mob4): the secondary cells are inline items of one wrapping row. The separator between two
// items is drawn by the stylesheet (.row-second-item + .row-second-item::before), so no text node holds a "·", an empty
// cell draws nothing and no separator, and none can sit before the first item, after the last, or twice in a row.
describe("a preset list's phone second line joins inline items with CSS separators (mob4)", () => {
  const secondColumns = (renders: ((r: Row) => ReactNode)[]): ItemListingProps<Row>["columns"] => [
    { key: "name", title: "Name", render: (r) => r.name },
    ...renders.map((render, i) => ({ key: `s${i}`, title: `S${i}`, secondary: true, render })),
  ];
  const lineOf = (name: string) => rowOf(name).querySelector(".row-second");
  const itemsOf = (name: string) => [...(lineOf(name)?.querySelectorAll(".row-second-item") ?? [])];

  it("joins the cells as inline items with no text separator nodes", () => {
    list({ phone: true, columns: secondColumns([(r) => r.name, () => "today", () => "4 bytes"]) });
    const line = lineOf("Alpha");
    expect(itemsOf("Alpha").map((c) => c.textContent)).toEqual(["Alpha", "today", "4 bytes"]);
    // No text node of the line holds a separator: the dot is the stylesheet's content, between the items only.
    const walker = document.createTreeWalker(line!, globalThis.NodeFilter.SHOW_TEXT);
    for (let n = walker.nextNode(); n !== null; n = walker.nextNode()) expect(n.textContent).not.toContain("·");
    expect(line?.textContent).toBe("Alphatoday4 bytes");
    // Every item is one inline span of the row, not a block: the line wraps as one row of items.
    for (const item of itemsOf("Alpha")) expect(item.tagName).toBe("SPAN");
  });

  it("an empty cell draws nothing and no separator, wherever it sits", () => {
    list({ phone: true, columns: secondColumns([() => "", () => null, () => "kept", () => "   ", () => "last"]) });
    expect(itemsOf("Alpha").map((c) => c.textContent)).toEqual(["kept", "last"]);
    expect(lineOf("Alpha")?.textContent).toBe("keptlast");
  });

  it("a row whose cells are all empty has no second line at all", () => {
    list({ phone: true, columns: secondColumns([() => "", () => null]) });
    expect(lineOf("Alpha")).toBeNull();
  });

  it("a single cell stands alone, with no separator before or after it", () => {
    list({ phone: true, columns: secondColumns([() => "only"]) });
    expect(itemsOf("Alpha").map((c) => c.textContent)).toEqual(["only"]);
    expect(lineOf("Alpha")?.textContent).toBe("only");
  });

  it("the desktop keeps the secondary columns, with no second line", () => {
    const undo = desktopViewport();
    try {
      list({ columns: secondColumns([(r) => r.name, () => "today"]) });
      expect(screen.getByRole("columnheader", { name: "S0" })).toBeTruthy();
      expect(lineOf("Alpha")).toBeNull();
    } finally {
      undo();
    }
  });
});

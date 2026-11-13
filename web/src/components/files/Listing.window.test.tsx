// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { type ListingProps } from "./Listing";

// My files' list view draws only the rows near the screen once more than 150 rows are loaded (decision 162). jsdom lays nothing
// out, so the geometry is fake: each row is 40 px, the column header 48 px, the list starts 120 px down the page, and the
// window is 800 px high. The page's scroll is scrollY, which the fake window.scrollTo sets.

const ROW = 40;
const HEAD = 48;
const REGION_TOP = 120;

let ids: string[] = [];
let scrollY = 0;

function rect(top: number, height: number): DOMRect {
  return { top, bottom: top + height, height, left: 0, right: 800, width: 800, x: 0, y: top, toJSON() {} } as DOMRect;
}

function fakeRect(el: Element): DOMRect {
  if (el.classList.contains("files-list")) return rect(REGION_TOP - scrollY, HEAD + ids.length * ROW);
  if (el.tagName === "THEAD") return rect(REGION_TOP - scrollY, HEAD);
  if (el.tagName === "TR" && el.hasAttribute("data-id")) {
    const i = ids.indexOf(el.getAttribute("data-id") ?? "");
    return rect(REGION_TOP + HEAD + i * ROW - scrollY, ROW);
  }
  return rect(0, 0);
}

function items(n: number): Node[] {
  return Array.from({ length: n }, (_, i) => ({
    id: `n${i}`,
    folder_id: "f1",
    parent_id: null,
    name: `file-${i}.txt`,
    kind: "file",
    size: 10,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
    starred: false,
  }));
}

function props(list: Node[], over: Partial<ListingProps> = {}): ListingProps {
  ids = list.map((n) => n.id);
  return {
    items: list,
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
    onRowDoubleClick: () => undefined,
    onSelectAll: vi.fn(),
    onSort: () => undefined,
    onRenameChange: () => undefined,
    onRename: () => undefined,
    onKeyDown: vi.fn(),
    onFocusItem: () => undefined,
    ...over,
  };
}

// scrollPage moves the page's scroll and tells the listing, as a browser does (a scroll event on the window, in its capture phase).
function scrollPage(y: number) {
  scrollY = y;
  act(() => {
    window.dispatchEvent(new Event("scroll"));
  });
}

function drawn(container: HTMLElement): string[] {
  return [...container.querySelectorAll<HTMLElement>("tbody tr[data-id]")].map((r) => r.dataset.id ?? "");
}

function spacer(container: HTMLElement, which: "top" | "bottom"): HTMLElement | null {
  return container.querySelector<HTMLElement>(`tbody tr[data-spacer="${which}"]`);
}

function setCoarse(coarse: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: coarse && query === "(pointer: coarse)",
    media: query,
    addEventListener() {},
    removeEventListener() {},
  }));
}

beforeEach(() => {
  scrollY = 0;
  Object.defineProperty(window, "innerHeight", { value: 800, configurable: true, writable: true });
  vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
    return fakeRect(this);
  });
  window.scrollTo = vi.fn((arg: ScrollToOptions | number) => {
    scrollY = typeof arg === "number" ? arg : (arg.top ?? scrollY);
  }) as unknown as typeof window.scrollTo;
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("My files' list view draws a slice once more than 150 rows are loaded (decision 162)", () => {
  it("150 rows: every row is drawn, and there are no spacers", () => {
    const { container } = render(<Listing {...props(items(150))} />);
    expect(drawn(container)).toHaveLength(150);
    expect(spacer(container, "top")).toBeNull();
    expect(spacer(container, "bottom")).toBeNull();
  });

  it("1,000 rows: a slice of the screen's rows, and two spacers that hold the place of the rest", async () => {
    const list = items(1000);
    const { container } = render(<Listing {...props(list)} />);
    await waitFor(() => expect(drawn(container).length).toBeLessThan(60));
    const n = drawn(container).length;
    expect(n).toBeGreaterThanOrEqual(15);
    expect(drawn(container)[0]).toBe("n0");
    expect(spacer(container, "top")?.style.height).toBe("0px");
    expect(parseFloat(spacer(container, "bottom")!.style.height)).toBe((1000 - n) * ROW);
  });

  it("the two spacers and the drawn rows add up to every loaded row", async () => {
    const { container } = render(<Listing {...props(items(1000))} />);
    await waitFor(() => expect(spacer(container, "bottom")).not.toBeNull());
    const top = parseFloat(spacer(container, "top")!.style.height);
    const bottom = parseFloat(spacer(container, "bottom")!.style.height);
    expect(top + drawn(container).length * ROW + bottom).toBe(1000 * ROW);
  });

  it("scrolling down moves the slice to the rows on the screen", async () => {
    const list = items(1000);
    const { container } = render(<Listing {...props(list)} />);
    await waitFor(() => expect(drawn(container).length).toBeGreaterThan(0));
    scrollPage(400 * ROW);
    await waitFor(() => expect(drawn(container)).toContain("n400"));
    expect(drawn(container)).not.toContain("n0");
    expect(parseFloat(spacer(container, "top")!.style.height)).toBeGreaterThan(0);
  });

  it("the row being renamed is drawn, even when it is far from the screen", async () => {
    const { container } = render(<Listing {...props(items(1000), { renameID: "n900", renameValue: "x" })} />);
    await waitFor(() => expect(drawn(container)).toContain("n900"));
    expect(container.querySelector('[data-id="n900"] input.rename')).not.toBeNull();
  });
});

describe("the table's row counts for a screen reader (decision 162)", () => {
  it("aria-rowcount is the loaded rows plus the header, and each row's aria-rowindex is its place, the header being row 1", async () => {
    const { container } = render(<Listing {...props(items(1000))} />);
    const table = container.querySelector("table")!;
    expect(table.getAttribute("aria-rowcount")).toBe("1001");
    expect(container.querySelector('thead tr')!.getAttribute("aria-rowindex")).toBe("1");
    expect(container.querySelector('[data-id="n0"]')!.getAttribute("aria-rowindex")).toBe("2");
    scrollPage(400 * ROW);
    await waitFor(() => expect(container.querySelector('[data-id="n400"]')).not.toBeNull());
    expect(container.querySelector('[data-id="n400"]')!.getAttribute("aria-rowindex")).toBe("402");
  });

  it("a row of a list that is not windowed has the same numbers", () => {
    const { container } = render(<Listing {...props(items(20))} />);
    expect(container.querySelector("table")!.getAttribute("aria-rowcount")).toBe("21");
    expect(container.querySelector('[data-id="n19"]')!.getAttribute("aria-rowindex")).toBe("21");
  });
});

describe("the page hears a row by its place in the whole list, not in the slice (decision 162)", () => {
  it("a click on a drawn row reports its index in the loaded list", async () => {
    const onRowClick = vi.fn();
    const { container } = render(<Listing {...props(items(1000), { onRowClick })} />);
    scrollPage(400 * ROW);
    await waitFor(() => expect(container.querySelector('[data-id="n400"]')).not.toBeNull());
    fireEvent.click(container.querySelector('[data-id="n400"]')!);
    expect(onRowClick).toHaveBeenCalledWith(400, expect.objectContaining({ shiftKey: false }));
  });
});

describe("the coarse pointer's select-all box reflects every loaded row (decision 162)", () => {
  it("all loaded rows selected, drawn or not: the box is checked", () => {
    setCoarse(true);
    const list = items(300);
    const selected = new Set(list.map((n) => n.id));
    const { container } = render(<Listing {...props(list, { selected })} />);
    const box = container.querySelector<HTMLInputElement>("thead .head-check input")!;
    expect(box).not.toBeNull();
    expect(box.checked).toBe(true);
    expect(container.querySelector("thead .head-check .ant-checkbox-indeterminate")).toBeNull();
  });

  it("only drawn rows selected: the box is indeterminate, not checked", async () => {
    setCoarse(true);
    const list = items(300);
    // The first three rows are drawn at the top of the list, so these are the selected rows the page can see.
    const { container } = render(<Listing {...props(list, { selected: new Set(["n0", "n1", "n2"]) })} />);
    expect(container.querySelector<HTMLInputElement>("thead .head-check input")!.checked).toBe(false);
    expect(container.querySelector("thead .head-check .ant-checkbox-indeterminate")).not.toBeNull();
  });

  it("the box selects every loaded row, not the slice", () => {
    setCoarse(true);
    const onSelectAll = vi.fn();
    const { container } = render(<Listing {...props(items(300), { onSelectAll })} />);
    fireEvent.click(container.querySelector<HTMLElement>("thead .head-check .ant-checkbox")!);
    expect(onSelectAll).toHaveBeenCalledWith(true);
  });
});

describe("keyboard focus to a row that is not drawn (decision 162)", () => {
  it("a focus request for an undrawn row scrolls the page to it, then focuses it", async () => {
    const list = items(1000);
    const { container, rerender } = render(<Listing {...props(list)} />);
    expect(container.querySelector('[data-id="n250"]')).toBeNull();
    rerender(<Listing {...props(list, { focusID: "n250", focusRequest: { id: "n250", seq: 1 } })} />);
    await waitFor(() => expect(document.activeElement?.getAttribute("data-id")).toBe("n250"));
    expect(window.scrollTo).toHaveBeenCalled();
    expect(scrollY).toBeGreaterThan(0);
  });

  it("a focused row that scrolls out of the slice hands the focus to the list, without scrolling", async () => {
    const list = items(1000);
    const { container } = render(<Listing {...props(list, { focusID: "n3" })} />);
    const row = container.querySelector<HTMLElement>('[data-id="n3"]')!;
    act(() => row.focus());
    expect(document.activeElement).toBe(row);
    const scrollTo = vi.mocked(window.scrollTo);
    scrollTo.mockClear();
    scrollPage(600 * ROW);
    await waitFor(() => expect(drawn(container)).not.toContain("n3"));
    expect(document.activeElement).toBe(container.querySelector(".files-list"));
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("the list region takes the arrow keys when it holds the focus", () => {
    const onKeyDown = vi.fn();
    const { container } = render(<Listing {...props(items(40), { onKeyDown })} />);
    const region = container.querySelector<HTMLElement>(".files-list")!;
    act(() => region.focus());
    fireEvent.keyDown(region, { key: "ArrowDown" });
    expect(onKeyDown).toHaveBeenCalledTimes(1);
  });
});

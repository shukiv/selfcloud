// SPDX-License-Identifier: AGPL-3.0-or-later

// The row height choice in the lists (decision 194): compact draws antd's small table and the list--compact class with a
// mouse, in both presets; a finger keeps the middle table whatever the choice, its tap targets as they are. The window
// measures the new density's height and recomputes the drawn rows, so a switch leaves no stale height, gap or overlap.

import { act, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import { session, type Session } from "../../lib/session";
import { writeDensity } from "../../lib/files/density";
import Listing, { type ItemListingProps, type ListingProps } from "./Listing";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: `Name ${id}`, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: "t1",
    publicLinks: false,
  };
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
    onRowClick: () => undefined,
    onRowDoubleClick: () => undefined,
    onSelectAll: () => undefined,
    onSort: () => undefined,
    onRenameChange: () => undefined,
    onRename: () => undefined,
    onKeyDown: () => undefined,
    onFocusItem: () => undefined,
    ...over,
  };
}

type Row = { id: string; name: string };
const presetRows: Row[] = [
  { id: "a", name: "Alpha" },
  { id: "b", name: "Beta" },
];
function presetProps(over: Partial<ItemListingProps<Row>> = {}) {
  return render(
    <Listing<Row>
      label="Things"
      items={presetRows}
      rowLabel={(r) => r.name}
      columns={[{ key: "name", title: "Name", render: (r) => r.name }]}
      empty={{ title: "Nothing here" }}
      {...over}
    />,
  );
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
  window.localStorage.clear();
  session.set(signedAs("u1"));
});

afterEach(() => {
  // The choice is put back before the lists unmount, so no later test reads a compact list.
  act(() => {
    session.set(null);
    writeDensity("u1", "comfortable");
  });
  window.localStorage.clear();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("the files preset's row height (decision 194)", () => {
  it("compact with a mouse draws the small table and the list--compact class", () => {
    writeDensity("u1", "compact");
    const { container } = render(<Listing {...props(items(2))} />);
    expect(container.querySelector(".files-list")?.classList.contains("list--compact")).toBe(true);
    expect(container.querySelector(".ant-table-small")).not.toBeNull();
    expect(container.querySelector(".ant-table-middle")).toBeNull();
  });

  it("comfortable draws the middle table, without the compact class", () => {
    const { container } = render(<Listing {...props(items(2))} />);
    expect(container.querySelector(".files-list")?.classList.contains("list--compact")).toBe(false);
    expect(container.querySelector(".ant-table-middle")).not.toBeNull();
    expect(container.querySelector(".ant-table-small")).toBeNull();
  });

  it("compact with a finger keeps the middle table and its tap targets", () => {
    setCoarse(true);
    writeDensity("u1", "compact");
    const { container } = render(<Listing {...props(items(2))} />);
    expect(container.querySelector(".files-list")?.classList.contains("list--compact")).toBe(false);
    expect(container.querySelector(".ant-table-middle")).not.toBeNull();
    expect(container.querySelector(".ant-table-small")).toBeNull();
  });

  it("the tiles are unchanged by the choice", () => {
    writeDensity("u1", "compact");
    const { container } = render(<Listing {...props(items(2), { view: "grid" })} />);
    expect(container.querySelector(".list--compact")).toBeNull();
    expect(container.querySelector(".ant-table-small")).toBeNull();
    expect(container.querySelector('[role="listbox"]')).not.toBeNull();
  });
});

describe("an item preset's row height (decision 194)", () => {
  it("compact with a mouse draws the small table and the list--compact class", () => {
    writeDensity("u1", "compact");
    const { container } = presetProps();
    expect(container.querySelector(".item-list")?.classList.contains("list--compact")).toBe(true);
    expect(container.querySelector(".ant-table-small")).not.toBeNull();
    expect(container.querySelector(".ant-table-middle")).toBeNull();
  });

  it("comfortable draws the middle table, without the compact class", () => {
    const { container } = presetProps();
    expect(container.querySelector(".item-list")?.classList.contains("list--compact")).toBe(false);
    expect(container.querySelector(".ant-table-middle")).not.toBeNull();
    expect(container.querySelector(".ant-table-small")).toBeNull();
  });

  it("compact with a finger keeps the middle table", () => {
    setCoarse(true);
    writeDensity("u1", "compact");
    const { container } = presetProps();
    expect(container.querySelector(".item-list")?.classList.contains("list--compact")).toBe(false);
    expect(container.querySelector(".ant-table-middle")).not.toBeNull();
    expect(container.querySelector(".ant-table-small")).toBeNull();
  });
});

// The window's geometry is fake, as in Listing.window.test.tsx: the column header is 48 px, the list starts 120 px down the
// page and the window is 800 px high. A row is 40 px in comfortable and 32 px in compact, read from the list's own class, so
// the measure sees the height the density draws.
const ROW_COMFORTABLE = 40;
const ROW_COMPACT = 32;
const HEAD = 48;
const REGION_TOP = 120;

let ids: string[] = [];
let scrollY = 0;

function rect(top: number, height: number): DOMRect {
  return new DOMRect(0, top, 800, height);
}

function rowHOf(el: Element): number {
  return el.closest(".files-list")?.classList.contains("list--compact") ? ROW_COMPACT : ROW_COMFORTABLE;
}

function fakeRect(el: Element): DOMRect {
  if (el.classList.contains("files-list")) return rect(REGION_TOP - scrollY, HEAD + ids.length * rowHOf(el));
  if (el.tagName === "THEAD") return rect(REGION_TOP - scrollY, HEAD);
  if (el.tagName === "TR" && el.hasAttribute("data-id")) {
    const i = ids.indexOf(el.getAttribute("data-id") ?? "");
    const h = rowHOf(el);
    return rect(REGION_TOP + HEAD + i * h - scrollY, h);
  }
  return rect(0, 0);
}

function windowProps(list: Node[]): ListingProps {
  ids = list.map((n) => n.id);
  return props(list);
}

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

function totalHeight(container: HTMLElement, rowH: number): number {
  const top = parseFloat(spacer(container, "top")!.style.height);
  const bottom = parseFloat(spacer(container, "bottom")!.style.height);
  return top + drawn(container).length * rowH + bottom;
}

describe("the window measures the new density's height (decision 194)", () => {
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

  it("switching density recomputes the drawn range with the new height, and switching back restores it", async () => {
    const list = items(1000);
    const { container } = render(<Listing {...windowProps(list)} />);
    await waitFor(() => expect(drawn(container).length).toBeLessThan(60));
    expect(totalHeight(container, ROW_COMFORTABLE)).toBe(1000 * ROW_COMFORTABLE);

    // At 16,000 px down, the comfortable window draws from row 389 (the screen's row 399, ten above).
    scrollPage(400 * ROW_COMFORTABLE);
    await waitFor(() => expect(drawn(container)).toContain("n400"));
    expect(drawn(container)[0]).toBe("n389");
    const comfortableCount = drawn(container).length;

    act(() => {
      writeDensity("u1", "compact");
    });
    await waitFor(() => expect(container.querySelector(".ant-table-small")).not.toBeNull());
    // The same scroll now points at row 499: the window draws from 489, measured at 32 px, more rows on the screen.
    await waitFor(() => expect(drawn(container)[0]).toBe("n489"));
    expect(drawn(container).length).toBeGreaterThan(comfortableCount);
    expect(totalHeight(container, ROW_COMPACT)).toBe(1000 * ROW_COMPACT);
    expect(container.querySelector("table")!.getAttribute("aria-rowcount")).toBe("1001");

    act(() => {
      writeDensity("u1", "comfortable");
    });
    await waitFor(() => expect(drawn(container)[0]).toBe("n389"));
    expect(drawn(container).length).toBe(comfortableCount);
    expect(totalHeight(container, ROW_COMFORTABLE)).toBe(1000 * ROW_COMFORTABLE);
  });

  it("a list that starts compact measures the compact height at once", async () => {
    writeDensity("u1", "compact");
    const { container } = render(<Listing {...windowProps(items(1000))} />);
    await waitFor(() => expect(drawn(container).length).toBeLessThan(60));
    expect(container.querySelector(".files-list")?.classList.contains("list--compact")).toBe(true);
    expect(totalHeight(container, ROW_COMPACT)).toBe(1000 * ROW_COMPACT);
    // The screen fits more compact rows: the first frame's slice is already the compact one.
    scrollPage(400 * ROW_COMFORTABLE);
    await waitFor(() => expect(drawn(container)[0]).toBe("n489"));
  });
});

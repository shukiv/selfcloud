// SPDX-License-Identifier: AGPL-3.0-or-later

// A finger's targets in the listing (mob3): a sortable column header is at least 44 px tall. A finger selects a row by a long
// press, and a tap toggles a row while something is selected; with nothing selected a tap opens the item. There is no select
// cell and no checkbox. On a mouse none of this changes: a click selects the row. A preset list's ⋮ menu rows are a finger's size too.
import { useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { type ItemListingProps, type ListingProps } from "./Listing";
import { coarsePointer } from "../../test/viewport";

function node(i: number): Node {
  return {
    id: `n${i}`,
    folder_id: "f1",
    parent_id: null,
    name: `file-${i}.txt`,
    kind: "file",
    size: 2048,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
    starred: false,
  };
}
const items = Array.from({ length: 6 }, (_, i) => node(i));

// FilesPage keeps the selection and opens on a finger's tap: a touch click opens, a Ctrl click toggles, a plain click selects.
// The opens are drawn into a test id so a check can say a select-cell tap opened nothing.
function FilesPage({ view }: { view: "list" | "grid" }) {
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [opens, setOpens] = useState<string[]>([]);
  const p: ListingProps = {
    items,
    view,
    selected: sel,
    focusID: null,
    sortKey: "name",
    sortDir: "asc",
    renameID: null,
    renameValue: "",
    renameError: null,
    label: "Files",
    onRowClick: (i, e) => {
      const id = items[i].id;
      if (e.touch) {
        setOpens((o) => [...o, id]);
        return;
      }
      setSel((s) => {
        const next = new Set(s);
        if (e.ctrlKey || e.metaKey) {
          if (next.has(id)) next.delete(id);
          else next.add(id);
        } else {
          next.clear();
          next.add(id);
        }
        return next;
      });
    },
    onRowDoubleClick: () => undefined,
    onSelectAll: () => undefined,
    onSort: () => undefined,
    onRenameChange: () => undefined,
    onRename: () => undefined,
    onKeyDown: () => undefined,
    onFocusItem: () => undefined,
  };
  return (
    <>
      <Listing {...p} />
      <div data-testid="opens">{opens.join(",")}</div>
    </>
  );
}

type Row = { id: string; name: string };
const prows: Row[] = [
  { id: "a", name: "Alpha" },
  { id: "b", name: "Beta" },
];
function presetProps(over: Partial<ItemListingProps<Row>> = {}): ItemListingProps<Row> {
  return {
    items: prows,
    label: "Things",
    rowLabel: (r) => r.name,
    columns: [{ key: "name", title: "Name", render: (r) => r.name }],
    selectable: true,
    menu: (r) => [{ key: "info", label: `Info ${r.name}`, onSelect: () => undefined }],
    empty: { title: "Nothing here" },
    ...over,
  };
}

let restore: () => void = () => undefined;
afterEach(() => {
  restore();
  restore = () => undefined;
});

const selected = (container: HTMLElement, id: string) => container.querySelector(`[data-id="${id}"]`)?.getAttribute("aria-selected");

describe("a sortable column header is a finger's target (mob3)", () => {
  it("is at least 44 px tall on a coarse pointer", () => {
    restore = coarsePointer();
    const { container } = render(<FilesPage view="list" />);
    const btn = container.querySelector(".files-list .ant-table-thead th button:not(.head-mark)") as HTMLElement;
    expect(btn).not.toBeNull();
    expect(btn.style.minHeight).toBe("44px");
  });

  it("keeps its small height on a mouse", () => {
    const { container } = render(<FilesPage view="list" />);
    const btn = container.querySelector(".files-list .ant-table-thead th button:not(.head-mark)") as HTMLElement;
    expect(btn).not.toBeNull();
    expect(btn.style.minHeight).toBe("");
  });
});

describe("a finger selects a row by long press, and taps toggle while something is selected", () => {
  it("a long press selects the row and opens nothing; the tap that ends it selects nothing more", () => {
    vi.useFakeTimers();
    try {
      restore = coarsePointer();
      const { container } = render(<FilesPage view="list" />);
      const row = container.querySelector('[data-id="n3"]') as HTMLElement;
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(selected(container, "n3")).toBe("true");
      expect(screen.getByTestId("opens").textContent).toBe("");
      fireEvent.pointerUp(row, { pointerType: "touch" });
      fireEvent.click(row);
      expect(selected(container, "n3")).toBe("true");
      expect(screen.getByTestId("opens").textContent).toBe("");
    } finally {
      vi.useRealTimers();
    }
  });

  it("a second long press on a selected row clears it", () => {
    vi.useFakeTimers();
    try {
      restore = coarsePointer();
      const { container } = render(<FilesPage view="list" />);
      const row = container.querySelector('[data-id="n3"]') as HTMLElement;
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      fireEvent.pointerUp(row, { pointerType: "touch" });
      fireEvent.click(row);
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(selected(container, "n3")).toBe("false");
      expect(screen.getByTestId("opens").textContent).toBe("");
    } finally {
      vi.useRealTimers();
    }
  });

  it("with something selected, a plain tap on another row toggles it in and opens nothing", () => {
    vi.useFakeTimers();
    try {
      restore = coarsePointer();
      const { container } = render(<FilesPage view="list" />);
      const row = container.querySelector('[data-id="n3"]') as HTMLElement;
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      fireEvent.pointerUp(row, { pointerType: "touch" });
      fireEvent.click(row);
      expect(selected(container, "n3")).toBe("true");
      fireEvent.click(container.querySelector('[data-id="n4"]') as HTMLElement);
      expect(selected(container, "n4")).toBe("true");
      expect(screen.getByTestId("opens").textContent).toBe("");
    } finally {
      vi.useRealTimers();
    }
  });

  it("with nothing selected, a tap opens the row", () => {
    restore = coarsePointer();
    const { container } = render(<FilesPage view="list" />);
    const row = container.querySelector('[data-id="n2"]') as HTMLElement;
    fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
    fireEvent.pointerUp(row, { pointerType: "touch" });
    fireEvent.click(row);
    expect(selected(container, "n2")).toBe("false");
    expect(screen.getByTestId("opens").textContent).toBe("n2");
  });

  it("on a mouse there is no select cell or checkbox, and a click selects the row without opening it", () => {
    const { container } = render(<FilesPage view="list" />);
    expect(container.querySelector(".ant-table-selection-column")).toBeNull();
    expect(container.querySelector(".ant-checkbox")).toBeNull();
    const row = container.querySelector('[data-id="n2"]') as HTMLElement;
    fireEvent.click(row);
    expect(selected(container, "n2")).toBe("true");
    expect(screen.getByTestId("opens").textContent).toBe("");
  });
});

describe("a preset list's long press and ⋮ menu on a finger (mob3)", () => {
  it("a long press toggles the row, and a tap while something is selected toggles too", () => {
    vi.useFakeTimers();
    try {
      restore = coarsePointer();
      const { container } = render(<Listing<Row> {...presetProps()} />);
      const row = container.querySelector('[data-id="a"]') as HTMLElement;
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(selected(container, "a")).toBe("true");
      fireEvent.pointerUp(row, { pointerType: "touch" });
      fireEvent.click(row);
      expect(selected(container, "a")).toBe("true");
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      act(() => {
        vi.advanceTimersByTime(600);
      });
      expect(selected(container, "a")).toBe("false");
    } finally {
      vi.useRealTimers();
    }
  });

  it("the ⋮ menu's rows are 44 px tall on a coarse pointer", async () => {
    restore = coarsePointer();
    render(<Listing<Row> {...presetProps()} />);
    fireEvent.click(screen.getByRole("button", { name: "More actions for Alpha" }));
    const item = await screen.findByRole("menuitem", { name: "Info Alpha" });
    expect(item.style.height).toBe("44px");
  });

  it("the ⋮ menu's rows keep their height on a mouse", async () => {
    render(<Listing<Row> {...presetProps()} />);
    fireEvent.click(screen.getByRole("button", { name: "More actions for Alpha" }));
    const item = await screen.findByRole("menuitem", { name: "Info Alpha" });
    expect(item.style.height).toBe("");
  });
});
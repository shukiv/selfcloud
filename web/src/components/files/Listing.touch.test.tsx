// SPDX-License-Identifier: AGPL-3.0-or-later

// A finger's targets in the listing (mob3): a sortable column header is at least 44 px tall, a tap anywhere in a row's select
// cell toggles the row (not only the little box), and that tap selects without opening the item. On a mouse none of this
// changes: there is no select cell, and the header keeps its small height. A preset list's ⋮ menu rows are a finger's size too.
import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
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
    const btn = container.querySelector(".files-list .ant-table-thead th button") as HTMLElement;
    expect(btn).not.toBeNull();
    expect(btn.style.minHeight).toBe("44px");
  });

  it("keeps its small height on a mouse", () => {
    const { container } = render(<FilesPage view="list" />);
    const btn = container.querySelector(".files-list .ant-table-thead th button") as HTMLElement;
    expect(btn).not.toBeNull();
    expect(btn.style.minHeight).toBe("");
  });
});

describe("a tap in a row's select cell toggles the row on a finger (mob3)", () => {
  it("selects the row away from the box, opens nothing, and a second tap clears it", () => {
    restore = coarsePointer();
    const { container } = render(<FilesPage view="list" />);
    const cell = container.querySelector('[data-id="n3"] .ant-table-selection-column') as HTMLElement;
    expect(cell).not.toBeNull();
    fireEvent.click(cell);
    expect(selected(container, "n3")).toBe("true");
    expect(screen.getByTestId("opens").textContent).toBe("");
    fireEvent.click(cell);
    expect(selected(container, "n3")).toBe("false");
    expect(screen.getByTestId("opens").textContent).toBe("");
  });

  it("a tap on the box itself toggles once, not twice", () => {
    restore = coarsePointer();
    const { container } = render(<FilesPage view="list" />);
    const box = container.querySelector('[data-id="n4"] input[type="checkbox"]') as HTMLElement;
    expect(box).not.toBeNull();
    fireEvent.click(box);
    expect(selected(container, "n4")).toBe("true");
  });

  it("on a mouse there is no select cell, and a click selects the row without opening it", () => {
    const { container } = render(<FilesPage view="list" />);
    expect(container.querySelector(".ant-table-selection-column")).toBeNull();
    const row = container.querySelector('[data-id="n2"]') as HTMLElement;
    fireEvent.click(row);
    expect(selected(container, "n2")).toBe("true");
    expect(screen.getByTestId("opens").textContent).toBe("");
  });
});

describe("a preset list's select cell and ⋮ menu on a finger (mob3)", () => {
  it("a tap in the select cell toggles the row", () => {
    restore = coarsePointer();
    const { container } = render(<Listing<Row> {...presetProps()} />);
    const cell = container.querySelector('[data-id="a"] .ant-table-selection-column') as HTMLElement;
    expect(cell).not.toBeNull();
    fireEvent.click(cell);
    expect(selected(container, "a")).toBe("true");
    fireEvent.click(cell);
    expect(selected(container, "a")).toBe("false");
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
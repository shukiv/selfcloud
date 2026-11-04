// SPDX-License-Identifier: AGPL-3.0-or-later

// Row draws (slice vl, decision 78). A row draws when its type icon renders: the name's icon in the list, the card's icon in
// the grid. The icon is counted by the item's name, so the test shows which rows a change draws again. A folder of 200 rows
// is rendered; a page of 50 is added, one ArrowDown is pressed, and a parent re-render passes new callbacks with no change.

import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import type { ViewMode } from "../../lib/files/viewPrefs";
import Listing, { type ListingProps } from "./Listing";

const { draws } = vi.hoisted(() => ({ draws: new Map<string, number>() }));

vi.mock("./Icon", async (importOriginal) => {
  const { createElement } = await import("react");
  const mod = await importOriginal<typeof import("./Icon")>();
  const Counted = (props: Parameters<typeof mod.default>[0]) => {
    const name = props.node?.name;
    if (name) draws.set(name, (draws.get(name) ?? 0) + 1);
    return createElement(mod.default, props);
  };
  return { ...mod, default: Counted };
});

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

// The items are the same objects on every call, as the page's items are: a row whose item did not change keeps its object.
const ALL = Array.from({ length: 200 }, (_, i) => node(i));
const items = (count: number) => ALL.slice(0, count);

// The page passes new callbacks on each of its renders (FilesPage's inline arrows), so each call here does too.
function props(over: Partial<ListingProps>): ListingProps {
  return {
    items: [],
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
    rowActs: () => [],
    onRowContext: () => {},
    ...over,
  };
}

const drawnNames = () => [...draws.keys()].sort();
const rowOf = (id: string) => document.querySelector(`[data-id="${id}"]`) as HTMLElement;
const indexOfName = (name: string) => Number(/\d+/.exec(name)?.[0]);

afterEach(() => {
  draws.clear();
  vi.unstubAllGlobals();
});

// The finger case takes the checkbox column (the list's rowSelection) and the tiles' checkboxes.
const pointers = [
  ["a mouse", false],
  ["a finger", true],
] as const;

describe.each(pointers)("row draws, %s", (_pointer, coarse) => {
  const views: ViewMode[] = ["list", "grid"];
  const stub = () =>
    vi.stubGlobal("matchMedia", (query: string) => ({ matches: coarse && query === "(pointer: coarse)", media: query, addEventListener() {}, removeEventListener() {} }));

  describe.each(views)("%s view", (view) => {
    it("adding a page of rows draws only the new rows", () => {
      stub();
      const r = render(<Listing {...props({ view, items: items(150) })} />);
      draws.clear();
      r.rerender(<Listing {...props({ view, items: items(200) })} />);
      const old = drawnNames().filter((name) => indexOfName(name) < 150);
      expect(old).toEqual([]);
      for (let i = 150; i < 200; i++) expect(draws.get(`file-${i}.txt`) ?? 0).toBeGreaterThan(0);
    });

    it("one ArrowDown draws at most the two rows whose focus or selection changed", () => {
      stub();
      const r = render(<Listing {...props({ view, items: items(200), focusID: "n0", selected: new Set(["n0"]) })} />);
      draws.clear();
      r.rerender(<Listing {...props({ view, items: items(200), focusID: "n1", selected: new Set(["n1"]) })} />);
      // The state moved (so the check is not empty): the focus and the selection are on n1 at once, in the same render.
      expect(rowOf("n1").getAttribute("tabindex")).toBe("0");
      expect(rowOf("n0").getAttribute("tabindex")).toBe("-1");
      expect(rowOf("n1").getAttribute("aria-selected")).toBe("true");
      expect(rowOf("n0").getAttribute("aria-selected")).toBe("false");
      expect(drawnNames().length).toBeLessThanOrEqual(2);
      expect(drawnNames().every((name) => name === "file-0.txt" || name === "file-1.txt")).toBe(true);
    });

    it("a parent re-render with the same items and state draws no row", () => {
      stub();
      const r = render(<Listing {...props({ view, items: items(200), focusID: "n0", selected: new Set(["n0"]) })} />);
      draws.clear();
      r.rerender(<Listing {...props({ view, items: items(200), focusID: "n0", selected: new Set(["n0"]) })} />);
      expect(drawnNames()).toEqual([]);
    });
  });
});

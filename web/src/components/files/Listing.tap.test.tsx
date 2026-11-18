// SPDX-License-Identifier: AGPL-3.0-or-later

// A finger selects a row by a long press, and a tap toggles a row while something is selected (a screen whose main pointer is
// coarse). A touch makes its pointer events first and the mouse's events after them (mousedown, the focus, mouseup, click), so the
// focus of the press comes after the pointer's release; the selection must still be the click's (decision 128). A mouse's click on
// the row's mark is one update too, and the focus the press held is dropped, not applied a turn later.
import { useState } from "react";
import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { type ListingProps } from "./Listing";

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
const items = Array.from({ length: 8 }, (_, i) => node(i));

// setPointer makes the main pointer a finger (coarse) or a mouse, on a wide window.
let restore: () => void = () => undefined;
function setPointer(coarse: boolean) {
  const before = window.matchMedia;
  window.matchMedia = ((query: string) => ({
    matches: (coarse && query === "(pointer: coarse)") || /min-width/.test(query),
    media: query,
    onchange: null,
    addListener: () => undefined,
    removeListener: () => undefined,
    addEventListener: () => undefined,
    removeEventListener: () => undefined,
    dispatchEvent: () => false,
  })) as unknown as typeof window.matchMedia;
  restore = () => {
    window.matchMedia = before;
  };
}
afterEach(() => {
  restore();
  restore = () => undefined;
});

// Page keeps the selection and the focus as FilesPage does: a click with Ctrl toggles, and the focus follows the click. A finger's
// tap that opens (touch, nothing selected) is not a selection here.
function Page({ view }: { view: "list" | "grid" }) {
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [focus, setFocus] = useState<string | null>(null);
  const p: ListingProps = {
    items,
    view,
    selected: sel,
    focusID: focus,
    sortKey: "name",
    sortDir: "asc",
    renameID: null,
    renameValue: "",
    renameError: null,
    label: "Files",
    onRowClick: (i, e) => {
      if (e.touch) return;
      setSel((s) => {
        const next = new Set(s);
        if (next.has(items[i].id)) next.delete(items[i].id);
        else next.add(items[i].id);
        return next;
      });
      setFocus(items[i].id);
    },
    onRowDoubleClick: () => undefined,
    onSelectAll: () => undefined,
    onSort: () => undefined,
    onRenameChange: () => undefined,
    onRename: () => undefined,
    onKeyDown: () => undefined,
    onFocusItem: (i) => setFocus(items[i].id),
  };
  return <Listing {...p} />;
}

// longPress is what a browser sends for one finger's hold: the pointer's down, the hold past the long-press time, then the lift's
// mouse events and click. The selection is made at the hold; the tap that ends it selects nothing more.
function longPress(target: HTMLElement) {
  fireEvent.pointerDown(target, { pointerType: "touch", clientX: 5, clientY: 5 });
  act(() => {
    vi.advanceTimersByTime(600);
  });
  fireEvent.pointerUp(target, { pointerType: "touch" });
  fireEvent.mouseDown(target);
  act(() => target.focus());
  fireEvent.mouseUp(target);
  fireEvent.click(target);
}

// A click that lands on the row's mark must reach the mark's own handler before anything draws the list again, and the focus the
// press held is dropped with it: one update. A native listener on the region's bubble phase runs between React's capture dispatch
// and its bubble dispatch.
describe("a click on a row's mark is not preceded by a focus update (decision 128)", () => {
  it("a mouse press that holds the focus does not apply it in the click's capture phase", () => {
    setPointer(false);
    const focusBefore: number[] = [];
    let focusCalls = 0;
    function Probe() {
      const [sel, setSel] = useState<Set<string>>(new Set());
      const p: ListingProps = {
        items,
        view: "list",
        selected: sel,
        focusID: null,
        sortKey: "name",
        sortDir: "asc",
        renameID: null,
        renameValue: "",
        renameError: null,
        label: "Files",
        onRowClick: (i) => setSel((s) => new Set([...s, items[i].id])),
        onRowDoubleClick: () => undefined,
        onSelectAll: () => undefined,
        onSort: () => undefined,
        onRenameChange: () => undefined,
        onRename: () => undefined,
        onKeyDown: () => undefined,
        onFocusItem: () => {
          focusCalls += 1;
        },
      };
      return <Listing {...p} />;
    }
    const { container } = render(<Probe />);
    const mark = container.querySelector('[data-id="n2"] button.row-mark') as HTMLElement;
    expect(mark).not.toBeNull();
    const region = container.querySelector('[role="region"]') as HTMLElement;
    fireEvent.pointerDown(mark, { pointerType: "mouse" });
    fireEvent.mouseDown(mark);
    act(() => mark.focus());
    fireEvent.pointerUp(mark, { pointerType: "mouse" });
    region.addEventListener("click", () => focusBefore.push(focusCalls));
    fireEvent.click(mark);
    expect(focusBefore).toEqual([0]);
    expect((container.querySelector('[data-id="n2"]') as HTMLElement).getAttribute("aria-selected")).toBe("true");
  });
});

describe("a finger's long press selects a row on a phone (decision 128)", () => {
  it("selects the row in a list, and the focus follows it", () => {
    vi.useFakeTimers();
    try {
      setPointer(true);
      const { container } = render(<Page view="list" />);
      const row = container.querySelector('[data-id="n3"]') as HTMLElement;
      longPress(row);
      expect(row.getAttribute("aria-selected")).toBe("true");
      expect(row.getAttribute("tabindex")).toBe("0");
    } finally {
      vi.useRealTimers();
    }
  });

  it("selects a tile in the tile view", () => {
    vi.useFakeTimers();
    try {
      setPointer(true);
      const { container } = render(<Page view="grid" />);
      const tile = container.querySelector('[data-id="n3"]') as HTMLElement;
      longPress(tile);
      expect(tile.getAttribute("aria-selected")).toBe("true");
    } finally {
      vi.useRealTimers();
    }
  });

  it("with something selected, a tap on another row toggles it in, with the focus before the release", () => {
    vi.useFakeTimers();
    try {
      setPointer(true);
      const { container } = render(<Page view="list" />);
      longPress(container.querySelector('[data-id="n3"]') as HTMLElement);
      const row = container.querySelector('[data-id="n4"]') as HTMLElement;
      fireEvent.pointerDown(row, { pointerType: "touch", clientX: 5, clientY: 5 });
      fireEvent.mouseDown(row);
      act(() => row.focus());
      fireEvent.pointerUp(row, { pointerType: "touch" });
      fireEvent.mouseUp(row);
      fireEvent.click(row);
      expect(row.getAttribute("aria-selected")).toBe("true");
    } finally {
      vi.useRealTimers();
    }
  });
});

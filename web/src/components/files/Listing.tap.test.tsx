// SPDX-License-Identifier: AGPL-3.0-or-later

// A finger's tap on a row's checkbox (a screen whose main pointer is coarse) selects the row. A touch makes its pointer events
// first and the mouse's events after them (mousedown, the focus, mouseup, click), so the focus of the press comes after the
// pointer's release; the selection must still be the click's (decision 128).
import { useState } from "react";
import { act, fireEvent, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
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

// A coarse pointer on a wide window: the query for a finger matches, so the list has its checkbox column.
let restore: () => void = () => undefined;
beforeEach(() => {
  const before = window.matchMedia;
  window.matchMedia = ((query: string) => ({
    matches: query === "(pointer: coarse)" || /min-width/.test(query),
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
});
afterEach(() => restore());

// Page keeps the selection and the focus as FilesPage does: a click with Ctrl toggles, and the focus follows the click.
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

// tap is what a browser sends for one finger's tap: the pointer's down and up, then the mouse's events, with the focus.
function tap(target: HTMLElement) {
  fireEvent.pointerDown(target, { pointerType: "touch" });
  fireEvent.pointerUp(target, { pointerType: "touch" });
  fireEvent.mouseDown(target);
  act(() => target.focus());
  fireEvent.mouseUp(target);
  fireEvent.click(target);
}

// A click that lands on a controlled checkbox must reach the checkbox's own handler before anything draws the list again: React
// draws a checkbox again with its prop's value, which undoes the click the browser made on it, and its change is then not seen.
// A native listener on the region's bubble phase runs between React's capture dispatch and its bubble dispatch.
describe("a click on a row's checkbox is not preceded by a focus update (decision 128)", () => {
  it("a mouse press that holds the focus does not apply it in the click's capture phase", () => {
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
    const box = container.querySelector('[data-id="n2"] input[type="checkbox"]') as HTMLElement;
    expect(box).not.toBeNull();
    const region = container.querySelector('[role="region"]') as HTMLElement;
    fireEvent.pointerDown(box, { pointerType: "mouse" });
    fireEvent.mouseDown(box);
    act(() => box.focus());
    fireEvent.pointerUp(box, { pointerType: "mouse" });
    region.addEventListener("click", () => focusBefore.push(focusCalls));
    fireEvent.click(box);
    expect(focusBefore).toEqual([0]);
    expect((container.querySelector('[data-id="n2"]') as HTMLElement).getAttribute("aria-selected")).toBe("true");
  });
});

describe("a finger's tap on a row's checkbox on a phone (decision 128)", () => {
  it("selects the row in a list", () => {
    const { container } = render(<Page view="list" />);
    const box = container.querySelector('[data-id="n3"] input[type="checkbox"]') as HTMLElement;
    expect(box).not.toBeNull();
    tap(box);
    expect((container.querySelector('[data-id="n3"]') as HTMLElement).getAttribute("aria-selected")).toBe("true");
  });

  it("selects a tile in the tile view", () => {
    const { container } = render(<Page view="grid" />);
    const box = container.querySelector('[data-id="n3"] input[type="checkbox"]') as HTMLElement;
    expect(box).not.toBeNull();
    tap(box);
    expect((container.querySelector('[data-id="n3"]') as HTMLElement).getAttribute("aria-selected")).toBe("true");
  });

  it("selects the row when the finger's pointer events and the click both land on the checkbox, with the focus before the release", () => {
    const { container } = render(<Page view="list" />);
    const box = container.querySelector('[data-id="n4"] input[type="checkbox"]') as HTMLElement;
    fireEvent.pointerDown(box, { pointerType: "touch" });
    fireEvent.mouseDown(box);
    act(() => box.focus());
    fireEvent.pointerUp(box, { pointerType: "touch" });
    fireEvent.mouseUp(box);
    fireEvent.click(box);
    expect((container.querySelector('[data-id="n4"]') as HTMLElement).getAttribute("aria-selected")).toBe("true");
  });
});

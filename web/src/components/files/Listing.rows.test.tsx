// SPDX-License-Identifier: AGPL-3.0-or-later

import { useState } from "react";
import { act, fireEvent, render } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { type ListingProps } from "./Listing";

// renders counts how many times each row's file icon is rendered, by the row's name. The icon is the row's content, so a row
// whose content is memoised draws it again only when that row changes (decision 128).
const renders = vi.hoisted(() => ({} as Record<string, number>));
vi.mock("./Icon", async (orig) => {
  const real = await orig<typeof import("./Icon")>();
  return {
    default: (p: Parameters<typeof real.default>[0]) => {
      const name = p.node?.name ?? "";
      renders[name] = (renders[name] ?? 0) + 1;
      return real.default(p);
    },
  };
});

// buttons counts how many times each row's icon buttons are rendered, by the button's label (the ⋮ button is "More actions for
// <name>"). A row's buttons are drawn again only when that row's own state changes.
const buttons = vi.hoisted(() => ({} as Record<string, number>));
vi.mock("../IconButton", async (orig) => {
  const real = await orig<typeof import("../IconButton")>();
  const { createElement } = await import("react");
  return {
    default: (p: Parameters<typeof real.default>[0]) => {
      buttons[p.label] = (buttons[p.label] ?? 0) + 1;
      return createElement(real.default, p);
    },
  };
});

const ROWS = 50;

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

const items = Array.from({ length: ROWS }, (_, i) => node(i));

// props builds the list's props. Each call makes new handler functions, as the page does on every render of its own.
function props(selected: Set<string>, focusID: string | null = null): ListingProps {
  return {
    items,
    view: "list",
    selected,
    focusID,
    sortKey: "name",
    sortDir: "asc",
    renameID: null,
    renameValue: "",
    renameError: null,
    label: "Files",
    onRowClick: vi.fn(),
    onRowDoubleClick: () => undefined,
    onSelectAll: () => undefined,
    onSort: () => undefined,
    onRenameChange: () => undefined,
    onRename: () => undefined,
    onKeyDown: () => undefined,
    onFocusItem: () => undefined,
  };
}

const resetCounts = () => {
  for (const k of Object.keys(renders)) delete renders[k];
  for (const k of Object.keys(buttons)) delete buttons[k];
};

// pageRenders counts the renders of a page that keeps the selection and the focus, as FilesPage does.
const pageRenders = vi.hoisted(() => ({ n: 0 }));
function Page() {
  const [sel, setSel] = useState<Set<string>>(new Set());
  const [focus, setFocus] = useState<string | null>(null);
  pageRenders.n += 1;
  return (
    <Listing
      {...props(sel, focus)}
      onFocusItem={(i) => setFocus(items[i].id)}
      onRowClick={(i) => {
        setSel(new Set([items[i].id]));
        setFocus(items[i].id);
      }}
    />
  );
}

describe("Listing focus: a press on a row renders the page once (decision 128)", () => {
  it("a mouse press and its click make one page render, not one for the focus and one for the click", () => {
    const { container } = render(<Page />);
    const row = container.querySelector('[data-id="n5"]') as HTMLElement;
    pageRenders.n = 0;
    fireEvent.pointerDown(row, { pointerType: "mouse" });
    act(() => row.focus());
    fireEvent.pointerUp(row, { pointerType: "mouse" });
    fireEvent.click(row);
    expect(pageRenders.n).toBe(1);
    expect(row.getAttribute("aria-selected")).toBe("true");
  });

  it("a press on a row's ⋮ button focuses the row with its click, in one render", () => {
    const { container } = render(<Page />);
    const more = container.querySelector('[data-id="n5"] button[aria-haspopup="menu"]') as HTMLElement;
    pageRenders.n = 0;
    fireEvent.pointerDown(more, { pointerType: "mouse" });
    act(() => more.focus());
    fireEvent.pointerUp(more, { pointerType: "mouse" });
    fireEvent.click(more);
    expect(pageRenders.n).toBe(1);
    expect((container.querySelector('[data-id="n5"]') as HTMLElement).getAttribute("tabindex")).toBe("0");
  });

  it("a keyboard focus on a row still reports it to the page", () => {
    const { container } = render(<Page />);
    const row = container.querySelector('[data-id="n5"]') as HTMLElement;
    pageRenders.n = 0;
    act(() => row.focus());
    expect(pageRenders.n).toBe(1);
    expect(row.getAttribute("tabindex")).toBe("0");
  });
});

// A press released off its row (a drag, or a release on blank space) focuses the row it was pressed on, as base did: the browser
// focused that row on the press, and the click that would have chosen it is swallowed by the drag (review round 1, HIGH).
describe("Listing focus: a press released off its row (decision 128)", () => {
  it("keeps the focus on the row it was pressed on, when the release is on another row", () => {
    const { container } = render(<Page />);
    const row5 = container.querySelector('[data-id="n5"]') as HTMLElement;
    const row9 = container.querySelector('[data-id="n9"]') as HTMLElement;
    fireEvent.pointerDown(row5, { pointerType: "mouse" });
    act(() => row5.focus());
    fireEvent.pointerUp(row9, { pointerType: "mouse" });
    expect(row5.getAttribute("tabindex")).toBe("0");
  });

  it("does not hold the focus after the release: the next row the person focuses is the one that takes it", () => {
    const { container } = render(<Page />);
    const row5 = container.querySelector('[data-id="n5"]') as HTMLElement;
    const row9 = container.querySelector('[data-id="n9"]') as HTMLElement;
    fireEvent.pointerDown(row5, { pointerType: "mouse" });
    act(() => row5.focus());
    fireEvent.pointerUp(row9, { pointerType: "mouse" });
    act(() => row9.focus());
    act(() => row5.focus());
    expect(row5.getAttribute("tabindex")).toBe("0");
  });

  it("a touch that the browser cancels for a scroll does not move the focus when no focus is in the row", async () => {
    const onFocusItem = vi.fn();
    const { container } = render(<Listing {...props(new Set())} onFocusItem={onFocusItem} />);
    const row5 = container.querySelector('[data-id="n5"]') as HTMLElement;
    fireEvent.pointerDown(row5, { pointerType: "touch" });
    fireEvent.pointerCancel(row5, { pointerType: "touch" });
    await new Promise((r) => setTimeout(r, 10));
    expect(onFocusItem).not.toHaveBeenCalled();
  });
});

// A selected row's icons follow its actions: when the page's actions for the row change (an action that stops being enabled), the
// row is drawn again, so its icon goes (the row's end is memoised on the actions' signature, review round 1, MED-2).
describe("Listing rows: a row's icons follow its actions (decision 128)", () => {
  const action = (key: string, label: string, enabled: boolean) => ({ key, label, icon: <span>{label[0]}</span>, enabled, run: () => undefined });

  it("drops the icon of an action that is no longer enabled, on a selected row", () => {
    const withStar = (starOn: boolean) => ({
      ...props(new Set(["n3"]), "n3"),
      rowActs: () => [action("share", "Share", true), action("star", "Star", starOn)],
    });
    const { container, rerender } = render(<Listing {...withStar(true)} />);
    const row = container.querySelector('[data-id="n3"]') as HTMLElement;
    expect(row.querySelector('button[aria-label="Star"]')).not.toBeNull();
    rerender(<Listing {...withStar(false)} />);
    expect(row.querySelector('button[aria-label="Star"]')).toBeNull();
    expect(row.querySelector('button[aria-label="Share"]')).not.toBeNull();
  });
});

describe("Listing rows: a selection change re-renders only the rows it changes (decision 128)", () => {
  it("selecting one row draws the icon of that row only, not of the other 49", () => {
    const { rerender } = render(<Listing {...props(new Set())} />);
    resetCounts();
    rerender(<Listing {...props(new Set(["n3"]))} />);
    const others = items.filter((n) => n.id !== "n3").map((n) => renders[n.name] ?? 0);
    expect(others.filter((c) => c > 0)).toEqual([]);
    expect(renders["file-3.txt"]).toBeGreaterThan(0);
  });

  it("a click on one row, with the page re-rendering, draws the icons of the changed row only", () => {
    const { container, rerender } = render(<Listing {...props(new Set())} />);
    const row = container.querySelector('[data-id="n7"]') as HTMLElement;
    resetCounts();
    fireEvent.click(row);
    rerender(<Listing {...props(new Set(["n7"]), "n7")} />);
    const drawn = items.filter((n) => (renders[n.name] ?? 0) > 0).map((n) => n.id);
    expect(drawn).toEqual(["n7"]);
  });
});

// moreOf is how many times the ⋮ buttons of rows other than the given ones were drawn since the last reset.
const moreOfOthers = (except: string[]) =>
  items.filter((n) => !except.includes(n.id)).reduce((sum, n) => sum + (buttons[`More actions for ${n.name}`] ?? 0), 0);

describe("Listing rows: a click, a focus move and a hover leave the other rows' buttons alone (decision 128)", () => {
  it("a click on row 5 of a page that keeps the selection draws the ⋮ buttons of rows 5 and the previous focus only", () => {
    const { container } = render(<Page />);
    const row = container.querySelector('[data-id="n5"]') as HTMLElement;
    resetCounts();
    fireEvent.pointerDown(row, { pointerType: "mouse" });
    act(() => row.focus());
    fireEvent.pointerUp(row, { pointerType: "mouse" });
    fireEvent.click(row);
    expect(moreOfOthers(["n5"])).toBe(0);
    expect(buttons["More actions for file-5.txt"]).toBeGreaterThan(0);
  });

  it("a hover on a row draws no row's ⋮ button again", () => {
    const { container } = render(<Page />);
    const row = container.querySelector('[data-id="n9"]') as HTMLElement;
    resetCounts();
    fireEvent.pointerEnter(row, { pointerType: "mouse" });
    expect(moreOfOthers([])).toBe(0);
    fireEvent.pointerLeave(row, { pointerType: "mouse" });
    expect(moreOfOthers([])).toBe(0);
  });

  it("a page render with new callbacks and the same data draws no row's content again", () => {
    const { rerender } = render(<Listing {...props(new Set(["n3"]), "n3")} />);
    resetCounts();
    rerender(<Listing {...props(new Set(["n3"]), "n3")} />);
    expect(Object.values(renders).reduce((a, b) => a + b, 0)).toBe(0);
    expect(moreOfOthers([])).toBe(0);
  });
});

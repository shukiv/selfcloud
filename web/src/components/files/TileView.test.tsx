// SPDX-License-Identifier: AGPL-3.0-or-later

// The tile view (slice d1): folders first, in their own band of one-line chips; the files as cards of one height; no size on a
// tile; the star and the menu on each item; and the full name in a title.

import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import { formatBytes } from "../../lib/files/format";
import Listing, { type ListingProps } from "./Listing";

function node(id: string, name: string, over: Partial<Node> = {}): Node {
  return {
    id,
    folder_id: "f1",
    parent_id: null,
    name,
    kind: "file",
    size: 2048,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
    starred: false,
    ...over,
  };
}

function props(over: Partial<ListingProps> = {}): ListingProps {
  return {
    items: [node("d1", "Docs", { kind: "dir", mime_type: undefined }), node("n1", "plan.txt"), node("n2", "notes.txt", { starred: true })],
    view: "grid",
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
    onStar: vi.fn(),
    onRowContext: vi.fn(),
    ...over,
  };
}

describe("the tile view", () => {
  it("shows the folders first, in a band of their own, then the files in the band below", () => {
    render(<Listing {...props()} />);
    const options = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]'));
    expect(options.map((o) => o.dataset.id)).toEqual(["d1", "n1", "n2"]);
    // Two bands: the folder chip's band holds Docs and no file.
    const bands = Array.from(document.querySelectorAll<HTMLElement>('[role="listbox"] > [role="presentation"]'));
    expect(bands).toHaveLength(2);
    expect(bands[0].textContent).toContain("Docs");
    expect(bands[0].textContent).not.toContain("plan.txt");
    expect(bands[1].textContent).toContain("plan.txt");
  });

  it("does not show a file's size on its tile; the list view still does", () => {
    const size = formatBytes(2048, "en");
    const { unmount } = render(<Listing {...props()} />);
    expect(document.body.textContent).not.toContain(size);
    unmount();
    render(<Listing {...props({ view: "list" })} />);
    expect(document.body.textContent).toContain(size);
  });

  it("cuts a long name on one line, with the full name in its title", () => {
    const long = `${"a very long name ".repeat(12)}.pdf`;
    render(<Listing {...props({ items: [node("n9", long)] })} />);
    const name = document.querySelector<HTMLElement>(`[data-id="n9"] bdi[title]`) as HTMLElement;
    expect(name.getAttribute("title")).toBe(long);
    // The ellipsis is on the box that holds the name, which is a block.
    expect(name.parentElement?.style.textOverflow).toBe("ellipsis");
    expect(name.parentElement?.style.whiteSpace).toBe("nowrap");
  });

  // A name in another script keeps its own direction inside, and its box sits at the start of the row, not the far end.
  it("aligns a name at the start of its row, whatever its script", () => {
    render(<Listing {...props({ items: [node("h1", "מסמך עבודה.txt")] })} />);
    const box = document.querySelector<HTMLElement>(`[data-id="h1"] bdi`)?.parentElement as HTMLElement;
    expect(box.style.textAlign).toBe("start");
    expect(box.style.direction).toBe("inherit");
  });

  // The menu button is out of the tab order: Shift+F10 opens the item's menu from the tile. The star stays a tab stop, as a
  // list row's star is, because the item menu has no Star action and the star has no other key. Arrow keys move on from both.
  it("takes the menu button out of the tab order, keeps the star a tab stop, and lets arrow keys move on from both", () => {
    const onKeyDown = vi.fn();
    render(<Listing {...props({ onKeyDown })} />);
    const star = screen.getByRole("button", { name: "Star notes.txt" });
    const menu = screen.getByRole("button", { name: "Actions for plan.txt" });
    expect(star.tabIndex).toBe(0);
    expect(menu.tabIndex).toBe(-1);
    fireEvent.keyDown(star, { key: "ArrowDown" });
    fireEvent.keyDown(menu, { key: "ArrowRight" });
    expect(onKeyDown.mock.calls.map((c) => c[0].key)).toEqual(["ArrowDown", "ArrowRight"]);
  });

  it("gives every item a menu button, and the button opens that item's menu at the button", () => {
    const onRowContext = vi.fn();
    const onRowClick = vi.fn();
    render(<Listing {...props({ onRowContext, onRowClick })} />);
    const menu = screen.getByRole("button", { name: "Actions for plan.txt" });
    fireEvent.click(menu);
    expect(onRowContext).toHaveBeenCalledTimes(1);
    expect(onRowContext.mock.calls[0][0]).toBe("n1");
    // The click on the menu button does not select the item.
    expect(onRowClick).not.toHaveBeenCalled();
  });

  it("keeps the star on each item: a button on a chip and in a card's corner", () => {
    render(<Listing {...props()} />);
    expect(screen.getByRole("button", { name: "Star notes.txt" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Star plan.txt" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Star Docs" })).toBeTruthy();
  });

  it("gives the files cards of one height: the preview is the same for each, whatever the name", () => {
    render(<Listing {...props({ items: [node("a", "x.txt"), node("b", "y".repeat(200) + ".txt")] })} />);
    const previews = Array.from(document.querySelectorAll<HTMLElement>(".tile-preview, [data-id] > div:nth-child(2)")).filter((el) => el.style.height);
    expect(previews.length).toBeGreaterThanOrEqual(2);
    expect(new Set(previews.map((el) => el.style.height)).size).toBe(1);
  });
});

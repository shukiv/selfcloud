// SPDX-License-Identifier: AGPL-3.0-or-later

// The tile view (slice d1, its folder chips made cards by decision 239): folders first, in their own band of cards like the
// files'; every card one height; no size on a tile; the star and the menu on each item; and the full name in a title.

import { fireEvent, render, screen, within } from "@testing-library/react";
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
    onRowContext: vi.fn(),
    ...over,
  };
}

describe("the tile view", () => {
  it("shows the folders first, in a band of their own, then the files in the band below", () => {
    render(<Listing {...props()} />);
    const options = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]'));
    expect(options.map((o) => o.dataset.id)).toEqual(["d1", "n1", "n2"]);
    // Two bands: the folder band holds Docs and no file.
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

  // The menu button is out of the tab order: Shift+F10 opens the item's menu from the tile. The star is not a button on a tile
  // (decision 70); the menu's Star entry changes it. Arrow keys move on from the menu button.
  it("takes the menu button out of the tab order, and lets arrow keys move on from it", () => {
    const onKeyDown = vi.fn();
    render(<Listing {...props({ onKeyDown })} />);
    const menu = screen.getByRole("button", { name: "More actions for plan.txt" });
    expect(menu.tabIndex).toBe(-1);
    fireEvent.keyDown(menu, { key: "ArrowRight" });
    expect(onKeyDown.mock.calls.map((c) => c[0].key)).toEqual(["ArrowRight"]);
  });

  it("gives every item a menu button, and the button opens that item's menu at the button", () => {
    const onRowContext = vi.fn();
    const onRowClick = vi.fn();
    render(<Listing {...props({ onRowContext, onRowClick })} />);
    const menu = screen.getByRole("button", { name: "More actions for plan.txt" });
    fireEvent.click(menu);
    expect(onRowContext).toHaveBeenCalledTimes(1);
    expect(onRowContext.mock.calls[0][0]).toBe("n1");
    // The click on the menu button does not select the item.
    expect(onRowClick).not.toHaveBeenCalled();
  });

  // A starred tile shows a filled star after its name, on every card alike; no tile has a star button (decision 70).
  it("shows the star as a mark after the name of a starred item, and no star button on any tile", () => {
    render(<Listing {...props()} />);
    expect(screen.queryByRole("button", { name: "Star notes.txt" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Star plan.txt" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Star Docs" })).toBeNull();
    const starred = document.querySelector('[data-id="n2"]') as HTMLElement;
    expect(starred.textContent).toContain("Starred");
    expect(document.querySelector('[data-id="n1"]')?.textContent).not.toContain("Starred");
  });

  it("gives the files cards of one height: the preview is the same for each, whatever the name", () => {
    render(<Listing {...props({ items: [node("a", "x.txt"), node("b", "y".repeat(200) + ".txt")] })} />);
    const previews = Array.from(document.querySelectorAll<HTMLElement>(".tile-preview, [data-id] > div:nth-child(2)")).filter((el) => el.style.height);
    expect(previews.length).toBeGreaterThanOrEqual(2);
    expect(new Set(previews.map((el) => el.style.height)).size).toBe(1);
  });
});

// Decision 239: a folder's tile is the same card as a file's, not a one-line chip: the same header, the same preview height,
// the folder's own icon in the preview, the same selected look, and the same visitor rules. The folders keep their own band.
describe("the tile view: a folder's tile is a card (decision 239)", () => {
  const tileOf = (id: string) => document.querySelector(`[data-id="${id}"]`) as HTMLElement;
  const headerOf = (id: string) => tileOf(id).querySelector(":scope > div:nth-child(1)") as HTMLElement;
  const previewOf = (id: string) => tileOf(id).querySelector(":scope > div:nth-child(2)") as HTMLElement;

  it("gives a folder the file card's preview at the same height, with the folder's icon at 48 px inside it and no thumbnail", () => {
    render(<Listing {...props()} />);
    const preview = previewOf("d1");
    expect(preview.style.height).toBe(previewOf("n1").style.height);
    expect(preview.style.height).toBe("120px");
    const icon = preview.querySelector("svg.kind-folder");
    expect(icon).not.toBeNull();
    expect(icon?.getAttribute("width")).toBe("48");
    expect(icon?.getAttribute("height")).toBe("48");
    expect(preview.querySelector("img")).toBeNull();
  });

  it("gives a folder the file card's header: the name on one line, the star mark and the More button, at the same height", () => {
    render(<Listing {...props({ items: [node("d1", "Docs", { kind: "dir", mime_type: undefined, starred: true }), node("n1", "plan.txt")] })} />);
    const header = headerOf("d1");
    expect(header.style.minHeight).toBe(headerOf("n1").style.minHeight);
    expect(header.style.minHeight).toBe("40px");
    expect(within(header).getByText("Docs")).toBeTruthy();
    expect(header.textContent).toContain("Starred");
    expect(within(header).getByRole("button", { name: "More actions for Docs" })).toBeTruthy();
  });

  it("keeps a folder's colour mark on its card, and its rename field and error in the header", () => {
    const { unmount } = render(<Listing {...props({ items: [node("d1", "Docs", { kind: "dir", mime_type: undefined, color: "blue" })] })} />);
    expect(tileOf("d1").textContent).toContain("Colour: Blue");
    unmount();
    render(<Listing {...props({ renameID: "d1", renameValue: "Docs 2", renameError: "That name is taken." })} />);
    expect(within(headerOf("d1")).getByRole("textbox", { name: "New name" })).toBeTruthy();
    expect(within(tileOf("d1")).getByRole("alert").textContent).toBe("That name is taken.");
  });

  it("shows the check badge in the preview of a selected folder tile, with the selected tint and outline", () => {
    render(<Listing {...props({ selected: new Set(["d1"]) })} />);
    const tile = tileOf("d1");
    expect(tile.getAttribute("aria-selected")).toBe("true");
    expect(tile.style.background).not.toBe("");
    expect(tile.style.outline).toContain("2px solid");
    const badge = previewOf("d1").querySelector(".check-badge");
    expect(badge).not.toBeNull();
    expect(headerOf("d1").querySelector(".check-badge")).toBeNull();
  });

  it("keeps the folders first in their own band, as cards of the files' height", () => {
    render(<Listing {...props()} />);
    const bands = Array.from(document.querySelectorAll<HTMLElement>('[role="listbox"] > [role="presentation"]'));
    expect(bands).toHaveLength(2);
    expect(bands[0].querySelector('[role="option"]')?.getAttribute("data-id")).toBe("d1");
    const heights = Array.from(document.querySelectorAll<HTMLElement>('[role="option"]')).map((o) => (o.querySelector(":scope > div:nth-child(2)") as HTMLElement).style.height);
    expect(new Set(heights).size).toBe(1);
    // The folder tile keeps its drop attributes and its row role.
    const folder = tileOf("d1");
    expect(folder.getAttribute("data-drop-folder")).toBe("f1");
    expect(folder.getAttribute("data-drop-dir")).toBe("d1");
    expect(folder.getAttribute("role")).toBe("option");
  });

  it("gives a visitor's folder tile the visitor rules: no drop, no More, no star or badge, the row icons in their place", () => {
    render(
      <Listing
        {...props({
          visitor: true,
          selected: new Set(["d1"]),
          items: [node("d1", "Docs", { kind: "dir", mime_type: undefined, starred: true }), node("n1", "plan.txt")],
          rowActs: (n) => (n.kind === "dir" ? [{ key: "open", label: "Open", icon: <svg aria-hidden="true" />, enabled: true, run: vi.fn() }] : []),
        })}
      />,
    );
    const folder = tileOf("d1");
    expect(folder.hasAttribute("data-drop-folder")).toBe(false);
    expect(folder.hasAttribute("data-drop-dir")).toBe(false);
    expect(screen.queryByRole("button", { name: "More actions for Docs" })).toBeNull();
    expect(folder.textContent).not.toContain("Starred");
    expect(folder.querySelector(".check-badge")).toBeNull();
    expect(within(headerOf("d1")).getByRole("button", { name: "Open" })).toBeTruthy();
    expect(previewOf("d1").querySelector("svg.kind-folder")).not.toBeNull();
    expect(previewOf("d1").querySelector("img")).toBeNull();
  });
});

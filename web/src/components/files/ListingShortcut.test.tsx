// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { type ListingProps } from "./Listing";

function node(id: string, name: string, over: Partial<Node> = {}): Node {
  return {
    id,
    folder_id: "fld_1",
    parent_id: null,
    name,
    kind: "file",
    size: 2048,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
    ...over,
  };
}

function props(items: Node[], view: ListingProps["view"] = "list"): ListingProps {
  return {
    items,
    view,
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
  };
}

describe("Listing: shortcuts and folder colours", () => {
  it("marks a reachable shortcut as a shortcut, with no size, in both views", () => {
    const sc = node("nod_sc", "plan link", { kind: "shortcut", size: 0, unavailable: false, target: { id: "nod_t", kind: "file" } });
    for (const view of ["list", "grid"] as const) {
      const { unmount } = render(<Listing {...props([sc], view)} />);
      expect(screen.getByText("plan link")).toBeTruthy();
      expect(screen.getAllByText("Shortcut").length).toBeGreaterThan(0);
      expect(document.querySelector(".icon.kind-shortcut")).not.toBeNull();
      unmount();
    }
  });

  it("says an unavailable shortcut is unavailable, in text, and never shows its target's ID", () => {
    const sc = node("nod_sc", "plan link", { kind: "shortcut", size: 0, unavailable: true });
    const { container } = render(<Listing {...props([sc])} />);
    expect(screen.getByText("Unavailable")).toBeTruthy();
    expect(container.textContent).not.toContain("nod_t");
    const attrs = Array.from(container.querySelectorAll("*")).flatMap((el) => Array.from(el.attributes).map((a) => a.value));
    expect(attrs.some((v) => v.includes("nod_t"))).toBe(false);
  });

  it("names a folder's colour for screen readers and tints its icon", () => {
    const dir = node("nod_d", "Projects", { kind: "dir", color: "teal", size: 0 });
    const { container } = render(<Listing {...props([dir])} />);
    expect(screen.getByText("Colour: Teal")).toBeTruthy();
    const icon = container.querySelector(".icon.kind-folder") as HTMLElement | null;
    expect(icon).not.toBeNull();
    expect(icon?.style.color).toBe("var(--colour-teal)");
  });

  it("leaves an uncoloured folder's icon in the theme's colour, with no colour name", () => {
    const dir = node("nod_d", "Projects", { kind: "dir", size: 0 });
    const { container } = render(<Listing {...props([dir])} />);
    expect(container.querySelector(".icon.kind-folder")?.getAttribute("style")).toBeNull();
    expect(within(container).queryByText(/^Colour:/)).toBeNull();
  });
});

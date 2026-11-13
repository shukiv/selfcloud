// SPDX-License-Identifier: AGPL-3.0-or-later

import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Node } from "../../lib/files/types";
import Listing, { type ListingProps } from "./Listing";

// The owner's rule (2026-11-10, decision 168, option A): a list row's name is one line in My files' list view, at every size
// and in both densities. A long name is cut with an ellipsis, its full text is the name's title, and the marks (the link badge,
// the kind marks, the star) stay on the same line after it, in a group that does not wrap. The rename field keeps the width it
// has: its own flexible width, outside the cut name.

const LONG = `${"a very long file name that keeps on going and going ".repeat(6)}final.pdf`;

function node(id: string, name: string, extra: Partial<Node> = {}): Node {
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
    ...extra,
  };
}

const items: Node[] = [
  node("short", "short.txt"),
  node("long", LONG, { has_link: true, starred: true }),
  node("hebrew", "שם קובץ ארוך מאוד בעברית ".repeat(10) + ".docx"),
];

function props(over: Partial<ListingProps> = {}): ListingProps {
  return {
    items,
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

// setCoarse answers the pointer query as a finger (true) or a mouse (false), as a test's window does.
function setCoarse(coarse: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: coarse && query === "(pointer: coarse)",
    media: query,
    addEventListener() {},
    removeEventListener() {},
  }));
}

function nameOf(container: HTMLElement, id: string): HTMLElement {
  const el = container.querySelector<HTMLElement>(`[data-id="${id}"] .row-name`);
  if (!el) throw new Error(`no name cell for ${id}`);
  return el;
}

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("a list row's name is one line (decision 168)", () => {
  it.each([false, true])("a long name is cut on one line with an ellipsis (coarse pointer: %s)", (coarse) => {
    setCoarse(coarse);
    const { container } = render(<Listing {...props()} />);
    const name = nameOf(container, "long");
    expect(name.style.whiteSpace).toBe("nowrap");
    expect(name.style.overflow).toBe("hidden");
    expect(name.style.textOverflow).toBe("ellipsis");
  });

  it("the name's title is the full name, for a long name, a short one and a Hebrew one", () => {
    const { container } = render(<Listing {...props()} />);
    expect(nameOf(container, "long").title).toBe(LONG);
    expect(nameOf(container, "short").title).toBe("short.txt");
    expect(nameOf(container, "hebrew").title).toBe(items[2].name);
  });

  it("a name is never allowed to break at its characters or into several lines", () => {
    const { container } = render(<Listing {...props()} />);
    for (const id of ["short", "long", "hebrew"]) {
      const bdi = nameOf(container, id).querySelector<HTMLElement>("bdi");
      expect(bdi).not.toBeNull();
      expect(bdi!.style.overflowWrap).not.toBe("anywhere");
      expect(bdi!.style.wordBreak).not.toBe("break-all");
    }
  });

  it("the marks sit after the name in one group that does not wrap, and keep their words", () => {
    const { container } = render(<Listing {...props()} />);
    const row = container.querySelector<HTMLElement>('[data-id="long"]')!;
    const name = nameOf(container, "long");
    const marks = row.querySelector<HTMLElement>(".row-marks");
    expect(marks).not.toBeNull();
    // flex: none, as jsdom writes it back (0 0 auto): the group keeps its own width.
    expect(["none", "0 0 auto"]).toContain(marks!.style.flex);
    expect(marks!.style.whiteSpace).toBe("nowrap");
    // The name comes first, then the marks, in the same cell.
    expect(name.compareDocumentPosition(marks!) & globalThis.Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(name.contains(marks!)).toBe(false);
    expect(marks!.textContent).toContain("Has a link");
    expect(marks!.textContent).toContain("Starred");
  });

  it("the name shrinks to make room: it takes the space the marks leave, and cannot push them out", () => {
    const { container } = render(<Listing {...props()} />);
    const name = nameOf(container, "long");
    // A flex item that may shrink to nothing, so an ellipsis can take the room, and that starts from zero, so the marks
    // are placed first and the name takes what is left.
    expect(name.style.minWidth).toBe("0px");
    expect(name.style.flex).toMatch(/^1 1 0/);
  });

  it("the kind marks (play, editing, an unavailable shortcut) are in the same group after the name", () => {
    const clip = node("clip", "clip.mp4", { kind: "file", mime_type: "video/mp4", editing: true });
    const gone = node("gone", "old shortcut", { kind: "shortcut", unavailable: true });
    const { container } = render(<Listing {...props({ items: [...items, clip, gone] })} />);
    const clipMarks = container.querySelector<HTMLElement>('[data-id="clip"] .row-marks');
    expect(clipMarks).not.toBeNull();
    expect(clipMarks!.querySelector('[data-testid="play-mark"]')).not.toBeNull();
    expect(clipMarks!.querySelector('[data-testid="editing-mark"]')).not.toBeNull();
    const goneMarks = container.querySelector<HTMLElement>('[data-id="gone"] .row-marks');
    expect(goneMarks).not.toBeNull();
    expect(goneMarks!.querySelector(".ant-tag")).not.toBeNull();
    expect(nameOf(container, "clip").contains(clipMarks)).toBe(false);
    expect(nameOf(container, "gone").contains(goneMarks)).toBe(false);
  });

  it("the rename error keeps its own line, outside the cut name, and the field keeps its width", () => {
    const { container } = render(<Listing {...props({ renameID: "long", renameValue: "taken", renameError: "Name is taken" })} />);
    const input = container.querySelector<HTMLInputElement>('[data-id="long"] input.rename');
    expect(input).not.toBeNull();
    expect(input!.style.flex).toBe("1 1 160px");
    expect(input!.style.minWidth).toBe("0px");
    const error = container.querySelector<HTMLElement>('[data-id="long"] #rename-error');
    expect(error).not.toBeNull();
    expect(error!.style.flexBasis).toBe("100%");
    expect(error!.closest(".row-name")).toBeNull();
    expect(container.querySelector('[data-id="long"] .row-name')).toBeNull();
    expect(input!.compareDocumentPosition(error!) & globalThis.Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("the rename field keeps its own flexible width, outside the cut name", () => {
    const { container } = render(<Listing {...props({ renameID: "long", renameValue: "new name" })} />);
    const input = container.querySelector<HTMLInputElement>('[data-id="long"] input.rename');
    expect(input).not.toBeNull();
    expect(input!.style.flex).toBe("1 1 160px");
    expect(input!.style.minWidth).toBe("0px");
    // The field is not inside the cut name: its text is not clipped by the ellipsis.
    expect(input!.closest(".row-name")).toBeNull();
  });
});

describe("one row height per density, with a long name in it (decision 168)", () => {
  it.each([false, true])("every row's name is one line in the same way (coarse pointer: %s)", (coarse) => {
    setCoarse(coarse);
    const { container } = render(<Listing {...props()} />);
    const styles = items.map((n) => {
      const el = nameOf(container, n.id);
      return `${el.style.whiteSpace}|${el.style.overflow}|${el.style.textOverflow}|${el.style.minWidth}`;
    });
    expect(new Set(styles).size).toBe(1);
    expect(styles[0]).toBe("nowrap|hidden|ellipsis|0px");
  });
});

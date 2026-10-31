// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Node } from "../../lib/files/types";
import KindMarks from "./KindMarks";

function node(over: Partial<Node>): Node {
  return {
    id: "nod_1",
    folder_id: "fld_1",
    parent_id: "nod_root",
    name: "thing",
    kind: "file",
    size: 0,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
    ...over,
  };
}

describe("KindMarks", () => {
  it("says a shortcut is a shortcut, in text", () => {
    render(<KindMarks node={node({ kind: "shortcut", unavailable: false, target: { id: "nod_t", kind: "file" } })} />);
    expect(screen.getByText("Shortcut")).toBeTruthy();
    expect(screen.queryByText("Unavailable")).toBeNull();
  });

  it("marks an unavailable shortcut Unavailable, in text", () => {
    render(<KindMarks node={node({ kind: "shortcut", unavailable: true })} />);
    expect(screen.getByText("Unavailable")).toBeTruthy();
  });

  it("names a folder's palette colour for screen readers", () => {
    render(<KindMarks node={node({ kind: "dir", color: "teal" })} />);
    expect(screen.getByText("Colour: Teal")).toBeTruthy();
  });

  it("names nothing for a colour that is not on the palette", () => {
    const { container } = render(<KindMarks node={node({ kind: "dir", color: "#ff0000" })} />);
    expect(container.textContent).toBe("");
  });

  it("marks nothing on an ordinary file", () => {
    const { container } = render(<KindMarks node={node({ kind: "file" })} />);
    expect(container.textContent).toBe("");
  });
});

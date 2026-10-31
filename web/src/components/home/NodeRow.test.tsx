// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { Node } from "../../lib/files/types";
import NodeRow from "./NodeRow";

function node(over: Partial<Node>): Node {
  return {
    id: "nod_sc",
    folder_id: "fld_1",
    parent_id: "nod_root",
    name: "Report link",
    kind: "shortcut",
    size: 0,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "viewer",
    no_download: false,
    editors_can_share: false,
    ...over,
  };
}

describe("NodeRow for a shortcut", () => {
  it("shows a reachable shortcut as a shortcut, and opens its target, not the shortcut", () => {
    render(<NodeRow node={node({ unavailable: false, target: { id: "nod_target", kind: "file" } })} meta="today" />);
    expect(screen.getByText("Shortcut")).toBeTruthy();
    expect(screen.getByRole("link").getAttribute("href")).toBe("/n/nod_target");
  });

  it("shows an unavailable shortcut as unavailable, with no link to it and no target ID", () => {
    const { container } = render(<NodeRow node={node({ unavailable: true })} meta="today" />);
    expect(screen.getByText("Unavailable")).toBeTruthy();
    expect(container.querySelector("a")).toBeNull();
    expect(container.textContent).not.toContain("nod_target");
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { planNodeLink } from "./nodeLink";
import type { Node } from "./types";

function node(over: Partial<Node>): Node {
  return {
    id: "nod_shortcut",
    folder_id: "fld_1",
    parent_id: "nod_dir",
    name: "plan link",
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

describe("planNodeLink for a shortcut", () => {
  it("never opens a shortcut as a directory of its own, even when its folder is out of reach", () => {
    const plan = planNodeLink(node({}), null);
    expect(plan.kind).not.toBe("in-folder");
    expect(JSON.stringify(plan)).not.toContain('"dir":"nod_shortcut"');
  });

  it("selects a shortcut in its folder, as a row and not a preview", () => {
    const parent = node({ id: "nod_dir", kind: "dir", parent_id: "nod_root", name: "Dir" });
    expect(planNodeLink(node({}), parent)).toEqual({
      kind: "in-folder",
      folder: "fld_1",
      dir: "nod_dir",
      select: "nod_shortcut",
    });
  });
});

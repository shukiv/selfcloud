// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import type { Node } from "./types";
import { openShortcut } from "./shortcut";
import { sortNodes } from "./sort";

function shortcut(over: Partial<Node> = {}): Node {
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
    unavailable: true,
    ...over,
  };
}

describe("sortNodes with shortcuts", () => {
  it("lists folders, then shortcuts, then files, whatever the key and the direction", () => {
    const items: Node[] = [
      shortcut({ id: "a", name: "aaa link" }),
      shortcut({ id: "f", name: "zzz", kind: "file" as Node["kind"], unavailable: undefined }),
      shortcut({ id: "d", name: "mmm", kind: "dir", unavailable: undefined }),
    ];
    for (const dir of ["asc", "desc"] as const) {
      const order = sortNodes(items, "name", dir, "en").map((n) => n.kind);
      expect(order).toEqual(["dir", "shortcut", "file"]);
    }
  });
});

describe("openShortcut", () => {
  it("opens a reachable shortcut at its target's address", () => {
    expect(openShortcut(shortcut({ unavailable: false, target: { id: "nod_target", kind: "file" } }))).toEqual({
      kind: "go",
      to: "/n/nod_target",
    });
  });

  it("opens an unavailable shortcut on its own: a request for access, with no target", () => {
    const got = openShortcut(shortcut());
    expect(got).toEqual({ kind: "unavailable" });
    expect(JSON.stringify(got)).not.toContain("nod_target");
  });

  it("treats a shortcut without a target as unavailable, however the API answers", () => {
    expect(openShortcut(shortcut({ unavailable: false, target: undefined }))).toEqual({ kind: "unavailable" });
  });
});

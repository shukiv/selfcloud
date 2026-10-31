// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it } from "vitest";
import { keepTargetOnSave, savedNode } from "./appearance";
import type { Node } from "./types";

function dir(over: Partial<Node> = {}): Node {
  return {
    id: "nod_a",
    folder_id: "fld_1",
    parent_id: "nod_root",
    name: "Projects",
    kind: "dir",
    size: 0,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "editor",
    no_download: false,
    editors_can_share: false,
    ...over,
  };
}

describe("savedNode", () => {
  it("takes a cleared colour and description from the answer, which omits them", () => {
    const old = dir({ color: "teal", description: "Notes" });
    // The server leaves an empty field out of the answer, so the old value must not survive the spread.
    const got = savedNode(old, dir({ color: undefined, description: undefined, modified_at: "2026-10-02T00:00:00Z" }));
    expect(got.color).toBeUndefined();
    expect(got.description).toBeUndefined();
    expect(got.modified_at).toBe("2026-10-02T00:00:00Z");
  });

  it("keeps the caller's role when the answer carries none", () => {
    const got = savedNode(dir({ role: "manager" }), dir({ role: undefined }));
    expect(got.role).toBe("manager");
  });
});

describe("keepTargetOnSave", () => {
  it("applies a save to the item still open in the drawer", () => {
    const saved = dir({ color: "red" });
    expect(keepTargetOnSave(dir(), saved, true)).toBe(saved);
  });

  it("does not let a late save overwrite another item the drawer now shows", () => {
    const b = dir({ id: "nod_b", name: "Other" });
    expect(keepTargetOnSave(b, dir({ color: "red" }), true)).toBe(b);
  });

  it("applies nothing once the drawer is closed", () => {
    const a = dir();
    expect(keepTargetOnSave(a, dir({ color: "red" }), false)).toBe(a);
  });
});

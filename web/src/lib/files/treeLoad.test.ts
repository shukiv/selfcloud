// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import { loadDirs } from "./treeLoad";
import type { Node, Page } from "./types";

function n(id: string, kind: Node["kind"]): Node {
  return { id, folder_id: "f", parent_id: null, name: id, kind, size: 0, created_at: "", modified_at: "", no_download: false, editors_can_share: false };
}

describe("loadDirs", () => {
  it("reads the folders of a place and stops at the first file", async () => {
    const read = vi.fn(async (): Promise<Page<Node>> => ({ items: [n("a", "dir"), n("b", "dir"), n("c", "file")], next_cursor: "more" }));
    expect(await loadDirs(read)).toEqual({ dirs: [n("a", "dir"), n("b", "dir")], capped: false });
    expect(read).toHaveBeenCalledTimes(1);
  });

  it("follows the pages while they are all folders, and passes the cursor", async () => {
    const read = vi.fn(async (cursor?: string): Promise<Page<Node>> =>
      cursor === undefined ? { items: [n("a", "dir")], next_cursor: "p2" } : { items: [n("b", "dir")], next_cursor: undefined },
    );
    const got = await loadDirs(read);
    expect(got.dirs.map((d) => d.id)).toEqual(["a", "b"]);
    expect(read.mock.calls.map((c) => c[0])).toEqual([undefined, "p2"]);
  });

  it("stops at the cap and says there are more", async () => {
    const read = vi.fn(async (): Promise<Page<Node>> => ({ items: [n("x", "dir")], next_cursor: "again" }));
    const got = await loadDirs(read, 3);
    expect(got.capped).toBe(true);
    expect(got.dirs).toHaveLength(3);
    expect(read).toHaveBeenCalledTimes(3);
  });

  it("passes a failed read on, for the tree to show", async () => {
    await expect(loadDirs(async () => Promise.reject(new Error("no")))).rejects.toThrow("no");
  });

  it("leaves a shortcut out of the folders", async () => {
    const got = await loadDirs(async () => ({ items: [n("a", "dir"), n("s", "shortcut")], next_cursor: undefined }));
    expect(got.dirs.map((d) => d.id)).toEqual(["a"]);
  });
});

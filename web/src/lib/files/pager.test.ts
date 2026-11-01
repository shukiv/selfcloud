// SPDX-License-Identifier: AGPL-3.0-or-later

import { describe, expect, it, vi } from "vitest";
import type { Node } from "./types";
import { PAGER_LIMIT, Pager, type PageLike } from "./pager";

function file(name: string, mime = "video/webm"): Node {
  return {
    id: name,
    folder_id: "f",
    parent_id: "d",
    name,
    kind: "file",
    size: 1,
    mime_type: mime,
    created_at: "2026-10-31T00:00:00Z",
    modified_at: "2026-10-31T00:00:00Z",
    no_download: false,
    editors_can_share: false,
  };
}

describe("Pager", () => {
  it("reads no page until one is asked for", () => {
    const fetchPage = vi.fn(async (): Promise<PageLike> => ({ items: [] }));
    const p = new Pager([file("a.webm")], "c1", fetchPage);
    expect(fetchPage).not.toHaveBeenCalled();
    expect(p.hasMore).toBe(true);
    expect(p.loaded.map((n) => n.name)).toEqual(["a.webm"]);
  });

  it("reads one page per ask, keeps the playable files, and ends at the last page", async () => {
    const pages: Record<string, PageLike> = {
      c1: { items: [file("b.webm"), file("notes.txt", "text/plain"), file("c.mp3", "audio/mpeg")], next_cursor: "c2" },
      c2: { items: [file("d.webm")] },
    };
    const fetchPage = vi.fn(async (c: string) => pages[c]);
    const p = new Pager([], "c1", fetchPage);
    const first = await p.more();
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(first).toEqual({ kind: "page", items: [file("b.webm"), file("c.mp3", "audio/mpeg")] });
    expect(p.hasMore).toBe(true);
    await p.more();
    expect(p.hasMore).toBe(false);
    expect(await p.more()).toEqual({ kind: "end" });
    expect(fetchPage).toHaveBeenCalledTimes(2);
  });

  it("answers two asks made together with one read", async () => {
    const fetchPage = vi.fn(async (): Promise<PageLike> => ({ items: [file("a.webm")] }));
    const p = new Pager([], "c1", fetchPage);
    const [x, y] = await Promise.all([p.more(), p.more()]);
    expect(fetchPage).toHaveBeenCalledTimes(1);
    expect(x).toEqual(y);
  });

  it("says a page that could not be read is failed, not the end, and asks for the same page again", async () => {
    let ok = false;
    const fetchPage = vi.fn(async (): Promise<PageLike> => {
      if (!ok) throw new Error("offline");
      return { items: [file("late.webm")] };
    });
    const p = new Pager([], "c1", fetchPage);
    expect(await p.more()).toEqual({ kind: "failed" });
    // The folder still has its page: the failure is not an end, and nothing moved.
    expect(p.hasMore).toBe(true);
    expect(p.cursor).toBe("c1");
    expect(await p.more()).toEqual({ kind: "failed" });
    expect(fetchPage).toHaveBeenNthCalledWith(2, "c1");
    // A retry that works brings the page.
    ok = true;
    expect(await p.more()).toEqual({ kind: "page", items: [file("late.webm")] });
    expect(p.loaded.map((n) => n.name)).toEqual(["late.webm"]);
  });

  it("holds no more than the limit", async () => {
    const seed = Array.from({ length: PAGER_LIMIT }, (_, i) => file(`f${i}.webm`));
    const fetchPage = vi.fn(async (): Promise<PageLike> => ({ items: [] }));
    const p = new Pager(seed, "c1", fetchPage);
    expect(p.hasMore).toBe(false);
    expect(await p.more()).toEqual({ kind: "end" });
    expect(fetchPage).not.toHaveBeenCalled();
  });
});

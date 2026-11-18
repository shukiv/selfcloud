// SPDX-License-Identifier: AGPL-3.0-or-later

// The bookmark rules the player applies: the order a list is shown in, and the four routes' paths and bodies. The api
// function is mocked, so these judge what the client sends, not the server (decision 218 owns the server half).

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../api", async (orig) => ({ ...(await orig<typeof import("../api")>()), api: vi.fn() }));

import { api } from "../api";
import { session, type Session } from "../session";
import { addBookmark, deleteBookmark, listBookmarks, relabelBookmark, type Bookmark } from "./api";
import { BOOKMARK_LIMIT, LABEL_MAX, sortBookmarks } from "./bookmarks";

const b = (over: Partial<Bookmark> = {}): Bookmark => ({
  id: "bmk_1",
  position_seconds: 10,
  label: "",
  created_at: "2026-11-16T00:00:00Z",
  ...over,
});

beforeEach(() => {
  session.set({ user: { id: "u1" }, account: null, csrf: "csrf", publicLinks: false } as unknown as Session);
  vi.mocked(api).mockReset();
});

afterEach(() => {
  session.set(null);
});

describe("sortBookmarks", () => {
  it("orders by position, then created_at, then id, into a new array", () => {
    const input = [
      b({ id: "bmk_c", position_seconds: 5, created_at: "2026-11-16T00:00:02Z" }),
      b({ id: "bmk_b", position_seconds: 5, created_at: "2026-11-16T00:00:01Z" }),
      b({ id: "bmk_a", position_seconds: 5, created_at: "2026-11-16T00:00:01Z" }),
      b({ id: "bmk_z", position_seconds: 1 }),
      b({ id: "bmk_y", position_seconds: 9 }),
    ];
    const out = sortBookmarks(input);
    expect(out.map((x) => x.id)).toEqual(["bmk_z", "bmk_a", "bmk_b", "bmk_c", "bmk_y"]);
    expect(out).not.toBe(input);
    // The array it was given is untouched.
    expect(input.map((x) => x.id)).toEqual(["bmk_c", "bmk_b", "bmk_a", "bmk_z", "bmk_y"]);
  });

  it("keeps the cap and the label's length", () => {
    expect(BOOKMARK_LIMIT).toBe(200);
    expect(LABEL_MAX).toBe(100);
  });
});

describe("the bookmark routes", () => {
  it("listBookmarks reads the file's bookmarks and returns the list", async () => {
    vi.mocked(api).mockResolvedValue({ bookmarks: [b(), b({ id: "bmk_2" })] } as never);
    const out = await listBookmarks("nod_1");
    expect(api).toHaveBeenCalledWith("GET", "/api/v1/nodes/nod_1/bookmarks");
    expect(out.map((x) => x.id)).toEqual(["bmk_1", "bmk_2"]);
  });

  it("addBookmark posts the position and the label", async () => {
    vi.mocked(api).mockResolvedValue(b() as never);
    await addBookmark("nod_1", 12.5, "Intro");
    expect(api).toHaveBeenCalledWith("POST", "/api/v1/nodes/nod_1/bookmarks", { body: { position_seconds: 12.5, label: "Intro" } });
  });

  it("relabelBookmark patches the label, encoding the bookmark id", async () => {
    vi.mocked(api).mockResolvedValue(b() as never);
    await relabelBookmark("nod_1", "bmk/a b", "New");
    expect(api).toHaveBeenCalledWith("PATCH", "/api/v1/nodes/nod_1/bookmarks/bmk%2Fa%20b", { body: { label: "New" } });
  });

  it("deleteBookmark deletes the bookmark, encoding its id", async () => {
    vi.mocked(api).mockResolvedValue(undefined as never);
    await deleteBookmark("nod_1", "bmk/a b");
    expect(api).toHaveBeenCalledWith("DELETE", "/api/v1/nodes/nod_1/bookmarks/bmk%2Fa%20b");
  });
});

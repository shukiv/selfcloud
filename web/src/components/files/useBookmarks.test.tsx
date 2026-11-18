// SPDX-License-Identifier: AGPL-3.0-or-later

// The player's bookmark hook: it reads a file's list when the file changes, drops an answer for a file no longer current,
// and keeps decision 222's rule — a session that has changed sends nothing and uses no answer. add posts the rounded place
// with an empty label and inserts the answer in order; the cap and a failure each say their own words.

import { act, renderHook, waitFor } from "@testing-library/react";
import { App } from "antd";
import type { ReactNode } from "react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/api";
import { loadMedia } from "../../lib/media/i18n";
import { session, type Session } from "../../lib/session";
import type { Bookmark } from "../../lib/files/api";
import { BOOKMARK_LIMIT, type BookmarkApi } from "../../lib/files/bookmarks";
import { useBookmarks } from "./useBookmarks";

const b = (over: Partial<Bookmark> = {}): Bookmark => ({
  id: "bmk_1",
  position_seconds: 10,
  label: "",
  created_at: "2026-11-16T00:00:00Z",
  ...over,
});

function signIn(id = "u1") {
  session.set({ user: { id }, account: null, csrf: "csrf", publicLinks: false } as unknown as Session);
}

function media(at = 0): HTMLMediaElement {
  const m = document.createElement("audio");
  Object.defineProperty(m, "currentTime", { value: at, writable: true, configurable: true });
  m.setAttribute("aria-label", "song.mp3");
  return m;
}

function mockApi(): BookmarkApi {
  return {
    list: vi.fn(async () => [] as Bookmark[]),
    add: vi.fn(async () => b()),
    relabel: vi.fn(async () => b()),
    remove: vi.fn(async () => {}),
  };
}

const wrapper = ({ children }: { children: ReactNode }) => <App>{children}</App>;

function draw(api: BookmarkApi | undefined, nodeId: string | null, m: HTMLMediaElement | null) {
  return renderHook(({ a, n, el }) => useBookmarks(a, n, el), {
    initialProps: { a: api, n: nodeId, el: m },
    wrapper,
  });
}

const notices = () => [...document.querySelectorAll(".ant-message-notice")].map((n) => n.textContent ?? "");

beforeAll(async () => {
  await loadMedia("en");
});

beforeEach(() => {
  signIn();
});

afterEach(() => {
  session.set(null);
  vi.restoreAllMocks();
});

describe("reading the list", () => {
  it("reads when the file changes and shows the answer in order", async () => {
    const api = mockApi();
    vi.mocked(api.list).mockResolvedValueOnce([b({ id: "bmk_late", position_seconds: 30 }), b({ id: "bmk_early", position_seconds: 2 })]);
    const { result, rerender } = draw(api, "nod_1", media());
    await waitFor(() => expect(result.current.marks?.ready).toBe(true));
    expect(api.list).toHaveBeenCalledWith("nod_1");
    expect(result.current.marks?.items.map((x) => x.id)).toEqual(["bmk_early", "bmk_late"]);

    vi.mocked(api.list).mockResolvedValueOnce([b({ id: "bmk_other", position_seconds: 7 })]);
    rerender({ a: api, n: "nod_2", el: media() });
    await waitFor(() => expect(result.current.marks?.items.map((x) => x.id)).toEqual(["bmk_other"]));
    expect(api.list).toHaveBeenCalledWith("nod_2");
  });

  it("drops an answer for a file no longer current", async () => {
    const api = mockApi();
    let resolveOld: (v: Bookmark[]) => void = () => {};
    vi.mocked(api.list).mockImplementationOnce(() => new Promise<Bookmark[]>((res) => { resolveOld = res; }));
    vi.mocked(api.list).mockResolvedValueOnce([b({ id: "bmk_new", position_seconds: 5 })]);
    const { result, rerender } = draw(api, "nod_1", media());
    rerender({ a: api, n: "nod_2", el: media() });
    await waitFor(() => expect(result.current.marks?.items.map((x) => x.id)).toEqual(["bmk_new"]));
    // The first file's answer arrives late; it must not replace the second file's list.
    await act(async () => {
      resolveOld([b({ id: "bmk_old", position_seconds: 99 })]);
    });
    expect(result.current.marks?.items.map((x) => x.id)).toEqual(["bmk_new"]);
  });

  it("makes no request and gives no marks without an api", () => {
    const { result } = draw(undefined, "nod_1", media());
    expect(result.current.marks).toBeUndefined();
    expect(result.current.dialog).toBeNull();
  });
});

describe("the session's rule (decision 222)", () => {
  it("sends nothing once the session key has changed", async () => {
    const api = mockApi();
    const { result } = draw(api, "nod_1", media());
    await waitFor(() => expect(result.current.marks?.ready).toBe(true));
    signIn("someone-else");
    act(() => {
      result.current.marks?.add();
    });
    expect(api.add).not.toHaveBeenCalled();
    expect(notices()).toEqual([]);
  });

  it("uses no answer and shows no message when the key changes after the send", async () => {
    const api = mockApi();
    let resolveAdd: (v: Bookmark) => void = () => {};
    vi.mocked(api.add).mockImplementationOnce(() => new Promise<Bookmark>((res) => { resolveAdd = res; }));
    const { result } = draw(api, "nod_1", media());
    await waitFor(() => expect(result.current.marks?.ready).toBe(true));
    act(() => {
      result.current.marks?.add();
    });
    expect(api.add).toHaveBeenCalledTimes(1);
    signIn("someone-else");
    await act(async () => {
      resolveAdd(b({ id: "bmk_late" }));
    });
    expect(result.current.marks?.items).toEqual([]);
    expect(notices()).toEqual([]);
  });
});

describe("add", () => {
  it("posts the rounded place with an empty label and inserts the answer in order", async () => {
    const api = mockApi();
    vi.mocked(api.list).mockResolvedValue([b({ id: "bmk_later", position_seconds: 40 })]);
    vi.mocked(api.add).mockResolvedValue(b({ id: "bmk_new", position_seconds: 12.3 }));
    const { result } = draw(api, "nod_1", media(12.34));
    await waitFor(() => expect(result.current.marks?.ready).toBe(true));
    act(() => {
      result.current.marks?.add();
    });
    expect(api.add).toHaveBeenCalledWith("nod_1", 12.3, "");
    await waitFor(() => expect(result.current.marks?.items.map((x) => x.id)).toEqual(["bmk_new", "bmk_later"]));
    await waitFor(() => expect(notices().some((t) => t.includes("Bookmark added at 0:12"))).toBe(true));
  });

  it("says the cap's words on a 409 with reason limit", async () => {
    const api = mockApi();
    vi.mocked(api.add).mockRejectedValue(new ApiError(409, "conflict", { reason: "limit", limit: BOOKMARK_LIMIT }, 0));
    const { result } = draw(api, "nod_1", media());
    await waitFor(() => expect(result.current.marks?.ready).toBe(true));
    act(() => {
      result.current.marks?.add();
    });
    await waitFor(() => expect(notices().some((t) => t.includes(`A file holds at most ${BOOKMARK_LIMIT} bookmarks`))).toBe(true));
    expect(result.current.marks?.items).toEqual([]);
  });

  it("says the save-failure words on any other refusal", async () => {
    const api = mockApi();
    vi.mocked(api.add).mockRejectedValue(new ApiError(500, "internal", {}, 0));
    const { result } = draw(api, "nod_1", media());
    await waitFor(() => expect(result.current.marks?.ready).toBe(true));
    act(() => {
      result.current.marks?.add();
    });
    await waitFor(() => expect(notices().some((t) => t.includes("Could not save the bookmark"))).toBe(true));
  });

  it("is full at the cap", async () => {
    const api = mockApi();
    vi.mocked(api.list).mockResolvedValue(Array.from({ length: BOOKMARK_LIMIT }, (_, i) => b({ id: `bmk_${i}`, position_seconds: i })));
    const { result } = draw(api, "nod_1", media());
    await waitFor(() => expect(result.current.marks?.full).toBe(true));
    expect(result.current.marks?.items.length).toBe(BOOKMARK_LIMIT);
  });
});

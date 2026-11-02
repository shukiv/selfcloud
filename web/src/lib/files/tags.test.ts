// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The tags cache asks the server through api; the signed-in check reads the session store. Both are replaced here.
const apiMock = vi.hoisted(() => vi.fn());
vi.mock("../api", () => ({
  api: apiMock,
  ApiError: class extends Error {
    constructor(
      public status: number,
      public code: string,
      public body: unknown,
      public retryAfter: number,
    ) {
      super(code);
    }
  },
}));
const sessionWatch = vi.hoisted(() => ({ fn: null as ((s: unknown) => void) | null }));
vi.mock("../session", () => ({
  session: {
    subscribe: (fn: (s: unknown) => void) => {
      sessionWatch.fn = fn;
      fn({ key: "first" });
      return () => undefined;
    },
  },
  sessionKey: (s: { key: string } | null) => s?.key ?? "",
}));
vi.mock("../store", () => ({ get: () => ({ signedIn: true }) }));

import {
  MAX_BACKOFF_MS,
  MAX_DURATION_MS,
  MAX_WAITS,
  WAIT_MS,
  displayArtist,
  displayTitle,
  getTags,
  hasCover,
  loadTags,
  markCover,
  parseTagState,
  resetTags,
  seedTags,
} from "./tags";

beforeEach(() => {
  apiMock.mockReset();
  resetTags();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("parseTagState: an answer of the server, read as text", () => {
  it("reads the three states", () => {
    expect(parseTagState({ state: "pending" })).toEqual({ state: "pending" });
    expect(parseTagState({ state: "none" })).toEqual({ state: "none" });
    expect(parseTagState({ state: "ready", title: "שלום עולם", artist: "A", duration_ms: 1044 })).toEqual({
      state: "ready",
      title: "שלום עולם",
      artist: "A",
      duration_ms: 1044,
    });
  });

  it("refuses an answer it does not know", () => {
    expect(parseTagState({ state: "gone" })).toBeNull();
    expect(parseTagState(null)).toBeNull();
    expect(parseTagState("ready")).toBeNull();
  });

  it("leaves out a value that is not plain text", () => {
    // A control character, a bidirectional override and a value over the length are not shown; the other values are.
    const got = parseTagState({
      state: "ready",
      title: "a\u0007b",
      artist: "x‮y",
      album: "z".repeat(257),
      duration_ms: 5,
    });
    expect(got).toEqual({ state: "ready", duration_ms: 5 });
  });

  it("leaves out a duration that is not a whole number of milliseconds within a week", () => {
    expect(parseTagState({ state: "ready", duration_ms: -1 })).toEqual({ state: "ready" });
    expect(parseTagState({ state: "ready", duration_ms: 1.5 })).toEqual({ state: "ready" });
    expect(parseTagState({ state: "ready", duration_ms: MAX_DURATION_MS + 1 })).toEqual({ state: "ready" });
    expect(parseTagState({ state: "ready", duration_ms: MAX_DURATION_MS })).toEqual({ state: "ready", duration_ms: MAX_DURATION_MS });
  });

  it("keeps a zero-width joiner, which Arabic and Persian need", () => {
    expect(parseTagState({ state: "ready", title: "a‍b" })).toEqual({ state: "ready", title: "a‍b" });
  });
});

describe("displayTitle and displayArtist: the file name until a tag is read", () => {
  it("shows the name when no tag is read, and the title when it is", () => {
    expect(displayTitle("track01.mp3", undefined)).toBe("track01.mp3");
    expect(displayTitle("track01.mp3", { state: "none" })).toBe("track01.mp3");
    expect(displayTitle("track01.mp3", { state: "ready", artist: "A" })).toBe("track01.mp3");
    expect(displayTitle("track01.mp3", { state: "ready", title: "Song" })).toBe("Song");
    expect(displayArtist({ state: "ready", title: "Song" })).toBeUndefined();
    expect(displayArtist({ state: "ready", artist: "A" })).toBe("A");
  });
});

describe("loadTags: one batch for the audio files, answers kept", () => {
  it("stores the values of a batch and marks a node the answer leaves out as none", async () => {
    apiMock.mockResolvedValueOnce({
      items: { nod_a: { state: "ready", title: "Song", artist: "A" } },
    });
    await loadTags(["nod_a", "nod_b"]);
    expect(apiMock).toHaveBeenCalledWith("GET", "/api/v1/media/tags?ids=nod_a,nod_b");
    expect(getTags("nod_a")).toEqual({ state: "ready", title: "Song", artist: "A" });
    expect(getTags("nod_b")).toEqual({ state: "none" });
  });

  it("keeps what is shown when a refetch fails", async () => {
    vi.useFakeTimers();
    // A read still pending is shown as pending; a refetch that fails must not turn it into "none".
    apiMock.mockResolvedValueOnce({ items: { nod_a: { state: "pending" } } });
    await loadTags(["nod_a"]);
    apiMock.mockRejectedValueOnce(new Error("offline"));
    await loadTags(["nod_a"]);
    expect(getTags("nod_a")).toEqual({ state: "pending" });
  });

  it("does not ask again for a node already read", async () => {
    seedTags("nod_a", { title: "Song" });
    await loadTags(["nod_a"]);
    expect(apiMock).not.toHaveBeenCalled();
  });

  it("asks again after a failed request, backing off, and stops after the bounded number of tries", async () => {
    vi.useFakeTimers();
    apiMock.mockRejectedValue(new Error("network"));
    await loadTags(["nod_f"]);
    expect(apiMock).toHaveBeenCalledTimes(1);
    expect(getTags("nod_f")).toBeUndefined();
    await vi.advanceTimersByTimeAsync(WAIT_MS);
    expect(apiMock).toHaveBeenCalledTimes(2);
    // The second retry waits twice as long: nothing is sent in the first WAIT_MS after it.
    await vi.advanceTimersByTimeAsync(WAIT_MS);
    expect(apiMock).toHaveBeenCalledTimes(2);
    await vi.advanceTimersByTimeAsync(WAIT_MS);
    expect(apiMock).toHaveBeenCalledTimes(3);
    await vi.advanceTimersByTimeAsync(MAX_BACKOFF_MS * (MAX_WAITS + 2));
    // The first try plus one per retry, and no more than MAX_WAITS retries.
    expect(apiMock).toHaveBeenCalledTimes(MAX_WAITS + 1);
    expect(getTags("nod_f")).toBeUndefined();
  });

  // The arithmetic the server's request limit (tags.batch, 120 a minute) is sized against: a 500-track queue is 5 batches; each
  // retry round sends at most 5 more; there are MAX_WAITS rounds. So while every read is pending, the queue asks 5 + 5 x 6 = 35
  // times, and while every request fails, 35 times too (decision 56). The two are not added: one node is pending or failed.
  it("asks no more than 35 times for a 500-track queue while every read stays pending", async () => {
    vi.useFakeTimers();
    const ids = Array.from({ length: 500 }, (_, i) => `nod_${String(i).padStart(4, "0")}`);
    apiMock.mockImplementation((_m: string, url: string) => {
      const names = url.split("ids=")[1].split(",").map(decodeURIComponent);
      return Promise.resolve({ items: Object.fromEntries(names.map((n) => [n, { state: "pending" }])) });
    });
    await loadTags(ids);
    await vi.advanceTimersByTimeAsync(WAIT_MS * (MAX_WAITS + 3));
    expect(apiMock.mock.calls.length).toBe(5 + 5 * MAX_WAITS);
  });

  it("asks no more than 35 times for a 500-track queue while every request fails", async () => {
    vi.useFakeTimers();
    const ids = Array.from({ length: 500 }, (_, i) => `nod_${String(i).padStart(4, "0")}`);
    apiMock.mockRejectedValue(new Error("refused"));
    await loadTags(ids);
    await vi.advanceTimersByTimeAsync(MAX_BACKOFF_MS * (MAX_WAITS + 2));
    expect(apiMock.mock.calls.length).toBe(5 + 5 * MAX_WAITS);
  });

  it("asks a pending node again after the wait, and stops after the bounded number of waits", async () => {
    vi.useFakeTimers();
    apiMock.mockResolvedValue({ items: { nod_p: { state: "pending" } } });
    await loadTags(["nod_p"]);
    expect(getTags("nod_p")).toEqual({ state: "pending" });
    for (let i = 0; i < MAX_WAITS + 2; i++) {
      await vi.advanceTimersByTimeAsync(WAIT_MS);
    }
    // The first ask plus one per wait, and no more than that.
    expect(apiMock).toHaveBeenCalledTimes(MAX_WAITS + 1);
    expect(getTags("nod_p")).toEqual({ state: "pending" });
  });

  it("shows the values once a pending read is done", async () => {
    vi.useFakeTimers();
    apiMock
      .mockResolvedValueOnce({ items: { nod_p: { state: "pending" } } })
      .mockResolvedValueOnce({ items: { nod_p: { state: "ready", title: "Late" } } });
    await loadTags(["nod_p"]);
    await vi.advanceTimersByTimeAsync(WAIT_MS);
    expect(getTags("nod_p")).toEqual({ state: "ready", title: "Late" });
  });

  it("asks for a batch's pending nodes in one request, not one request each", async () => {
    vi.useFakeTimers();
    apiMock.mockResolvedValue({ items: { nod_p: { state: "pending" }, nod_q: { state: "pending" } } });
    await loadTags(["nod_p", "nod_q"]);
    apiMock.mockClear();
    await vi.advanceTimersByTimeAsync(WAIT_MS);
    expect(apiMock).toHaveBeenCalledTimes(1);
    expect(apiMock).toHaveBeenCalledWith("GET", "/api/v1/media/tags?ids=nod_p,nod_q");
  });

  it("does not ask twice for a node whose read is on its way", async () => {
    let done: (v: unknown) => void = () => undefined;
    apiMock.mockReturnValueOnce(new Promise((r) => (done = r)));
    const first = loadTags(["nod_a"]);
    await loadTags(["nod_a"]);
    expect(apiMock).toHaveBeenCalledTimes(1);
    done({ items: { nod_a: { state: "none" } } });
    await first;
  });

  it("does not ask again for a node the server said has none", async () => {
    apiMock.mockResolvedValueOnce({ items: { nod_a: { state: "none" } } });
    await loadTags(["nod_a"]);
    await loadTags(["nod_a"]);
    expect(apiMock).toHaveBeenCalledTimes(1);
  });

  it("splits a long list into batches of 100", async () => {
    apiMock.mockResolvedValue({ items: {} });
    await loadTags(Array.from({ length: 101 }, (_, i) => `nod_${i}`));
    expect(apiMock).toHaveBeenCalledTimes(2);
  });
});

describe("a change of person forgets what was read", () => {
  it("empties the cache and the seen covers when the session changes", () => {
    seedTags("nod_a", { title: "Song" });
    markCover("nod_a");
    sessionWatch.fn?.({ key: "second" });
    expect(getTags("nod_a")).toBeUndefined();
    expect(hasCover("nod_a")).toBe(false);
  });

  it("keeps what was read when the session is the same one", () => {
    seedTags("nod_a", { title: "Song" });
    sessionWatch.fn?.({ key: "stay" });
    seedTags("nod_a", { title: "Song" });
    sessionWatch.fn?.({ key: "stay" });
    expect(getTags("nod_a")).toEqual({ state: "ready", title: "Song" });
  });

  it("drops an answer that comes back after the change", async () => {
    let done: (v: unknown) => void = () => undefined;
    apiMock.mockReturnValueOnce(new Promise((r) => (done = r)));
    const read = loadTags(["nod_a"]);
    resetTags();
    done({ items: { nod_a: { state: "ready", title: "Theirs" } } });
    await read;
    expect(getTags("nod_a")).toBeUndefined();
  });
});

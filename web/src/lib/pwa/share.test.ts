// SPDX-License-Identifier: AGPL-3.0-or-later

// The page's copy of a share's files (decision 82, rounds 2 and 3). The page keeps the files it took from the worker in memory,
// for a time-box: sixty seconds from the page's last take of a share, or five minutes for a share taken while signed out (the
// sign-in with an emailed code). These tests drive the module against a fake worker that holds shares by id and hands each one
// over once, as the real worker does, and they run the clock with fake timers.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { clearShares, dropShare, forgetShare, takeShare, takenIDs } from "./share";

let held: Record<string, File[]> = {};
let asks = 0;
let drops = 0;

// The page's answer port: a message to it is delivered at once and unchanged. A real port copies the message, and the copy of a
// test File loses its name in this environment, so the copy is not what is under test here.
class SameProcessChannel {
  port1: { onmessage: ((e: { data: unknown }) => void) | null; close(): void } = { onmessage: null, close() {} };
  port2 = {
    postMessage: (data: unknown) => this.port1.onmessage?.({ data }),
  };
}
(globalThis as unknown as { MessageChannel: unknown }).MessageChannel = SameProcessChannel;

beforeEach(() => {
  held = {};
  asks = 0;
  drops = 0;
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      controller: {
        postMessage(msg: { type: string; id?: string }, ports?: MessagePort[]) {
          if (msg.type === "SHARE_TAKE") {
            asks += 1;
            const files = held[msg.id as string] ?? [];
            delete held[msg.id as string];
            ports![0].postMessage({ files });
          }
          if (msg.type === "SHARE_DROP") {
            drops += 1;
            delete held[msg.id as string];
          }
          if (msg.type === "SHARE_DROP_ALL") {
            drops += 1;
            held = {};
          }
        },
      },
    },
  });
});

afterEach(() => {
  vi.useRealTimers();
});

const file = (name: string) => new File(["x"], name, { type: "text/plain" });

describe("the page's copy of a share", () => {
  it("asks the worker once for an id and keeps the answer for the page's life", async () => {
    held = { s1: [file("a.txt")] };
    expect((await takeShare("s1")).map((f) => f.name)).toEqual(["a.txt"]);
    expect((await takeShare("s1")).map((f) => f.name)).toEqual(["a.txt"]);
    expect(asks).toBe(1);
  });

  it("forgetShare drops the page's copy, so the next take asks the worker again", async () => {
    held = { s2: [file("b.txt")] };
    await takeShare("s2");
    forgetShare("s2");
    expect(await takeShare("s2")).toEqual([]);
    expect(asks).toBe(2);
  });

  it("dropShare forgets the page's copy as well as the worker's", async () => {
    held = { s3: [file("c.txt")] };
    await takeShare("s3");
    dropShare("s3");
    expect(await takeShare("s3")).toEqual([]);
    expect(asks).toBe(2);
  });

  it("clearShares forgets every id the page holds, and tells the worker to drop every share it holds", async () => {
    held = { s4: [file("d.txt")], s5: [file("e.txt")] };
    await takeShare("s4");
    await takeShare("s5");
    // The worker still holds a share the page has not taken: clearShares must reach it too (decision 82, round 3, L5).
    held = { s6: [file("f.txt")] };
    clearShares();
    expect(held).toEqual({});
    expect(drops).toBe(1);
    expect(await takeShare("s4")).toEqual([]);
    expect(asks).toBe(3);
  });
});

describe("the time-box on the page's copy (decision 82, round 3, M1)", () => {
  // Fake timers stand in for the clock, so each limit is run in a moment, not in a minute or five. takenIDs reads the page's copy
  // without taking it, so reading does not restart a limit.
  beforeEach(() => {
    vi.useFakeTimers();
  });

  const SIXTY = 60 * 1000;
  const FIVE_MINUTES = 5 * 60 * 1000;

  it("forgets a share the page took while signed in after sixty seconds", async () => {
    held = { t1: [file("g.txt")] };
    await takeShare("t1");
    vi.advanceTimersByTime(SIXTY - 1);
    expect(takenIDs()).toContain("t1");
    vi.advanceTimersByTime(1);
    expect(takenIDs()).not.toContain("t1");
  });

  it("keeps a share taken while signed out for five minutes, so a sign-in with an emailed code fits in that", async () => {
    held = { t2: [file("h.txt")] };
    await takeShare("t2", { signedOut: true });
    vi.advanceTimersByTime(FIVE_MINUTES - 1);
    expect(takenIDs()).toContain("t2");
    vi.advanceTimersByTime(1);
    expect(takenIDs()).not.toContain("t2");
  });

  it("gives the sixty-second limit once the page takes a signed-out share after the sign-in", async () => {
    held = { t3: [file("i.txt")] };
    await takeShare("t3", { signedOut: true });
    vi.advanceTimersByTime(SIXTY * 2);
    expect(takenIDs()).toContain("t3");
    // Taken again by the signed-in page: from here it is the page's own share, so it is forgotten a minute later.
    await takeShare("t3");
    vi.advanceTimersByTime(SIXTY - 1);
    expect(takenIDs()).toContain("t3");
    vi.advanceTimersByTime(1);
    expect(takenIDs()).not.toContain("t3");
  });

  it("restarts the sixty seconds from each take of the page", async () => {
    held = { t4: [file("j.txt")] };
    await takeShare("t4");
    vi.advanceTimersByTime(SIXTY - 1000);
    await takeShare("t4");
    vi.advanceTimersByTime(SIXTY - 1000);
    expect(takenIDs()).toContain("t4");
    vi.advanceTimersByTime(1000);
    expect(takenIDs()).not.toContain("t4");
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

// The page's copy of a share's files (decision 82, round 2). The page keeps the files it took from the worker in memory, so a
// revisit of the same address during the same session gets them back only while nothing has forgotten them. These tests drive
// the module against a fake worker that holds shares by id and hands each one over once, as the real worker does.

import { beforeEach, describe, expect, it } from "vitest";

import { clearShares, dropShare, forgetShare, takeShare } from "./share";

let held: Record<string, File[]> = {};
let asks = 0;

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
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      controller: {
        postMessage(msg: { type: string; id: string }, ports: MessagePort[]) {
          if (msg.type === "SHARE_TAKE") {
            asks += 1;
            const files = held[msg.id] ?? [];
            delete held[msg.id];
            ports[0].postMessage({ files });
          }
          if (msg.type === "SHARE_DROP") delete held[msg.id];
        },
      },
    },
  });
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

  it("clearShares forgets every id the page holds", async () => {
    held = { s4: [file("d.txt")], s5: [file("e.txt")] };
    await takeShare("s4");
    await takeShare("s5");
    clearShares();
    // The worker is told nothing here: this stands for a worker that still holds the files.
    held = { s4: [file("d.txt")] };
    expect((await takeShare("s4")).map((f) => f.name)).toEqual(["d.txt"]);
    expect(asks).toBe(3);
  });
});

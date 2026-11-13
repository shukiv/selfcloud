// SPDX-License-Identifier: AGPL-3.0-or-later

// A session the server ended (decision 82): the page's shares go, and the worker is told to drop the shares it still
// holds, so they are not kept until their expiry. The request that found it out answers 401 and the page goes to sign in.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./router", () => ({ go: vi.fn(), signInURL: () => "/login" }));

import { api } from "./api";
import { session } from "./session";
import { takeShare, takenIDs } from "./pwa/share";

let held: Record<string, File[]> = {};
let drops: string[] = [];

class SameProcessChannel {
  port1: { onmessage: ((e: { data: unknown }) => void) | null; close(): void } = { onmessage: null, close() {} };
  port2 = { postMessage: (data: unknown) => this.port1.onmessage?.({ data }) };
}
(globalThis as unknown as { MessageChannel: unknown }).MessageChannel = SameProcessChannel;

beforeEach(() => {
  held = { s9: [new File(["x"], "z.txt", { type: "text/plain" })] };
  drops = [];
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: {
      controller: {
        postMessage(msg: { type: string; id?: string }, ports?: MessagePort[]) {
          if (msg.type === "SHARE_TAKE") {
            const files = held[msg.id as string] ?? [];
            delete held[msg.id as string];
            ports![0].postMessage({ files });
          }
          if (msg.type === "SHARE_DROP_ALL") {
            drops.push(msg.type);
            held = {};
          }
        },
      },
    },
  });
  session.set({ user: { id: "usr_a" } as never, account: null, csrf: "tok", publicLinks: false });
});

afterEach(() => {
  vi.unstubAllGlobals();
  session.set(null);
});

describe("the server ends the session (decision 82)", () => {
  it("drops the page's shares and tells the worker to drop the ones it holds", async () => {
    await takeShare("s9");
    expect(takenIDs()).toContain("s9");
    held = { s9: [new File(["x"], "z.txt", { type: "text/plain" })] };
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ error: "unauthenticated" }), { status: 401, headers: { "content-type": "application/json" } })));
    await expect(api("GET", "/api/v1/me")).rejects.toBeDefined();
    expect(takenIDs()).not.toContain("s9");
    expect(drops).toEqual(["SHARE_DROP_ALL"]);
    expect(held).toEqual({});
  });
});

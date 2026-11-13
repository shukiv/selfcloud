// SPDX-License-Identifier: AGPL-3.0-or-later

// The page's held shares follow the person (decision 82). A different person who signs in over a signed-in one, a
// change of the person viewed as, and the start and end of viewing as are each a point where the page's copy of a share must go.
// A sign-in from the signed-out state keeps it: that is the exception for a share opened while signed out (decision 82).
// Each test uses the real share module against a fake worker, and counts what the page asks and what the worker is told.

import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Me, SignedIn } from "./types";
import { session, setMe, setSignedIn, clearSession } from "./session";
import { takeShare, takenIDs } from "./pwa/share";

let asks = 0;
let held: Record<string, File[]> = {};

class SameProcessChannel {
  port1: { onmessage: ((e: { data: unknown }) => void) | null; close(): void } = { onmessage: null, close() {} };
  port2 = { postMessage: (data: unknown) => this.port1.onmessage?.({ data }) };
}
(globalThis as unknown as { MessageChannel: unknown }).MessageChannel = SameProcessChannel;

beforeEach(() => {
  asks = 0;
  held = { s1: [new File(["x"], "a.txt", { type: "text/plain" })] };
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
          if (msg.type === "SHARE_DROP_ALL") held = {};
        },
      },
    },
  });
  session.set(null);
});

afterEach(() => {
  session.set(null);
});

// signedIn is the sign-in answer for a person.
const signedIn = (id: string): SignedIn => ({ user: { id, role: "member" } as SignedIn["user"], csrf_token: `csrf-${id}` } as SignedIn);

// me is the page's account answer for a person, viewed as another person when impersonation is given.
function me(id: string, impersonation = false): Me {
  return {
    user: { id, role: "member" },
    account: null,
    csrf_token: `csrf-${id}`,
    public_links: false,
    impersonation: impersonation ? { ends_at: "2099-01-01T00:00:00Z" } : null,
  } as unknown as Me;
}

// holdAndTake puts a share into the page's copy, and reports whether the page still holds it at once after.
async function holdAndTake(id: string): Promise<boolean> {
  await takeShare(id);
  return takenIDs().includes(id);
}

describe("a change of person clears the page's shares (decision 82)", () => {
  it("B signs in over A, and A's shares are cleared", async () => {
    setSignedIn(signedIn("usr_a"));
    expect(await holdAndTake("s1")).toBe(true);
    setSignedIn(signedIn("usr_b"));
    expect(takenIDs()).not.toContain("s1");
    expect(held).toEqual({});
  });

  it("signing in as the same person again keeps the shares", async () => {
    setSignedIn(signedIn("usr_a"));
    expect(await holdAndTake("s1")).toBe(true);
    setSignedIn(signedIn("usr_a"));
    expect(takenIDs()).toContain("s1");
  });

  it("a sign-out ends the session, and the shares go with it", async () => {
    setSignedIn(signedIn("usr_a"));
    expect(await holdAndTake("s1")).toBe(true);
    clearSession();
    expect(takenIDs()).not.toContain("s1");
    expect(held).toEqual({});
  });
});

describe("a sign-in from the signed-out state keeps the shares (decision 82)", () => {
  it("a share held while signed out is still there after the sign-in", async () => {
    session.set(null);
    await takeShare("s1", { signedOut: true });
    setSignedIn(signedIn("usr_a"));
    expect(takenIDs()).toContain("s1");
    expect(asks).toBe(1);
    await takeShare("s1");
    expect(asks).toBe(1);
  });
});

describe("a signed-out page's own check keeps the share it was sent (decision 82, e2e step 6)", () => {
  it("a 401 on a page with no session to end (the first check of a signed-out load) drops nothing", async () => {
    // The worker holds the share the post made, and the page has not taken it yet, as on a fresh load of the share address.
    session.set(null);
    clearSession();
    expect(Object.keys(held)).toEqual(["s1"]);
    expect((await takeShare("s1", { signedOut: true })).map((f) => f.name)).toEqual(["a.txt"]);
  });
});

describe("viewing as another person clears the page's shares (decision 82)", () => {
  it("the start of viewing as clears them", async () => {
    setMe(me("usr_admin"));
    expect(await holdAndTake("s1")).toBe(true);
    setMe(me("usr_admin", true));
    expect(takenIDs()).not.toContain("s1");
  });

  it("the end of viewing as clears them", async () => {
    setMe(me("usr_admin", true));
    expect(await holdAndTake("s1")).toBe(true);
    setMe(me("usr_admin"));
    expect(takenIDs()).not.toContain("s1");
  });

  it("switching to view as another person clears them", async () => {
    setMe(me("usr_b", true));
    expect(await holdAndTake("s1")).toBe(true);
    setMe(me("usr_c", true));
    expect(takenIDs()).not.toContain("s1");
  });

  it("a refresh of the same person, not viewing as anyone, keeps them", async () => {
    setMe(me("usr_admin"));
    expect(await holdAndTake("s1")).toBe(true);
    setMe(me("usr_admin"));
    expect(takenIDs()).toContain("s1");
  });
});

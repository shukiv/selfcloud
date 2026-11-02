// SPDX-License-Identifier: AGPL-3.0-or-later

// The sign-out marker and the messages that carry it (decision 65): the marker is written at sign-out, lifted at sign-in, and
// the offline page is asked for again at sign-in only when it was needed.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { markSignedIn, markSignedOut } from "./cacheState";

// fakeCaches is the browser's caches, by name, with the request keys as plain strings.
function fakeCaches(initial: Record<string, string[]>) {
  const store = new Map<string, Map<string, string>>(Object.entries(initial).map(([n, keys]) => [n, new Map(keys.map((k) => [k, k]))]));
  const open = async (name: string) => {
    if (!store.has(name)) store.set(name, new Map());
    const c = store.get(name)!;
    return {
      match: async (key: string) => (c.has(key) ? new Response("x") : undefined),
      put: async (key: string) => void c.set(key, key),
      delete: async (key: string) => c.delete(key),
    };
  };
  (window as unknown as { caches: unknown }).caches = {
    keys: async () => [...store.keys()],
    delete: async (name: string) => store.delete(name),
    open,
  };
  return store;
}

let posted: Array<{ type: string; seq: number }>;

beforeEach(() => {
  posted = [];
  Object.defineProperty(navigator, "serviceWorker", {
    configurable: true,
    value: { controller: { postMessage: (m: { type: string; seq: number }) => posted.push(m) } },
  });
});

afterEach(() => {
  delete (window as unknown as { caches?: unknown }).caches;
  delete (navigator as unknown as { serviceWorker?: unknown }).serviceWorker;
  vi.restoreAllMocks();
});

describe("markSignedOut", () => {
  it("removes the app's caches, keeps the state, writes the marker, and sends CLEAR_CACHES with a sequence number", async () => {
    const store = fakeCaches({ "selfcloud-build1": ["/assets/a.js"], "selfcloud-state": ["/build"], "other-app": ["/x"] });
    await markSignedOut();
    expect([...store.keys()].sort()).toEqual(["other-app", "selfcloud-state"]);
    expect(store.get("selfcloud-state")?.has("/signed-out")).toBe(true);
    expect(posted).toHaveLength(1);
    expect(posted[0].type).toBe("CLEAR_CACHES");
    expect(typeof posted[0].seq).toBe("number");
  });
});

describe("markSignedIn", () => {
  it("lifts the marker and asks the worker for the offline page again, because the marker was there", async () => {
    const store = fakeCaches({ "selfcloud-state": ["/signed-out"], "selfcloud-build1": ["/offline.html"] });
    await markSignedIn();
    expect(store.get("selfcloud-state")?.has("/signed-out")).toBe(false);
    expect(posted.map((m) => m.type)).toEqual(["PRECACHE"]);
  });

  it("asks for the offline page again when it is not in the cache", async () => {
    fakeCaches({ "selfcloud-state": [], "selfcloud-build1": ["/assets/a.js"] });
    await markSignedIn();
    expect(posted.map((m) => m.type)).toEqual(["PRECACHE"]);
  });

  it("sends nothing on a visit that finds the page cached and no marker", async () => {
    fakeCaches({ "selfcloud-state": [], "selfcloud-build1": ["/offline.html"] });
    await markSignedIn();
    expect(posted).toEqual([]);
  });

  it("numbers each message later than the one before, so the worker can order them", async () => {
    fakeCaches({ "selfcloud-state": ["/signed-out"], "selfcloud-build1": [] });
    await markSignedOut();
    await markSignedIn();
    expect(posted[1].seq).toBeGreaterThan(posted[0].seq);
  });
});

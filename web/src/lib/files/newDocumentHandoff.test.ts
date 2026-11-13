// SPDX-License-Identifier: AGPL-3.0-or-later

import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./api", () => ({ createDocument: vi.fn() }));

import { createDocument } from "./api";
import { openNewDocumentTab } from "./documents";
import {
  finishHandoff,
  KEY_PATTERN,
  listenHandoff,
  newKey,
  startHandoff,
  takeHandoff,
  waitPath,
} from "./newDocumentHandoff";

// FakeChannel records what the opener posts on the channel, and delivers it to every other channel of the same name.
const sent: unknown[] = [];
const listeners = new Set<(e: { data: unknown }) => void>();
class FakeChannel {
  onmessage: ((e: { data: unknown }) => void) | null = null;
  constructor(public name: string) {
    listeners.add((e) => this.onmessage?.(e));
  }
  postMessage(data: unknown) {
    sent.push(data);
    for (const l of listeners) l({ data });
  }
  close() {}
}

const K = "AAAAAAAAAAAAAAAAAAAAAA";

beforeEach(() => {
  localStorage.clear();
  sent.length = 0;
  listeners.clear();
  vi.stubGlobal("BroadcastChannel", FakeChannel);
  vi.mocked(createDocument).mockReset();
});

describe("the new-document key (decision 102)", () => {
  it("is 128 random bits, base64url, and each is new", () => {
    const spy = vi.spyOn(crypto, "getRandomValues");
    const a = newKey();
    const b = newKey();
    expect(a).toMatch(KEY_PATTERN);
    expect(a.length).toBe(22);
    expect(a).not.toBe(b);
    expect(spy.mock.calls[0][0]).toBeInstanceOf(Uint8Array);
    expect((spy.mock.calls[0][0] as Uint8Array).length).toBe(16);
    spy.mockRestore();
  });

  it("the address of the wait page carries the key and nothing else: no kind, parent or name", () => {
    const path = waitPath(K);
    expect(path).toBe(`/edit/wait?k=${K}`);
    for (const word of ["kind", "parent", "name", "docx", "nod_"]) expect(path).not.toContain(word);
  });
});

describe("the opener: the click makes the key, the new tab waits, the server call makes the file (decision 102)", () => {
  // popup is the window a script opened: the opener clears its opener link before anything else happens in it.
  const popup = { opener: {} as unknown };

  it("the new tab opens only the wait page, makes the file once, and hands the result over on the channel; the opener link is cleared", async () => {
    vi.mocked(createDocument).mockResolvedValue({ id: "nod_a1", folder_id: "fld_b", name: "Untitled document.docx" } as never);
    const open = vi.fn(() => popup);
    openNewDocumentTab({ parent: "nod_root", kind: "docx", from: "/files/fld_b", coarse: false, goTo: vi.fn(), fail: () => "x" }, open);
    expect(open).toHaveBeenCalledTimes(1);
    const [url, target, features] = open.mock.calls[0] as unknown as [string, string, string | undefined];
    expect(url).toMatch(/^\/edit\/wait\?k=[A-Za-z0-9_-]{22}$/);
    expect(target).toBe("_blank");
    // No noopener feature: a blocked pop-up is seen as null, and the opener link is cleared here instead.
    expect(features).toBeUndefined();
    expect(popup.opener).toBeNull();
    await vi.waitFor(() => expect(sent.length).toBe(1));
    expect(createDocument).toHaveBeenCalledTimes(1);
    expect(createDocument).toHaveBeenCalledWith("nod_root", "docx");
    const k = new URL(url, "https://x.test").searchParams.get("k");
    expect(sent[0]).toEqual({ k, handoff: { node: "nod_a1", from: "/files/fld_b", tab: true } });
  });

  it("a blocked new tab (the browser returned no window) creates nothing, and tells the person", async () => {
    vi.mocked(createDocument).mockResolvedValue({ id: "nod_a1" } as never);
    const open = vi.fn(() => null);
    const blocked = vi.fn();
    openNewDocumentTab({ parent: "nod_root", kind: "docx", from: "/files/fld_b", coarse: false, goTo: vi.fn(), fail: () => "x", blocked }, open);
    expect(open).toHaveBeenCalledTimes(1);
    expect(blocked).toHaveBeenCalledTimes(1);
    await new Promise((r) => setTimeout(r, 20));
    expect(createDocument).not.toHaveBeenCalled();
    expect(sent.length).toBe(0);
  });

  it("a refused creation is handed over as its error text", async () => {
    vi.mocked(createDocument).mockRejectedValue(new Error("over quota"));
    const open = vi.fn(() => popup);
    openNewDocumentTab({ parent: "nod_root", kind: "xlsx", from: "/files/fld_b", coarse: false, goTo: vi.fn(), fail: (e) => `failed: ${(e as Error).message}` }, open);
    await vi.waitFor(() => expect(sent.length).toBe(1));
    const k = new URL((open.mock.calls[0] as unknown as [string])[0], "https://x.test").searchParams.get("k");
    expect(sent[0]).toEqual({ k, handoff: { error: "failed: over quota" } });
  });

  it("on a coarse pointer the wait page opens in this tab, and the file is made once, with the same hand-over", async () => {
    vi.mocked(createDocument).mockResolvedValue({ id: "nod_a1" } as never);
    const open = vi.fn(() => null);
    const goTo = vi.fn();
    openNewDocumentTab({ parent: "nod_root", kind: "pptx", from: "/files/fld_b", coarse: true, goTo, fail: () => "x" }, open);
    expect(open).not.toHaveBeenCalled();
    expect(goTo).toHaveBeenCalledTimes(1);
    expect(goTo.mock.calls[0][0]).toMatch(/^\/edit\/wait\?k=[A-Za-z0-9_-]{22}$/);
    await vi.waitFor(() => expect(sent.length).toBe(1));
    expect(createDocument).toHaveBeenCalledTimes(1);
    expect((sent[0] as { handoff: unknown }).handoff).toEqual({ node: "nod_a1", from: "/files/fld_b", tab: false });
  });
});

describe("the hand-over: a tab that loads late finds the result, and only for its own key (decision 102)", () => {
  it("a result stored before the tab listens is found by takeHandoff, once", () => {
    startHandoff(K, 0);
    finishHandoff(K, { node: "nod_a1", from: null, tab: true }, 1000);
    expect(takeHandoff(K, 2000)).toEqual({ state: "done", handoff: { node: "nod_a1", from: null, tab: true } });
    expect(takeHandoff(K, 2000)).toEqual({ state: "unknown" });
  });

  it("a key nobody started is unknown, so the wait page creates nothing", () => {
    expect(takeHandoff("BBBBBBBBBBBBBBBBBBBBBB", 0)).toEqual({ state: "unknown" });
    expect(createDocument).not.toHaveBeenCalled();
  });

  it("a started key with no result yet is pending, with the time it stops waiting", () => {
    startHandoff(K, 0);
    expect(takeHandoff(K, 1000)).toEqual({ state: "pending", expires: 120000 });
  });

  it("a result that is older than its 60 seconds is refused", () => {
    startHandoff(K, 0);
    finishHandoff(K, { node: "nod_a1", from: null, tab: true }, 1000);
    expect(takeHandoff(K, 61001)).toEqual({ state: "expired" });
  });

  it("a pending key that was never finished expires", () => {
    startHandoff(K, 0);
    expect(takeHandoff(K, 120001)).toEqual({ state: "expired" });
  });

  it("an error result is found as an error", () => {
    startHandoff(K, 0);
    finishHandoff(K, { error: "over quota" }, 1000);
    expect(takeHandoff(K, 1000)).toEqual({ state: "done", handoff: { error: "over quota" } });
  });

  it("a listening tab gets the result on the channel", () => {
    startHandoff(K, 0);
    const got: unknown[] = [];
    const stop = listenHandoff(K, (h) => got.push(h));
    finishHandoff(K, { node: "nod_a1", from: null, tab: true }, 1000);
    expect(got).toEqual([{ k: K, handoff: { node: "nod_a1", from: null, tab: true } }]);
    stop();
  });

  it("a result for another key is not taken", () => {
    startHandoff(K, 0);
    finishHandoff(K, { node: "nod_a1", from: null, tab: true }, 1000);
    expect(takeHandoff("CCCCCCCCCCCCCCCCCCCCCC", 1000)).toEqual({ state: "unknown" });
  });

  it("an expired entry of another start is removed when the next creation starts", () => {
    localStorage.setItem("selfcloud.newdoc.OLD", JSON.stringify({ state: "done", expires: 5, handoff: { error: "x" } }));
    startHandoff(K, 100);
    expect(localStorage.getItem("selfcloud.newdoc.OLD")).toBeNull();
  });
});

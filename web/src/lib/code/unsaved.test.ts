// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  announceSignOut,
  dropUnsaved,
  keepUnsaved,
  noteSignedIn,
  purgeAllUnsaved,
  purgeOtherUnsaved,
  readUnsaved,
  SIGNOUT_CHANNEL,
  watchSignOuts,
} from "./unsaved";

const NOD = "nod_a1";
const OTHER_NOD = "nod_b2";
const rec = { text: "x{}", etag: '"e1"', bom: false, sep: "\n" as const };
// until polls a condition until it holds: a channel message reaches this tab's handler in a later turn, so a message is waited for by its
// effect, never by a fixed pause (a late message would land in the next test).
async function until(cond: () => boolean): Promise<void> {
  for (let i = 0; i < 400 && !cond(); i++) await new Promise((r) => setTimeout(r, 5));
}
// keeps says whether this tab keeps text now, probed with a person no test uses.
const keeps = () => keepUnsaved("probe", OTHER_NOD, rec);

beforeEach(() => {
  sessionStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
  sessionStorage.clear();
  noteSignedIn();
});

describe("another tab's explicit sign-out (decision 133, review round 1 MED-4)", () => {
  it("clears every entry in this tab when another tab of the browser announces an explicit sign-out", async () => {
    const stop = watchSignOuts();
    const other = new BroadcastChannel(SIGNOUT_CHANNEL);
    try {
      keepUnsaved("usr_1", NOD, rec);
      keepUnsaved("usr_2", OTHER_NOD, rec);
      other.postMessage({ type: "signed-out" });
      await until(() => readUnsaved("usr_1", NOD) === null);
      expect(readUnsaved("usr_2", OTHER_NOD)).toBeNull();
    } finally {
      stop();
      other.close();
    }
  });

  it("keeps nothing in this tab after another tab's sign-out, until this tab signs in again", async () => {
    const stop = watchSignOuts();
    const other = new BroadcastChannel(SIGNOUT_CHANNEL);
    try {
      other.postMessage({ type: "signed-out" });
      await until(() => !keeps());
      expect(keepUnsaved("usr_1", NOD, rec)).toBe(false);
      expect(readUnsaved("usr_1", NOD)).toBeNull();
      noteSignedIn();
      expect(keepUnsaved("usr_1", NOD, rec)).toBe(true);
    } finally {
      stop();
      other.close();
    }
  });

  it("announceSignOut sends a message that carries no text, to the other tabs", async () => {
    const heard: unknown[] = [];
    const listener = new BroadcastChannel(SIGNOUT_CHANNEL);
    listener.onmessage = (e: MessageEvent) => heard.push(e.data);
    try {
      keepUnsaved("usr_1", NOD, rec);
      announceSignOut();
      await until(() => heard.length > 0);
      expect(heard).toHaveLength(1);
      expect(JSON.stringify(heard)).not.toContain("x{}");
    } finally {
      listener.close();
    }
  });

  it("a sign-in in another tab clears the flag here, so this tab keeps text again (N1)", async () => {
    const stop = watchSignOuts();
    const other = new BroadcastChannel(SIGNOUT_CHANNEL);
    try {
      other.postMessage({ type: "signed-out" });
      await until(() => !keeps());
      expect(keepUnsaved("usr_1", NOD, rec)).toBe(false);
      other.postMessage({ type: "signed-in" });
      await until(keeps);
      expect(keepUnsaved("usr_1", NOD, rec)).toBe(true);
    } finally {
      stop();
      other.close();
    }
  });

  it("a sign-in in this tab posts a sign-in message that carries no text (N1)", async () => {
    const heard: unknown[] = [];
    const listener = new BroadcastChannel(SIGNOUT_CHANNEL);
    listener.onmessage = (e: MessageEvent) => heard.push(e.data);
    try {
      keepUnsaved("usr_1", NOD, rec);
      noteSignedIn();
      await until(() => heard.length > 0);
      expect(heard).toEqual([{ type: "signed-in" }]);
    } finally {
      listener.close();
    }
  });

  it("stopping the watch closes this tab's channel (N2)", () => {
    const close = vi.spyOn(BroadcastChannel.prototype, "close");
    const stop = watchSignOuts();
    stop();
    expect(close).toHaveBeenCalled();
  });

  it("a browser without channels announces and watches nothing, and throws nothing", async () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    try {
      vi.resetModules();
      const m = await import("./unsaved");
      expect(() => m.announceSignOut()).not.toThrow();
      const stop = m.watchSignOuts();
      expect(m.keepUnsaved("usr_1", NOD, rec)).toBe(true);
      expect(() => stop()).not.toThrow();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});

describe("the unsaved text kept across a sign-in (decision 133)", () => {
  it("keeps the text, its ETag and its encoding facts for one person and file, and reads them back", () => {
    expect(keepUnsaved("usr_1", NOD, rec)).toBe(true);
    const got = readUnsaved("usr_1", NOD);
    expect(got).toMatchObject({ text: "x{}", etag: '"e1"', bom: false, sep: "\n", enc: "utf-8" });
    expect(typeof got?.at).toBe("number");
  });

  it("keeps the byte order mark and the line break a file had", () => {
    keepUnsaved("usr_1", NOD, { text: "a\r\nb", etag: '"e2"', bom: true, sep: "\r\n" });
    expect(readUnsaved("usr_1", NOD)).toMatchObject({ bom: true, sep: "\r\n" });
  });

  it("gives nothing for another person or another file", () => {
    keepUnsaved("usr_1", NOD, rec);
    expect(readUnsaved("usr_2", NOD)).toBeNull();
    expect(readUnsaved("usr_1", OTHER_NOD)).toBeNull();
  });

  it("tells two people whose ids share a start apart", () => {
    keepUnsaved("usr_1", NOD, rec);
    expect(readUnsaved("usr_10", NOD)).toBeNull();
  });

  it("drops one entry, and leaves the others", () => {
    keepUnsaved("usr_1", NOD, rec);
    keepUnsaved("usr_1", OTHER_NOD, rec);
    dropUnsaved("usr_1", NOD);
    expect(readUnsaved("usr_1", NOD)).toBeNull();
    expect(readUnsaved("usr_1", OTHER_NOD)).not.toBeNull();
  });

  it("purges every other person's entries, and keeps the person who signed in", () => {
    keepUnsaved("usr_1", NOD, rec);
    keepUnsaved("usr_10", NOD, rec);
    keepUnsaved("usr_2", NOD, rec);
    sessionStorage.setItem("unrelated", "keep");
    purgeOtherUnsaved("usr_1");
    expect(readUnsaved("usr_1", NOD)).not.toBeNull();
    expect(readUnsaved("usr_10", NOD)).toBeNull();
    expect(readUnsaved("usr_2", NOD)).toBeNull();
    expect(sessionStorage.getItem("unrelated")).toBe("keep");
  });

  it("clears every entry at a sign-out, and nothing else", () => {
    keepUnsaved("usr_1", NOD, rec);
    keepUnsaved("usr_2", OTHER_NOD, rec);
    sessionStorage.setItem("unrelated", "keep");
    purgeAllUnsaved();
    expect(readUnsaved("usr_1", NOD)).toBeNull();
    expect(readUnsaved("usr_2", OTHER_NOD)).toBeNull();
    expect(sessionStorage.getItem("unrelated")).toBe("keep");
  });

  it("reads nothing from an entry that is not a kept text", () => {
    keepUnsaved("usr_1", NOD, rec);
    const key = Object.keys(sessionStorage).find((k) => k.includes(NOD))!;
    sessionStorage.setItem(key, "{not json");
    expect(readUnsaved("usr_1", NOD)).toBeNull();
    sessionStorage.setItem(key, JSON.stringify({ text: 3, etag: '"e"', bom: false, sep: "\n", enc: "utf-8" }));
    expect(readUnsaved("usr_1", NOD)).toBeNull();
    sessionStorage.setItem(key, JSON.stringify({ text: "x", etag: '"e"', bom: false, sep: "\n", enc: "latin-1" }));
    expect(readUnsaved("usr_1", NOD)).toBeNull();
  });

  it("says a write that the store refuses was not kept, and does not throw", () => {
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new DOMException("full", "QuotaExceededError");
    });
    expect(() => keepUnsaved("usr_1", NOD, rec)).not.toThrow();
    expect(keepUnsaved("usr_1", NOD, rec)).toBe(false);
  });

  it("reads, drops and purges without throwing when the store refuses", () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(Storage.prototype, "removeItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(Storage.prototype, "key").mockImplementation(() => {
      throw new Error("denied");
    });
    expect(readUnsaved("usr_1", NOD)).toBeNull();
    expect(() => dropUnsaved("usr_1", NOD)).not.toThrow();
    expect(() => purgeOtherUnsaved("usr_1")).not.toThrow();
    expect(() => purgeAllUnsaved()).not.toThrow();
  });
});

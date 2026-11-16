// SPDX-License-Identifier: AGPL-3.0-or-later

// The frame session's state (decision 214): whether the page is in a frame, the remembered list of host
// origins, the one message per origin to the parent, and the host's name when it can be told. Each case
// takes a fresh module, so the remembered list of one case never reaches the next.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const SELF = Object.getOwnPropertyDescriptor(window, "self")!;
const ORIGINS = ["https://host.example", "https://other.example"];

function enterFrame(): void {
  Object.defineProperty(window, "self", { value: {} as Window, configurable: true });
}

function leaveFrame(): void {
  Object.defineProperty(window, "self", SELF);
}

async function freshFrame() {
  vi.resetModules();
  return await import("./frame");
}

beforeEach(() => {
  window.sessionStorage.clear();
  leaveFrame();
});

afterEach(() => {
  leaveFrame();
  delete (window.location as unknown as { ancestorOrigins?: unknown }).ancestorOrigins;
  delete (document as unknown as { referrer?: string }).referrer;
  vi.restoreAllMocks();
  window.sessionStorage.clear();
});

describe("inFrame", () => {
  it("is false at the top level", async () => {
    const f = await freshFrame();
    expect(f.inFrame()).toBe(false);
  });

  it("is true inside a frame", async () => {
    enterFrame();
    const f = await freshFrame();
    expect(f.inFrame()).toBe(true);
  });

  it("is true when reading the top throws", async () => {
    Object.defineProperty(window, "self", {
      get: () => {
        throw new Error("cross-origin");
      },
      configurable: true,
    });
    const f = await freshFrame();
    expect(f.inFrame()).toBe(true);
  });
});

describe("frameSession", () => {
  it("is false in a frame with no remembered list", async () => {
    enterFrame();
    const f = await freshFrame();
    expect(f.frameSession()).toBe(false);
  });

  it("is false outside a frame although a list is remembered", async () => {
    const f = await freshFrame();
    f.rememberFrame(ORIGINS);
    expect(f.frameSession()).toBe(false);
  });

  it("is true in a frame with a remembered list", async () => {
    enterFrame();
    const f = await freshFrame();
    f.rememberFrame(ORIGINS);
    expect(f.frameSession()).toBe(true);
  });

  it("keeps the list in sessionStorage under selfcloud.frame, and a later page load reads it back", async () => {
    enterFrame();
    const f = await freshFrame();
    f.rememberFrame(ORIGINS);
    expect(JSON.parse(window.sessionStorage.getItem("selfcloud.frame") ?? "null")).toEqual(ORIGINS);
    const later = await freshFrame();
    expect(later.frameSession()).toBe(true);
  });

  it("a storage that throws on write keeps the module state", async () => {
    enterFrame();
    const f = await freshFrame();
    vi.spyOn(window.sessionStorage, "setItem").mockImplementation(() => {
      throw new Error("refused");
    });
    f.rememberFrame(ORIGINS);
    expect(f.frameSession()).toBe(true);
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => {});
    f.postToHost("selfcloud.session-ended");
    expect(post).toHaveBeenCalledTimes(2);
  });

  it("a storage that throws on read keeps the module state, and an unreadable store is no list", async () => {
    enterFrame();
    const f = await freshFrame();
    vi.spyOn(window.sessionStorage, "getItem").mockImplementation(() => {
      throw new Error("refused");
    });
    vi.spyOn(window.sessionStorage, "setItem").mockImplementation(() => {
      throw new Error("refused");
    });
    expect(f.frameSession()).toBe(false);
    f.rememberFrame(ORIGINS);
    expect(f.frameSession()).toBe(true);
  });

  it("a stored value that is not a list of strings is no list", async () => {
    enterFrame();
    window.sessionStorage.setItem("selfcloud.frame", JSON.stringify({ origins: ORIGINS }));
    const f = await freshFrame();
    expect(f.frameSession()).toBe(false);
  });
});

describe("postToHost", () => {
  it("posts once per remembered origin, with that origin and only the type", async () => {
    enterFrame();
    const f = await freshFrame();
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => {});
    f.rememberFrame(ORIGINS);
    f.postToHost("selfcloud.session-ended");
    expect(post).toHaveBeenCalledTimes(2);
    expect(post).toHaveBeenNthCalledWith(1, { type: "selfcloud.session-ended" }, ORIGINS[0]);
    expect(post).toHaveBeenNthCalledWith(2, { type: "selfcloud.session-ended" }, ORIGINS[1]);
    for (const call of post.mock.calls) {
      expect(call[1]).not.toBe("*");
      expect(call[0]).toEqual({ type: "selfcloud.session-ended" });
    }
  });

  it("posts the frame-blocked message the same way", async () => {
    enterFrame();
    const f = await freshFrame();
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => {});
    f.rememberFrame(ORIGINS);
    f.postToHost("selfcloud.frame-blocked");
    expect(post).toHaveBeenCalledTimes(2);
    expect(post).toHaveBeenNthCalledWith(1, { type: "selfcloud.frame-blocked" }, ORIGINS[0]);
    expect(post).toHaveBeenNthCalledWith(2, { type: "selfcloud.frame-blocked" }, ORIGINS[1]);
  });

  it("posts nothing outside a frame", async () => {
    const f = await freshFrame();
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => {});
    f.rememberFrame(ORIGINS);
    f.postToHost("selfcloud.session-ended");
    expect(post).not.toHaveBeenCalled();
  });

  it("posts nothing with no remembered list", async () => {
    enterFrame();
    const f = await freshFrame();
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => {});
    f.postToHost("selfcloud.session-ended");
    expect(post).not.toHaveBeenCalled();
  });
});

describe("hostLabel", () => {
  it("reads the parent's host from ancestorOrigins when it is in the list", async () => {
    enterFrame();
    const f = await freshFrame();
    f.rememberFrame(ORIGINS);
    Object.defineProperty(window.location, "ancestorOrigins", {
      value: [ORIGINS[0], "https://second.example"],
      configurable: true,
    });
    expect(f.hostLabel()).toBe("host.example");
  });

  it("falls back to the referrer's origin when there are no ancestorOrigins", async () => {
    enterFrame();
    const f = await freshFrame();
    f.rememberFrame(ORIGINS);
    Object.defineProperty(document, "referrer", { value: "https://other.example/page", configurable: true });
    expect(f.hostLabel()).toBe("other.example");
  });

  it("is null when the parent's origin is not in the list", async () => {
    enterFrame();
    const f = await freshFrame();
    f.rememberFrame(ORIGINS);
    Object.defineProperty(window.location, "ancestorOrigins", {
      value: ["https://stranger.example"],
      configurable: true,
    });
    expect(f.hostLabel()).toBeNull();
  });

  it("is null when nothing tells the parent", async () => {
    enterFrame();
    const f = await freshFrame();
    f.rememberFrame(ORIGINS);
    expect(f.hostLabel()).toBeNull();
  });

  it("is null with no remembered list", async () => {
    enterFrame();
    const f = await freshFrame();
    Object.defineProperty(window.location, "ancestorOrigins", { value: [ORIGINS[0]], configurable: true });
    expect(f.hostLabel()).toBeNull();
  });
});

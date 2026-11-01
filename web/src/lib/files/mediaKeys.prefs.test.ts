// SPDX-License-Identifier: AGPL-3.0-or-later

// The volume and the mute are kept in the browser, and a reload keeps them. Storage that is refused or unreadable leaves the
// defaults in place and throws nothing. The speed is not kept.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const KEY = "selfcloud.player";

beforeEach(() => {
  vi.resetModules();
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("the volume and mute kept in the browser", () => {
  it("starts with the volume and mute saved before the reload, and the speed at 1", async () => {
    window.localStorage.setItem(KEY, JSON.stringify({ volume: 0.4, muted: true }));
    const m = await import("./mediaKeys");
    expect(m.playerPrefs.volume).toBe(0.4);
    expect(m.playerPrefs.muted).toBe(true);
    expect(m.playerPrefs.rate).toBe(1);
  });

  it("uses the defaults when what is saved is not usable", async () => {
    window.localStorage.setItem(KEY, "not json");
    const m = await import("./mediaKeys");
    expect(m.playerPrefs.volume).toBe(1);
    expect(m.playerPrefs.muted).toBe(false);
  });

  it("uses the defaults, and throws nothing, when storage refuses to read", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    const m = await import("./mediaKeys");
    expect(m.playerPrefs.volume).toBe(1);
  });

  it("saves the volume and mute, and throws nothing when storage refuses to write", async () => {
    const m = await import("./mediaKeys");
    m.playerPrefs.volume = 0.2;
    m.playerPrefs.muted = true;
    m.saveVolumePrefs();
    expect(JSON.parse(window.localStorage.getItem(KEY) ?? "null")).toEqual({ volume: 0.2, muted: true });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("quota");
    });
    expect(() => m.saveVolumePrefs()).not.toThrow();
  });
});

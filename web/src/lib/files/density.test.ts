// SPDX-License-Identifier: AGPL-3.0-or-later

// The list's row height choice (decision 185): kept per signed-in person in this browser, under a key that carries the
// person's ID, so two people on one browser keep their own choice. Garbage or blocked storage leaves the default; no person
// stores nothing. A write tells every mounted list.

import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Session } from "../session";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: `Name ${id}`, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: "t1",
    publicLinks: false,
  };
}

beforeEach(() => {
  vi.resetModules();
  window.localStorage.clear();
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("the row height choice kept per person in this browser", () => {
  it("defaults to comfortable", async () => {
    const d = await import("./density");
    expect(d.readDensity("a")).toBe("comfortable");
  });

  it("writes and reads user A's choice under A's own key", async () => {
    const d = await import("./density");
    d.writeDensity("a", "compact");
    expect(d.readDensity("a")).toBe("compact");
    expect(window.localStorage.getItem(d.DENSITY_KEY_PREFIX + "a")).toBe("compact");
    d.writeDensity("a", "comfortable");
    expect(d.readDensity("a")).toBe("comfortable");
  });

  it("leaves user B unaffected by user A's choice", async () => {
    const d = await import("./density");
    d.writeDensity("a", "compact");
    expect(d.readDensity("b")).toBe("comfortable");
    expect(window.localStorage.getItem(d.DENSITY_KEY_PREFIX + "b")).toBeNull();
  });

  it("with no person reads the default and stores nothing", async () => {
    const d = await import("./density");
    expect(d.readDensity(null)).toBe("comfortable");
    d.writeDensity(null, "compact");
    expect(Object.keys(window.localStorage).filter((k) => k.startsWith(d.DENSITY_KEY_PREFIX))).toEqual([]);
  });

  it("reads the default and throws nothing when storage is blocked", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("denied");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("denied");
    });
    const d = await import("./density");
    expect(d.readDensity("a")).toBe("comfortable");
    expect(() => d.writeDensity("a", "compact")).not.toThrow();
  });

  it("reads the default when the stored value is garbage", async () => {
    const d = await import("./density");
    window.localStorage.setItem(d.DENSITY_KEY_PREFIX + "a", "banana");
    expect(d.readDensity("a")).toBe("comfortable");
    window.localStorage.setItem(d.DENSITY_KEY_PREFIX + "a", '{"density":"compact"}');
    expect(d.readDensity("a")).toBe("comfortable");
  });

  it("a write notifies subscribers, and an unsubscribe stops them", async () => {
    const d = await import("./density");
    const fn = vi.fn();
    const stop = d.subscribeDensity(fn);
    d.writeDensity("a", "compact");
    expect(fn).toHaveBeenCalledTimes(1);
    stop();
    d.writeDensity("a", "comfortable");
    expect(fn).toHaveBeenCalledTimes(1);
  });
});

describe("useDensity follows the signed-in person", () => {
  it("reads the person's choice, and a change of person reads the other's, never the last one's", async () => {
    const { session } = await import("../session");
    const d = await import("./density");
    session.set(signedAs("a"));
    const { result } = renderHook(() => d.useDensity());
    expect(result.current[0]).toBe("comfortable");
    act(() => result.current[1]("compact"));
    expect(result.current[0]).toBe("compact");
    expect(window.localStorage.getItem(d.DENSITY_KEY_PREFIX + "a")).toBe("compact");
    // Another person signs in over the first: their own choice, not A's.
    act(() => session.set(signedAs("b")));
    expect(result.current[0]).toBe("comfortable");
    act(() => result.current[1]("compact"));
    act(() => session.set(signedAs("a")));
    expect(result.current[0]).toBe("compact");
    // A sign-out reads the default and a write stores nothing.
    act(() => session.set(null));
    expect(result.current[0]).toBe("comfortable");
    act(() => result.current[1]("compact"));
    expect(window.localStorage.getItem(d.DENSITY_KEY_PREFIX + "b")).toBe("compact");
    expect(Object.keys(window.localStorage).filter((k) => k.startsWith(d.DENSITY_KEY_PREFIX)).sort()).toEqual([
      d.DENSITY_KEY_PREFIX + "a",
      d.DENSITY_KEY_PREFIX + "b",
    ]);
    session.set(null);
  });

  it("every mounted reader re-renders when the choice changes", async () => {
    const { session } = await import("../session");
    const d = await import("./density");
    session.set(signedAs("a"));
    const one = renderHook(() => d.useDensity());
    const two = renderHook(() => d.useDensity());
    act(() => d.writeDensity("a", "compact"));
    expect(one.result.current[0]).toBe("compact");
    expect(two.result.current[0]).toBe("compact");
    session.set(null);
  });
});

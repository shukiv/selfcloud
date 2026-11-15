// SPDX-License-Identifier: AGPL-3.0-or-later

import { afterEach, describe, expect, it, vi } from "vitest";
import { warmImport } from "./warm";

// jsdom has no requestIdleCallback; each test stubs what it needs and the cleanup takes it away.
afterEach(() => {
  vi.unstubAllGlobals();
  vi.useRealTimers();
});

describe("warmImport", () => {
  it("starts the load in an idle callback, not at once", () => {
    const calls: IdleRequestCallback[] = [];
    vi.stubGlobal("requestIdleCallback", (cb: IdleRequestCallback) => {
      calls.push(cb);
      return calls.length;
    });
    const load = vi.fn(() => Promise.resolve());
    warmImport(load);
    expect(load).not.toHaveBeenCalled();
    expect(calls.length).toBe(1);
    calls[0]({ didTimeout: false, timeRemaining: () => 10 });
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("gives the idle callback a deadline, so a busy page still warms", () => {
    let options: IdleRequestOptions | undefined;
    vi.stubGlobal("requestIdleCallback", (_cb: IdleRequestCallback, opts?: IdleRequestOptions) => {
      options = opts;
      return 1;
    });
    warmImport(() => Promise.resolve());
    expect(options?.timeout).toBeGreaterThan(0);
  });

  it("falls back to a short timeout where the browser has no idle callback", () => {
    vi.useFakeTimers();
    vi.stubGlobal("requestIdleCallback", undefined);
    const load = vi.fn(() => Promise.resolve());
    warmImport(load);
    expect(load).not.toHaveBeenCalled();
    vi.advanceTimersByTime(200);
    expect(load).toHaveBeenCalledTimes(1);
  });

  it("swallows a failed load: the lazy component's own load reports it where it draws", async () => {
    vi.stubGlobal("requestIdleCallback", (cb: IdleRequestCallback) => {
      cb({ didTimeout: false, timeRemaining: () => 10 });
      return 1;
    });
    warmImport(() => Promise.reject(new Error("the chunk is gone")));
    // The rejection is handled inside the warm; reaching the next tick with no unhandled rejection is the check.
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
});

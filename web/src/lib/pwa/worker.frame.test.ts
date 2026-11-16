// SPDX-License-Identifier: AGPL-3.0-or-later

// The worker in a frame (decision 214): a frame session registers no service worker, while a top-level page
// registers as today. Each case takes a fresh module, so the one-registration flag of one case never reaches
// the next.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const SELF = Object.getOwnPropertyDescriptor(window, "self")!;

let register: ReturnType<typeof vi.fn>;

beforeEach(() => {
  vi.resetModules();
  window.sessionStorage.clear();
  register = vi.fn(async () => ({ addEventListener: vi.fn(), update: vi.fn(async () => undefined) }));
  Object.defineProperty(navigator, "serviceWorker", {
    value: { register, addEventListener: vi.fn(), controller: null },
    configurable: true,
  });
  Object.defineProperty(window, "isSecureContext", { value: true, configurable: true });
  Object.defineProperty(document, "readyState", { value: "complete", configurable: true });
  Object.defineProperty(window, "self", SELF);
});

afterEach(() => {
  Object.defineProperty(window, "self", SELF);
  delete (navigator as unknown as { serviceWorker?: unknown }).serviceWorker;
  vi.restoreAllMocks();
});

describe("registerWorker in a frame", () => {
  it("registers nothing", async () => {
    Object.defineProperty(window, "self", { value: {} as Window, configurable: true });
    const { registerWorker } = await import("./worker");
    registerWorker();
    expect(register).not.toHaveBeenCalled();
  });

  it("registers as today at the top level", async () => {
    const { registerWorker } = await import("./worker");
    registerWorker();
    expect(register).toHaveBeenCalledTimes(1);
    expect(register).toHaveBeenCalledWith("/sw.js");
  });
});

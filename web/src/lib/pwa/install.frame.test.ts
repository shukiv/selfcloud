// SPDX-License-Identifier: AGPL-3.0-or-later

// The install offer in a frame (decision 214): a frame session keeps no install entry — the browser's offer
// is not taken and the phone's card is not offered — while a top-level page keeps today's offer. Each case
// takes a fresh module, so one case's listeners and offer never reach the next.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { get } from "../store";

const SELF = Object.getOwnPropertyDescriptor(window, "self")!;

function offer(): void {
  const event = new Event("beforeinstallprompt", { cancelable: true }) as Event & { prompt: () => Promise<unknown> };
  event.prompt = vi.fn(async () => ({ outcome: "dismissed" as const }));
  window.dispatchEvent(event);
}

beforeEach(() => {
  vi.resetModules();
  window.localStorage.clear();
  Object.defineProperty(window, "self", SELF);
});

afterEach(() => {
  Object.defineProperty(window, "self", SELF);
  vi.restoreAllMocks();
});

describe("the install offer in a frame", () => {
  it("listenForInstall keeps no offer", async () => {
    Object.defineProperty(window, "self", { value: {} as Window, configurable: true });
    const { canInstall, listenForInstall } = await import("./install");
    listenForInstall();
    offer();
    expect(get(canInstall)).toBe(false);
  });

  it("shouldOfferInstall refuses even on an iPhone with every other condition on", async () => {
    Object.defineProperty(window, "self", { value: {} as Window, configurable: true });
    const { shouldOfferInstall } = await import("./install");
    expect(
      shouldOfferInstall({ signedIn: true, inShell: true, impersonating: false, coarsePointer: true, canInstall: true, ios: true }),
    ).toBe(false);
  });

  it("outside a frame the offer is kept as today", async () => {
    const { canInstall, listenForInstall } = await import("./install");
    listenForInstall();
    offer();
    expect(get(canInstall)).toBe(true);
  });
});

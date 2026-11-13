// SPDX-License-Identifier: AGPL-3.0-or-later

// The install offer (decision 65): the menu entry follows the browser's offer, is used once, and the iPhone check reads the
// user agent. The phone's card (decision 171) is offered by shouldOfferInstall only when every condition holds, and its
// dismissal is kept under one browser key for fourteen days, or for good.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  INSTALL_PROMPT_KEY,
  canInstall,
  dismissInstallPrompt,
  isIOS,
  listenForInstall,
  promptInstall,
  resetInstallOffer,
  shouldOfferInstall,
  takeInstallOffer,
} from "./install";
import { get } from "../store";

// The pointer and display-mode queries the card's gate reads. The setup's matchMedia answers the width queries only.
const setupMatchMedia = window.matchMedia;

beforeEach(() => {
  canInstall.set(false);
  listenForInstall();
});

afterEach(() => {
  vi.restoreAllMocks();
  window.matchMedia = setupMatchMedia;
});

function offer(prompt = vi.fn(async () => ({ outcome: "accepted" as const }))) {
  const event = new Event("beforeinstallprompt", { cancelable: true }) as Event & { prompt: typeof prompt };
  event.prompt = prompt;
  window.dispatchEvent(event);
  return { event, prompt };
}

describe("the install offer", () => {
  it("is not offered until the browser offers it", () => {
    expect(get(canInstall)).toBe(false);
  });

  it("is offered when the browser fires the prompt, and the default is kept back", () => {
    const { event } = offer();
    expect(get(canInstall)).toBe(true);
    expect(event.defaultPrevented).toBe(true);
  });

  it("is used once: the browser's dialog opens, and the entry goes", async () => {
    const { prompt } = offer();
    await promptInstall();
    expect(prompt).toHaveBeenCalledTimes(1);
    expect(get(canInstall)).toBe(false);
    await promptInstall();
    expect(prompt).toHaveBeenCalledTimes(1);
  });
});

describe("iPhone and iPad", () => {
  const agent = (value: string) => vi.spyOn(navigator, "userAgent", "get").mockReturnValue(value);

  it("reads iPhone and iPad from the user agent", () => {
    agent("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X)");
    expect(isIOS()).toBe(true);
    agent("Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X)");
    expect(isIOS()).toBe(true);
  });

  it("is not iOS on a desktop or Android browser", () => {
    agent("Mozilla/5.0 (X11; Linux x86_64) Chrome/120");
    vi.spyOn(navigator, "platform", "get").mockReturnValue("Linux x86_64");
    expect(isIOS()).toBe(false);
    agent("Mozilla/5.0 (Linux; Android 14) Chrome/120 Mobile");
    expect(isIOS()).toBe(false);
  });
});

describe("the iPhone instructions", () => {
  it("are open only for the person who opened them", async () => {
    const { iosDialogOpen } = await import("./install");
    expect(iosDialogOpen("u1", "u1")).toBe(true);
    // A change of person closes them: the opener is no longer the one signed in.
    expect(iosDialogOpen("u1", "u2")).toBe(false);
    expect(iosDialogOpen(null, "u1")).toBe(false);
    expect(iosDialogOpen("u1", null)).toBe(false);
  });
});

describe("shouldOfferInstall, the phone's card (decision 171)", () => {
  const DAY = 24 * 60 * 60 * 1000;
  // Every condition holds: a signed-in person in the shell, on a finger, with a browser offer, not installed, never dismissed.
  const base = { signedIn: true, inShell: true, impersonating: false, coarsePointer: true, canInstall: true, ios: false };
  const storageDesc = Object.getOwnPropertyDescriptor(window, "localStorage");

  // blockStorage makes every read and write of localStorage throw, as a browser that refuses storage does.
  function blockStorage() {
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      get() {
        throw new Error("storage is blocked");
      },
    });
  }

  // standalone answers the installed-app query as an installed app does.
  function standalone(on: boolean) {
    window.matchMedia = (query: string) => ({ ...setupMatchMedia(query), matches: on ? query === "(display-mode: standalone)" : setupMatchMedia(query).matches });
  }

  beforeEach(() => {
    window.localStorage.clear();
    resetInstallOffer();
  });

  afterEach(() => {
    if (storageDesc) Object.defineProperty(window, "localStorage", storageDesc);
    else delete (window as unknown as Record<string, unknown>).localStorage;
  });

  it("offers when every condition holds", () => {
    expect(shouldOfferInstall(base)).toBe(true);
  });

  it("does not offer to a visitor who is not signed in", () => {
    expect(shouldOfferInstall({ ...base, signedIn: false })).toBe(false);
  });

  it("does not offer outside the shell (a share link, the sign-in or the forced-password page)", () => {
    expect(shouldOfferInstall({ ...base, inShell: false })).toBe(false);
  });

  it("does not offer while viewing as another person", () => {
    expect(shouldOfferInstall({ ...base, impersonating: true })).toBe(false);
  });

  it("does not offer with a fine pointer", () => {
    expect(shouldOfferInstall({ ...base, coarsePointer: false })).toBe(false);
  });

  it("does not offer in the installed app", () => {
    standalone(true);
    expect(shouldOfferInstall(base)).toBe(false);
    standalone(false);
    expect(shouldOfferInstall(base)).toBe(true);
  });

  it("does not offer with neither a browser offer nor iPhone/iPad Safari", () => {
    expect(shouldOfferInstall({ ...base, canInstall: false, ios: false })).toBe(false);
  });

  it("offers on iPhone/iPad Safari, which has no browser offer", () => {
    expect(shouldOfferInstall({ ...base, canInstall: false, ios: true })).toBe(true);
  });

  it("does not offer thirteen days after Not now (the fourteen days are not over)", () => {
    window.localStorage.setItem(INSTALL_PROMPT_KEY, JSON.stringify({ at: Date.now() - 13 * DAY, forever: false }));
    expect(shouldOfferInstall(base)).toBe(false);
  });

  it("offers again fifteen days after Not now", () => {
    window.localStorage.setItem(INSTALL_PROMPT_KEY, JSON.stringify({ at: Date.now() - 15 * DAY, forever: false }));
    expect(shouldOfferInstall(base)).toBe(true);
  });

  it("never offers again after Don't remind me again, however long ago", () => {
    window.localStorage.setItem(INSTALL_PROMPT_KEY, JSON.stringify({ at: Date.now() - 300 * DAY, forever: true }));
    expect(shouldOfferInstall(base)).toBe(false);
  });

  it("offers when the storage is empty", () => {
    expect(window.localStorage.getItem(INSTALL_PROMPT_KEY)).toBeNull();
    expect(shouldOfferInstall(base)).toBe(true);
  });

  it("offers when the storage throws: a refused storage is not a dismissal", () => {
    blockStorage();
    expect(shouldOfferInstall(base)).toBe(true);
  });

  it("offers when the stored choice cannot be read", () => {
    window.localStorage.setItem(INSTALL_PROMPT_KEY, "not a choice");
    expect(shouldOfferInstall(base)).toBe(true);
    window.localStorage.setItem(INSTALL_PROMPT_KEY, JSON.stringify({ at: "yesterday" }));
    expect(shouldOfferInstall(base)).toBe(true);
  });

  it("Not now keeps the time under the one browser key, and the offer returns after fourteen days", () => {
    const now = Date.now();
    dismissInstallPrompt(false);
    const stored = JSON.parse(window.localStorage.getItem(INSTALL_PROMPT_KEY) ?? "{}") as { at: number; forever: boolean };
    expect(stored.at).toBeGreaterThanOrEqual(now);
    expect(stored.forever).toBe(false);
    expect(shouldOfferInstall(base)).toBe(false);
    expect(shouldOfferInstall({ ...base, now: now + 13 * DAY })).toBe(false);
    expect(shouldOfferInstall({ ...base, now: now + 15 * DAY })).toBe(true);
  });

  it("Don't remind me again keeps the choice for good", () => {
    dismissInstallPrompt(true);
    const stored = JSON.parse(window.localStorage.getItem(INSTALL_PROMPT_KEY) ?? "{}") as { forever: boolean };
    expect(stored.forever).toBe(true);
    expect(shouldOfferInstall({ ...base, now: Date.now() + 365 * DAY })).toBe(false);
  });

  it("a dismissal write to a throwing storage does not throw", () => {
    blockStorage();
    expect(() => dismissInstallPrompt(false)).not.toThrow();
    expect(() => dismissInstallPrompt(true)).not.toThrow();
  });

  it("the card is offered once per page load", () => {
    expect(takeInstallOffer()).toBe(true);
    expect(takeInstallOffer()).toBe(false);
    resetInstallOffer();
    expect(takeInstallOffer()).toBe(true);
  });
});

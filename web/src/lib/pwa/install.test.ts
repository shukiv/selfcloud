// SPDX-License-Identifier: AGPL-3.0-or-later

// The install offer (decision 65): the menu entry follows the browser's offer, is used once, and the iPhone check reads the
// user agent.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { canInstall, isIOS, listenForInstall, promptInstall } from "./install";
import { get } from "../store";

beforeEach(() => {
  canInstall.set(false);
  listenForInstall();
});

afterEach(() => {
  vi.restoreAllMocks();
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

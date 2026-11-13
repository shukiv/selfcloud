// SPDX-License-Identifier: AGPL-3.0-or-later

// The install card's strings are a catalogue of their own (web/src/i18n/pwa). This file is the one that runs the lazy path:
// it does not preload the catalogue, and it is the first test in its file, so the first offer has nothing loaded. The card
// must not draw before the words arrive, and must show its real strings, never a key, once they do. It stays in its own file
// so that the other tests, which preload the catalogue, do not hide the path.

import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import InstallPrompt from "./InstallPrompt";
import { canInstall, listenForInstall } from "../lib/pwa/install";
import { session, type Session } from "../lib/session";
import { brand } from "../lib/brand";

const setupMatchMedia = window.matchMedia;

beforeEach(() => {
  window.localStorage.clear();
  canInstall.set(false);
  listenForInstall();
  session.set({
    user: { id: "u1", email: "mateo@example.com", display_name: "Mateo", role: "member", locale: "en" } as Session["user"],
    account: null,
    csrf: "csrf",
    publicLinks: false,
  });
  brand.set({ name: "Test Drive", accent: "#053D8E", logo_url: null });
  window.matchMedia = (query: string) => ({
    ...setupMatchMedia(query),
    matches: query === "(pointer: coarse)" ? true : setupMatchMedia(query).matches,
  });
});

afterEach(() => {
  window.matchMedia = setupMatchMedia;
  session.set(null);
  window.localStorage.clear();
});

describe("the install card loads its own strings", () => {
  it("draws nothing before the words arrive, then its real strings and no key", async () => {
    const prompt = async () => ({ outcome: "accepted" as const });
    const event = new Event("beforeinstallprompt", { cancelable: true }) as Event & { prompt: typeof prompt };
    event.prompt = prompt;
    window.dispatchEvent(event);
    render(<InstallPrompt />);
    expect(screen.queryByTestId("install-prompt")).toBeNull();
    expect(document.body.textContent ?? "").not.toMatch(/pwaPrompt\./);
    // The card waits two seconds, then loads its words; the real clock runs both.
    expect(await screen.findByTestId("install-prompt", {}, { timeout: 8000 })).toBeTruthy();
    expect(screen.getByText("Install Test Drive for one-tap access.")).toBeTruthy();
    expect(document.body.textContent ?? "").not.toMatch(/pwaPrompt\./);
  });
});

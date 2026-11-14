// SPDX-License-Identifier: AGPL-3.0-or-later

// The phone's install card (decision 179): it appears two seconds after the shell on a coarse pointer with an offer, keeps
// its words in a lazy catalogue, does not take the focus, and its three dismissals keep the choice under one browser key.
// iPhone/iPad opens the instructions instead of the browser's dialog. The words and the instructions are preloaded here so
// the fake clock reads them at once; the lazy path itself is tested in InstallPrompt.lazy.test.tsx.

import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { Modal } from "antd";
import InstallPrompt from "./InstallPrompt";
import { INSTALL_PROMPT_KEY, canInstall, listenForInstall, resetInstallOffer } from "../lib/pwa/install";
import { loadInstallText } from "../lib/pwa/text";
import { session, type Session } from "../lib/session";
import { brand } from "../lib/brand";
import { setLang } from "../lib/i18n";
import { Direction } from "../App";

// The setup's matchMedia answers the width queries; the card's gate reads (pointer: coarse) and (display-mode: standalone).
const setupMatchMedia = window.matchMedia;

function setCoarse(coarse: boolean) {
  window.matchMedia = (query: string) => ({
    ...setupMatchMedia(query),
    matches: query === "(pointer: coarse)" ? coarse : setupMatchMedia(query).matches,
  });
}

const me: Session = {
  user: { id: "u1", email: "mateo@example.com", display_name: "Mateo", role: "member", locale: "en" } as Session["user"],
  account: null,
  csrf: "csrf",
  publicLinks: false,
};

// offer fires the browser's install event with a prompt that answers the given outcome, as the browser does.
function offer(outcome: "accepted" | "dismissed" = "accepted") {
  const prompt = vi.fn(async () => ({ outcome }));
  const event = new Event("beforeinstallprompt", { cancelable: true }) as Event & { prompt: typeof prompt };
  event.prompt = prompt;
  window.dispatchEvent(event);
  return prompt;
}

// advance runs the fake clock and lets the lazy words and dialog chunks resolve between the ticks.
async function advance(ms: number) {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
  });
}

const card = () => screen.queryByTestId("install-prompt");
// stripped removes the bidi isolates the translator puts around a name in a right-to-left sentence (decision 67).
const stripped = (s: string | null | undefined) => (s ?? "").replace(/[\u2066-\u2069]/g, "");

// The card's words, the Hebrew ones and the instructions dialog are their own lazy chunks. These tests read them at once,
// so they preload them under the real clock, as the approval page's tests do (CliApprovePage.test.tsx).
beforeAll(async () => {
  await loadInstallText("en");
  await loadInstallText("he");
  await setLang("he");
  await setLang("en");
  await import("./pwa/IosInstallDialog");
});

beforeEach(() => {
  vi.useFakeTimers();
  window.localStorage.clear();
  resetInstallOffer();
  canInstall.set(false);
  listenForInstall();
  session.set(me);
  brand.set({ name: "Test Drive", accent: "#053D8E", logo_url: null });
  setCoarse(true);
});

afterEach(async () => {
  vi.useRealTimers();
  vi.restoreAllMocks();
  window.matchMedia = setupMatchMedia;
  session.set(null);
  window.localStorage.clear();
  resetInstallOffer();
  await setLang("en");
});

describe("InstallPrompt", () => {
  it("appears two seconds after the shell on a coarse pointer with a browser offer, naming the installation", async () => {
    offer();
    render(<InstallPrompt />);
    expect(card()).toBeNull();
    await advance(1900);
    expect(card()).toBeNull();
    await advance(1200);
    const region = screen.getByRole("region", { name: "Install Test Drive" });
    expect(region).toBeTruthy();
    expect(region.getAttribute("aria-modal")).toBeNull();
    expect(screen.getByText("Install Test Drive for one-tap access.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Not now" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Install" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Don't remind me again" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Close" })).toBeTruthy();
  });

  it("Install opens the browser's own dialog, and the card goes", async () => {
    const prompt = offer();
    render(<InstallPrompt />);
    await advance(3100);
    fireEvent.click(screen.getByRole("button", { name: "Install" }));
    expect(prompt).toHaveBeenCalledTimes(1);
    expect(card()).toBeNull();
    await advance(3000);
    expect(card()).toBeNull();
  });

  it("Not now hides the card and keeps the time, and a later tick does not bring it back", async () => {
    offer();
    render(<InstallPrompt />);
    await advance(3100);
    const now = Date.now();
    fireEvent.click(screen.getByRole("button", { name: "Not now" }));
    expect(card()).toBeNull();
    const stored = JSON.parse(window.localStorage.getItem(INSTALL_PROMPT_KEY) ?? "{}") as { at: number; forever: boolean };
    expect(stored.at).toBe(now);
    expect(stored.forever).toBe(false);
    await advance(10000);
    expect(card()).toBeNull();
  });

  it("the close icon hides the card and keeps the time as Not now does", async () => {
    offer();
    render(<InstallPrompt />);
    await advance(3100);
    fireEvent.click(screen.getByRole("button", { name: "Close" }));
    expect(card()).toBeNull();
    const stored = JSON.parse(window.localStorage.getItem(INSTALL_PROMPT_KEY) ?? "{}") as { at: number; forever: boolean };
    expect(stored.at).toBe(Date.now());
    expect(stored.forever).toBe(false);
  });

  it("Don't remind me again hides the card for good: a re-render and a fresh mount keep it gone", async () => {
    offer();
    const { rerender, unmount } = render(<InstallPrompt />);
    await advance(3100);
    fireEvent.click(screen.getByRole("button", { name: "Don't remind me again" }));
    expect(card()).toBeNull();
    const stored = JSON.parse(window.localStorage.getItem(INSTALL_PROMPT_KEY) ?? "{}") as { forever: boolean };
    expect(stored.forever).toBe(true);
    rerender(<InstallPrompt />);
    await advance(5000);
    expect(card()).toBeNull();
    // A fresh mount with the offer taken again still reads the dismissal: the choice outlives the page.
    unmount();
    resetInstallOffer();
    offer();
    render(<InstallPrompt />);
    await advance(5000);
    expect(card()).toBeNull();
  });

  it("Escape while the focus is inside the card closes it as Not now", async () => {
    offer();
    render(<InstallPrompt />);
    await advance(3100);
    const notNow = screen.getByRole("button", { name: "Not now" });
    fireEvent.keyDown(notNow, { key: "Escape" });
    expect(card()).toBeNull();
    const stored = JSON.parse(window.localStorage.getItem(INSTALL_PROMPT_KEY) ?? "{}") as { forever: boolean };
    expect(stored.forever).toBe(false);
  });

  it("iPhone opens the instructions the account menu opens, with no browser offer", async () => {
    vi.spyOn(navigator, "userAgent", "get").mockReturnValue("Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15");
    render(<InstallPrompt />);
    await advance(3100);
    expect(card()).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Install" }));
    await advance(200);
    expect(screen.getByText("Add to Home Screen")).toBeTruthy();
    expect(screen.getByText("Tap the browser's Share button.")).toBeTruthy();
  });

  it("shows nothing with a fine pointer, however long the page waits", async () => {
    setCoarse(false);
    offer();
    render(<InstallPrompt />);
    await advance(10000);
    expect(card()).toBeNull();
  });

  it("does not move the focus when it appears", async () => {
    offer();
    render(
      <>
        <button type="button">the page's own button</button>
        <InstallPrompt />
      </>,
    );
    const probe = screen.getByRole("button", { name: "the page's own button" });
    act(() => probe.focus());
    await advance(3100);
    expect(card()).toBeTruthy();
    expect(document.activeElement).toBe(probe);
  });

  it("waits while a dialog is open, and appears once it closes", async () => {
    offer();
    const { rerender } = render(
      <>
        <Modal open title="A form" destroyOnClose>
          <p>busy</p>
        </Modal>
        <InstallPrompt />
      </>,
    );
    await advance(4000);
    expect(card()).toBeNull();
    // The app unmounts a dialog when it closes (the shell's own dialogs are conditional); the null keeps the card in its
    // place, as the shell keeps it mounted while dialogs come and go.
    rerender(
      <>
        {null}
        <InstallPrompt />
      </>,
    );
    await advance(1500);
    expect(card()).toBeTruthy();
  });

  it("appinstalled takes the card away, and it stays gone for the rest of the page load", async () => {
    offer();
    const { unmount } = render(<InstallPrompt />);
    await advance(3100);
    expect(card()).toBeTruthy();
    act(() => {
      window.dispatchEvent(new Event("appinstalled"));
    });
    expect(card()).toBeNull();
    // A new offer on the same page load does not bring it back: the card is shown once per load.
    unmount();
    offer();
    render(<InstallPrompt />);
    await advance(5000);
    expect(card()).toBeNull();
  });

  it("renders right to left in Hebrew, with the Hebrew words and the name in its own order", async () => {
    await setLang("he");
    offer();
    render(
      <Direction>
        <InstallPrompt />
      </Direction>,
    );
    await advance(3100);
    const region = screen.getByTestId("install-prompt");
    expect(document.documentElement.dir).toBe("rtl");
    expect(stripped(region.getAttribute("aria-label"))).toBe("התקנת Test Drive");
    expect(stripped(region.textContent)).toContain("התקינו את Test Drive לגישה בלחיצה אחת.");
    expect(screen.getByRole("button", { name: "לא עכשיו" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "התקנה" })).toBeTruthy();
  });
});
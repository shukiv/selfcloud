// SPDX-License-Identifier: AGPL-3.0-or-later

// A frame sign-in link whose cookie the browser refuses (decision 214): the redeem answers frame true, the
// page remembers the origins and reads the session once; a 401 there draws the refused-cookie view and tells
// the host, and a session that answers moves on as today. A tab redeem never makes the probe.

import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";
import { rememberFrame } from "../lib/frame";
import SignInPage from "./SignInPage";

vi.mock("../lib/auth", () => ({
  signInWithLink: vi.fn(),
  establish: vi.fn(async () => undefined),
  loadMe: vi.fn(),
}));

import { establish, loadMe, signInWithLink } from "../lib/auth";

const SELF = Object.getOwnPropertyDescriptor(window, "self")!;
const TOKEN = "A".repeat(43);
const ORIGINS = ["https://host.example"];
const FRAME_ANSWER = {
  next: "/files",
  user: { id: "usr_m" },
  csrf_token: "csrf",
  frame: true,
  frame_origins: ORIGINS,
};

beforeEach(async () => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  window.history.replaceState(null, "", "/sign-in");
  Object.defineProperty(window, "self", { value: {} as Window, configurable: true });
  rememberFrame([]);
  vi.mocked(signInWithLink).mockReset();
  vi.mocked(establish).mockClear();
  vi.mocked(loadMe).mockReset();
  await setLang("en");
});

afterEach(() => {
  Object.defineProperty(window, "self", SELF);
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
});

function openLink(): void {
  window.history.replaceState(null, "", "/sign-in#token=" + TOKEN);
}

describe("SignInPage with a frame link", () => {
  it("a frame redeem whose session probe is 401 draws the refused-cookie view and tells the host", async () => {
    vi.mocked(signInWithLink).mockResolvedValue(FRAME_ANSWER as never);
    vi.mocked(loadMe).mockResolvedValue(null);
    const post = vi.spyOn(window, "postMessage").mockImplementation(() => {});
    openLink();
    render(
      <ThemeProvider>
        <SignInPage />
      </ThemeProvider>,
    );
    expect(await screen.findByText("This page can't keep you signed in")).toBeTruthy();
    expect(establish).not.toHaveBeenCalled();
    expect(window.location.pathname).toBe("/sign-in");
    expect(post).toHaveBeenCalledWith({ type: "selfcloud.frame-blocked" }, ORIGINS[0]);
    expect(JSON.parse(window.sessionStorage.getItem("selfcloud.frame") ?? "null")).toEqual(ORIGINS);
    expect(document.querySelector('input[type="password"]')).toBeNull();
  });

  it("a frame redeem whose probe answers moves on as today", async () => {
    vi.mocked(signInWithLink).mockResolvedValue(FRAME_ANSWER as never);
    vi.mocked(loadMe).mockResolvedValue({ user: { id: "usr_m" }, frame: true, frame_origins: ORIGINS } as never);
    openLink();
    render(
      <ThemeProvider>
        <SignInPage />
      </ThemeProvider>,
    );
    await waitFor(() => expect(window.location.pathname).toBe("/files"));
    expect(establish).toHaveBeenCalledTimes(1);
    expect(screen.queryByText("This page can't keep you signed in")).toBeNull();
  });

  it("a tab redeem never makes the probe", async () => {
    vi.mocked(signInWithLink).mockResolvedValue({ next: "/files", user: { id: "usr_m" }, csrf_token: "csrf", frame: false } as never);
    openLink();
    render(
      <ThemeProvider>
        <SignInPage />
      </ThemeProvider>,
    );
    await waitFor(() => expect(window.location.pathname).toBe("/files"));
    expect(loadMe).not.toHaveBeenCalled();
    expect(establish).toHaveBeenCalledTimes(1);
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

// A sign-in link that fails to redeem while the installation names an outside sign-in page (decision 204): the failure
// text stays, a Sign in again button goes to the outside page, and the page never redirects by itself. A redeem that
// works keeps going to the link's path.

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";
import { ApiError } from "../lib/apiError";
import { setSignInInfo } from "../lib/outsideSignIn";
import { stubLocationAssign } from "../test/locationStub";
import SignInPage from "./SignInPage";

vi.mock("../lib/auth", () => ({
  signInWithLink: vi.fn(),
  establish: vi.fn(async () => undefined),
  loadMe: vi.fn(),
}));

import { signInWithLink } from "../lib/auth";

const OUTSIDE = "https://id.example.com/sign-in?next={next}";
const TOKEN = "A".repeat(43);

let assign: ReturnType<typeof stubLocationAssign>;

beforeEach(async () => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  setSignInInfo(null);
  window.history.replaceState(null, "", "/sign-in");
  assign = stubLocationAssign();
  vi.mocked(signInWithLink).mockReset();
  await setLang("en");
});

afterEach(() => {
  vi.unstubAllGlobals();
  setSignInInfo(null);
  window.sessionStorage.clear();
});

function setOutside(): void {
  setSignInInfo({ externalUrl: OUTSIDE, signedOutUrl: "", password: false });
}

function openLink(): void {
  window.history.replaceState(null, "", "/sign-in#token=" + TOKEN);
}

describe("SignInPage with an outside sign-in page", () => {
  it("a failed redeem keeps the failure text, offers Sign in again, and redirects by itself never", async () => {
    setOutside();
    vi.mocked(signInWithLink).mockRejectedValue(new ApiError(400, "token_expired", {}, 0));
    openLink();
    render(
      <ThemeProvider>
        <SignInPage />
      </ThemeProvider>,
    );
    expect(await screen.findByText(/This sign-in link has expired or was already used/)).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Sign in again" }));
    expect(assign).toHaveBeenCalledTimes(1);
    expect(assign).toHaveBeenCalledWith("https://id.example.com/sign-in?next=%2F");
  });

  it("a failed redeem offers no Sign in again when no outside page is set", async () => {
    vi.mocked(signInWithLink).mockRejectedValue(new ApiError(400, "token_expired", {}, 0));
    openLink();
    render(
      <ThemeProvider>
        <SignInPage />
      </ThemeProvider>,
    );
    expect(await screen.findByText(/This sign-in link has expired or was already used/)).toBeTruthy();
    expect(screen.queryByRole("button", { name: "Sign in again" })).toBeNull();
    expect(assign).not.toHaveBeenCalled();
  });

  it("a successful redeem goes to the link's path as before", async () => {
    setOutside();
    vi.mocked(signInWithLink).mockResolvedValue({ next: "/files/abc", user: { id: "usr_m" } } as never);
    openLink();
    render(
      <ThemeProvider>
        <SignInPage />
      </ThemeProvider>,
    );
    await waitFor(() => expect(window.location.pathname).toBe("/files/abc"));
    expect(assign).not.toHaveBeenCalled();
  });
});
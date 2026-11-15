// SPDX-License-Identifier: AGPL-3.0-or-later

// The sign-in page while the installation names an outside sign-in page (decision 185): /login goes to it, the query
// password keeps the installation admin's form, and a redirect the loop guard refuses shows the signed-out page.

import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";
import { sendToSignIn, setSignInInfo, SIGNED_OUT_PATH } from "../lib/outsideSignIn";
import { stubLocationAssign } from "../test/locationStub";
import LoginPage from "./LoginPage";

const OUTSIDE = "https://id.example.com/sign-in?next={next}";

let assign: ReturnType<typeof stubLocationAssign>;

beforeEach(async () => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  setSignInInfo(null);
  window.history.replaceState(null, "", "/login");
  assign = stubLocationAssign();
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

describe("LoginPage with an outside sign-in page", () => {
  it("draws the form at /login?password", async () => {
    setOutside();
    window.history.replaceState(null, "", "/login?password");
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeTruthy();
    expect(document.getElementById("login-email")).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
  });

  it("redirects /login to the outside page with the next path", async () => {
    setOutside();
    window.history.replaceState(null, "", "/login?next=%2Ffiles%2Fabc");
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://id.example.com/sign-in?next=%2Ffiles%2Fabc"));
  });

  it("redirects a bare /login with the home path as next", async () => {
    setOutside();
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://id.example.com/sign-in?next=%2F"));
  });

  it("draws the form when no outside page is set", async () => {
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    expect(await screen.findByRole("button", { name: "Sign in" })).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
  });

  it("shows the signed-out page when the loop guard refuses a third redirect", async () => {
    setOutside();
    sendToSignIn("/files/a");
    sendToSignIn("/files/b");
    assign.mockClear();
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    await waitFor(() => expect(window.location.pathname).toBe(SIGNED_OUT_PATH));
    expect(assign).not.toHaveBeenCalled();
  });
});
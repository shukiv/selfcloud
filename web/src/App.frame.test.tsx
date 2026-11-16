// SPDX-License-Identifier: AGPL-3.0-or-later

// The gate in a frame session (decision 214): a signed-out person draws the session-ended view at every
// address but the link page, and never moves to the sign-in page or the installation's outside page. Outside
// a frame session today's moves are kept, although a list is remembered.

import { render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Direction, Gate } from "./App";
import { session } from "./lib/session";
import { setLang } from "./lib/i18n";
import { rememberFrame } from "./lib/frame";
import { setSignInInfo } from "./lib/outsideSignIn";
import { stubLocationAssign } from "./test/locationStub";
import { ThemeProvider } from "./theme/theme";

const SELF = Object.getOwnPropertyDescriptor(window, "self")!;
const ORIGINS = ["https://host.example"];
const OUTSIDE = "https://id.example.com/sign-in?next={next}";

function renderAt(path: string) {
  const router = createMemoryRouter([{ path: "*", element: <Gate /> }], { initialEntries: [path] });
  render(
    <ThemeProvider>
      <Direction>
        <RouterProvider router={router} />
      </Direction>
    </ThemeProvider>,
  );
  return router;
}

beforeEach(async () => {
  window.localStorage.clear();
  window.sessionStorage.clear();
  session.set(null);
  setSignInInfo(null);
  rememberFrame([]);
  Object.defineProperty(window, "self", SELF);
  await setLang("en");
});

afterEach(() => {
  Object.defineProperty(window, "self", SELF);
  setSignInInfo(null);
  vi.unstubAllGlobals();
  window.sessionStorage.clear();
});

describe("the gate in a frame session", () => {
  beforeEach(() => {
    Object.defineProperty(window, "self", { value: {} as Window, configurable: true });
    rememberFrame(ORIGINS);
  });

  it("draws the session-ended view for a signed-out drive page, and never goes to sign-in", async () => {
    const assign = stubLocationAssign();
    const router = renderAt("/trash");
    expect(await screen.findByText("Your session ended")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/trash");
    expect(assign).not.toHaveBeenCalled();
  });

  it("draws the session-ended view at the sign-in address, with no password form", async () => {
    const router = renderAt("/login");
    expect(await screen.findByText("Your session ended")).toBeTruthy();
    expect(document.getElementById("login-email")).toBeNull();
    expect(screen.queryByText("Forgot password")).toBeNull();
    expect(router.state.location.pathname).toBe("/login");
  });

  it("draws the session-ended view for a share opened while signed out", async () => {
    const router = renderAt("/s?id=lnk_1");
    expect(await screen.findByText("Your session ended")).toBeTruthy();
    expect(router.state.location.pathname).toBe("/s");
  });

  it("never leaves for the installation's outside sign-in page", async () => {
    setSignInInfo({ externalUrl: OUTSIDE, signedOutUrl: "", password: false });
    const assign = stubLocationAssign();
    const router = renderAt("/trash");
    expect(await screen.findByText("Your session ended")).toBeTruthy();
    expect(assign).not.toHaveBeenCalled();
    expect(router.state.location.pathname).toBe("/trash");
  });
});

describe("the gate outside a frame session", () => {
  it("keeps today's move to sign-in although a list is remembered", async () => {
    rememberFrame(ORIGINS);
    const router = renderAt("/trash");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toBe("?next=%2Ftrash");
  });

  it("keeps today's move in a frame with no remembered list", async () => {
    Object.defineProperty(window, "self", { value: {} as Window, configurable: true });
    const router = renderAt("/trash");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
  });

  it("keeps today's move to the outside sign-in page", async () => {
    setSignInInfo({ externalUrl: OUTSIDE, signedOutUrl: "", password: false });
    const assign = stubLocationAssign();
    renderAt("/trash");
    await waitFor(() => expect(assign).toHaveBeenCalledWith("https://id.example.com/sign-in?next=%2Ftrash"));
  });
});

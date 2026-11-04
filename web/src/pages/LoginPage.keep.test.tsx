// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";
import LoginPage from "./LoginPage";

// The sign-in call is held open, so the tests read what the page sent and nothing navigates.
const mocks = vi.hoisted(() => ({ signIn: vi.fn() }));
vi.mock("../lib/auth", async (orig) => ({
  ...(await orig<typeof import("../lib/auth")>()),
  signIn: mocks.signIn,
}));

const KEY = "selfcloud.keepSignedIn";

function renderPage() {
  return render(
    <ThemeProvider>
      <LoginPage />
    </ThemeProvider>,
  );
}

function box(): HTMLInputElement {
  return screen.getByRole("checkbox", { name: "Keep me signed in" }) as HTMLInputElement;
}

// signInAs fills the form and submits it, then returns what the page sent to the sign-in call.
async function signInAs(): Promise<unknown[]> {
  fireEvent.change(document.getElementById("login-email") as HTMLInputElement, { target: { value: "ann@example.com" } });
  fireEvent.change(document.getElementById("login-password") as HTMLInputElement, { target: { value: "a passphrase" } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
  await waitFor(() => expect(mocks.signIn).toHaveBeenCalled());
  return mocks.signIn.mock.calls[0];
}

describe("LoginPage: Keep me signed in", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    mocks.signIn.mockReset();
    mocks.signIn.mockReturnValue(new Promise(() => {}));
    await setLang("en");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("is unticked by default", () => {
    renderPage();
    expect(box().checked).toBe(false);
    // The box is a real checkbox whose label is its text, so a screen reader names it.
    expect(box().type).toBe("checkbox");
    expect(document.getElementById("login-keep")).toBe(box());
  });

  it("sends remember false when it is unticked", async () => {
    renderPage();
    const args = await signInAs();
    expect(args[3]).toBe(false);
  });

  it("sends remember true when it is ticked", async () => {
    renderPage();
    fireEvent.click(box());
    expect(box().checked).toBe(true);
    const args = await signInAs();
    expect(args[3]).toBe(true);
  });

  it("remembers the last choice in this browser", () => {
    const first = renderPage();
    fireEvent.click(box());
    expect(window.localStorage.getItem(KEY)).toBe("1");
    first.unmount();

    renderPage();
    expect(box().checked).toBe(true);
    fireEvent.click(box());
    expect(window.localStorage.getItem(KEY)).toBe("0");
  });

  it("falls back to unticked when the browser cannot read its storage, and still sends the choice", async () => {
    vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("full");
    });
    renderPage();
    expect(box().checked).toBe(false);
    fireEvent.click(box());
    expect(box().checked).toBe(true);
    const args = await signInAs();
    expect(args[3]).toBe(true);
  });
});

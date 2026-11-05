// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";
import LoginPage from "./LoginPage";

// sec2, decision 86: while the email's cap is spent from a browser the server does not know, the sign-in answers with the
// confirmation. The page says to check the email, shows no error, and offers the way back to the form.
const mocks = vi.hoisted(() => ({ signIn: vi.fn(), establish: vi.fn() }));
vi.mock("../lib/auth", async (orig) => ({
  ...(await orig<typeof import("../lib/auth")>()),
  signIn: mocks.signIn,
  establish: mocks.establish,
}));

function renderPage() {
  return render(
    <ThemeProvider>
      <LoginPage />
    </ThemeProvider>,
  );
}

function submitSignIn() {
  fireEvent.change(document.getElementById("login-email") as HTMLInputElement, { target: { value: "ann@example.com" } });
  fireEvent.change(document.getElementById("login-password") as HTMLInputElement, { target: { value: "a passphrase" } });
  fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
}

describe("LoginPage: the browser is confirmed by email (decision 86)", () => {
  beforeEach(async () => {
    mocks.signIn.mockReset();
    mocks.establish.mockReset();
    await setLang("en");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("says to check the email, with no error, and goes back to the form", async () => {
    mocks.signIn.mockResolvedValue({ confirm: "email" });
    renderPage();
    submitSignIn();

    await screen.findByRole("heading", { name: "Check your email" });
    expect(screen.getByText(/we sent a link to the address/)).toBeTruthy();
    expect(screen.getByText(/use Forgot password/)).toBeTruthy();
    // No session is set: the answer names none.
    expect(mocks.establish).not.toHaveBeenCalled();
    // No error is shown.
    expect(document.querySelector(".ant-alert-error")).toBeNull();

    fireEvent.click(screen.getByRole("button", { name: "Back to the sign-in form" }));
    await waitFor(() => expect(screen.getByRole("heading", { name: "Sign in" })).toBeTruthy());
    expect(document.getElementById("login-email")).toBeTruthy();
  });
});

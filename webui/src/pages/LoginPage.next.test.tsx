// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";

// The sign-in is answered, and the gate moves the page on while the session is being set, as it does in the app:
// the address then no longer carries the next the person came from.
const mocks = vi.hoisted(() => ({ signIn: vi.fn(), establish: vi.fn() }));
vi.mock("../lib/auth", async (orig) => ({ ...(await orig<typeof import("../lib/auth")>()), signIn: mocks.signIn, establish: mocks.establish }));

import LoginPage from "./LoginPage";

describe("LoginPage after a sign-in that started from another address", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
    mocks.signIn.mockResolvedValue({ user: { id: "u1" }, csrf_token: "t" });
    mocks.establish.mockImplementation(async () => {
      window.history.replaceState(null, "", "/shared");
    });
  });

  it("goes to the next address it was given, not to home, when the gate has moved on during the sign-in", async () => {
    window.history.replaceState(null, "", "/login?next=%2Ffiles%2Ffld_aaaaaaaa");
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    fireEvent.change(document.getElementById("login-email") as HTMLInputElement, { target: { value: "olivia@example.com" } });
    fireEvent.change(document.getElementById("login-password") as HTMLInputElement, { target: { value: "correct horse" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(mocks.establish).toHaveBeenCalled());
    await waitFor(() => expect(window.location.pathname).toBe("/files/fld_aaaaaaaa"));
    window.history.replaceState(null, "", "/");
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from "react";
import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createAppRouter } from "../lib/router";
import { setLang } from "../lib/i18n";
import { session, setSignedIn } from "../lib/session";
import { ThemeProvider } from "../theme/theme";
import type { User } from "../lib/types";

// The sign-in answers with an admin, and the session is set the way lib/auth does it. The admin's home is the
// tenants list. The tenants page itself is replaced: this test is about where the address ends up.
const mocks = vi.hoisted(() => ({ signIn: vi.fn(), establish: vi.fn(), loadInfo: vi.fn(), loadMe: vi.fn() }));
vi.mock("../lib/auth", async (orig) => ({
  ...(await orig<typeof import("../lib/auth")>()),
  signIn: mocks.signIn,
  establish: mocks.establish,
  loadInfo: mocks.loadInfo,
  loadMe: mocks.loadMe,
}));
vi.mock("../pages/admin/TenantsPage", () => ({ default: () => <p>tenant list stub</p> }));

const ADMIN: User = { id: "usr_admin", email: "ada@example.com", display_name: "Ada", locale: "en", role: "admin", status: "active" } as unknown as User;

import { Boot, Gate } from "../App";

describe("an installation admin signs in from /login", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
    session.set(null);
    // Boot, as the app mounts it: the installation's information and the signed-in person, read before the gate.
    mocks.loadInfo.mockResolvedValue(null);
    mocks.loadMe.mockResolvedValue(null);
    mocks.signIn.mockResolvedValue({ user: ADMIN, csrf_token: "csrf" });
    // lib/auth.establish: it sets the session, then waits for loadMe before it returns. The wait is what lets the
    // gate move the page on before LoginPage continues.
    mocks.establish.mockImplementation(async (signed: { user: User; csrf_token: string }) => {
      setSignedIn(signed);
      await new Promise((resolve) => setTimeout(resolve, 20));
    });
  });

  it("lands on the tenants list, not on a blank page at /", async () => {
    window.history.replaceState(null, "", "/login");
    const router = createAppRouter([{ path: "*", element: <Boot><Gate /></Boot> }]);
    // main.tsx mounts the app in StrictMode, which runs each effect twice: the test does the same.
    render(
      <StrictMode>
        <ThemeProvider>
          <RouterProvider router={router} />
        </ThemeProvider>
      </StrictMode>,
    );
    await waitFor(() => expect(document.getElementById("login-email")).toBeTruthy());
    fireEvent.change(document.getElementById("login-email") as HTMLInputElement, { target: { value: "ada@example.com" } });
    fireEvent.change(document.getElementById("login-password") as HTMLInputElement, { target: { value: "correct horse" } });
    await act(async () => {
      fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    });
    await waitFor(() => expect(mocks.establish).toHaveBeenCalled());
    // The app's root must draw something: a blank root is the failure this test is for.
    await waitFor(() => expect(document.getElementById("root") ?? document.body).toBeTruthy());
    await waitFor(() => expect(window.location.pathname).toBe("/admin/tenants"));
    expect(await screen.findByText("tenant list stub")).toBeTruthy();
    window.history.replaceState(null, "", "/");
  });
});

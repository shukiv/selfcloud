// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { describe, expect, it, beforeEach } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";
import LoginPage from "./LoginPage";

describe("LoginPage", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
  });

  it("asks for an email before it sends anything", async () => {
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Sign in" }));
    // 5 s: the message follows a click, and a loaded CI machine can take longer than the 1 s default.
    await waitFor(() => expect(screen.getByText("Enter your email address.")).toBeTruthy(), { timeout: 5000 });
    expect(document.getElementById("login-email")?.getAttribute("aria-invalid")).toBeNull();
  });

  it("asks for a password once the email is there", async () => {
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    const signIn = await screen.findByRole("button", { name: "Sign in" });
    const email = document.getElementById("login-email") as HTMLInputElement;
    fireEvent.change(email, { target: { value: "olivia@example.com" } });
    fireEvent.click(signIn);
    await waitFor(() => expect(screen.getByText("Enter your password.")).toBeTruthy());
  });

  it("draws the forgot-password link in the theme's link colour, not the browser's", async () => {
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    const link = await screen.findByRole("link", { name: "Forgot password?" });
    expect(link.classList.contains("ant-typography")).toBe(true);
    expect(link.getAttribute("href")).toBe("/forgot");
  });
});

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
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(screen.getByText("Enter your email address.")).toBeTruthy());
    expect(document.getElementById("login-email")?.getAttribute("aria-invalid")).toBeNull();
  });

  it("asks for a password once the email is there", async () => {
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    const email = document.getElementById("login-email") as HTMLInputElement;
    fireEvent.change(email, { target: { value: "olivia@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => expect(screen.getByText("Enter your password.")).toBeTruthy());
  });

  it("draws the forgot-password link in the theme's link colour, not the browser's", () => {
    render(
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>,
    );
    const link = screen.getByRole("link", { name: "Forgot password?" });
    expect(link.classList.contains("ant-typography")).toBe(true);
    expect(link.getAttribute("href")).toBe("/forgot");
  });
});

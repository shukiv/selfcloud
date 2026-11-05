// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { ApiError } from "../lib/apiError";
import { setLang } from "../lib/i18n";
import ConfirmDevicePage from "./ConfirmDevicePage";

// sec2, decision 86: the link that confirms a browser. The person presses the button; a bad link shows its own message,
// and another browser is told to open the link where it asked.
const mocks = vi.hoisted(() => ({ confirmDevice: vi.fn(), establish: vi.fn(), go: vi.fn() }));
vi.mock("../lib/auth", async (orig) => ({
  ...(await orig<typeof import("../lib/auth")>()),
  confirmDevice: mocks.confirmDevice,
  establish: mocks.establish,
}));
vi.mock("../lib/router", async (orig) => ({
  ...(await orig<typeof import("../lib/router")>()),
  go: mocks.go,
}));

function renderWithToken(fragment: string) {
  window.history.replaceState(null, "", `/confirm-device${fragment}`);
  return render(
    <ThemeProvider>
      <ConfirmDevicePage />
    </ThemeProvider>,
  );
}

// The button is found by its words. Its accessible name also carries antd's "loading" label while a request is running.
const confirmButton = (): HTMLElement | null =>
  [...document.querySelectorAll<HTMLElement>("button")].find((b) => b.textContent === "Confirm and sign in") ?? null;

describe("ConfirmDevicePage", () => {
  beforeEach(async () => {
    mocks.confirmDevice.mockReset();
    mocks.establish.mockReset();
    mocks.go.mockReset();
    await setLang("en");
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("with no token, says the link is not usable and offers the sign-in form", () => {
    renderWithToken("");
    expect(screen.getByText("This link has expired or was already used. Sign in again to get a new one.")).toBeTruthy();
    expect(confirmButton()).toBeNull();
    expect(screen.getByRole("link", { name: "Sign in" }).getAttribute("href")).toBe("/login");
  });

  it("signs the browser in when the person presses the button, and sends the token it read", async () => {
    mocks.confirmDevice.mockResolvedValue({ user: { id: "usr_ann", role: "member" }, csrf_token: "x" });
    renderWithToken("#token=abc123");
    // Nothing is sent until the person presses the button, so a link opened by a scanner is not used up.
    expect(mocks.confirmDevice).not.toHaveBeenCalled();
    fireEvent.click(confirmButton() as HTMLElement);
    await waitFor(() => expect(mocks.establish).toHaveBeenCalled());
    expect(mocks.confirmDevice).toHaveBeenCalledWith("abc123");
    expect(mocks.go).toHaveBeenCalled();
  });

  it("tells another browser to open the link where it asked, and keeps the button", async () => {
    mocks.confirmDevice.mockRejectedValue(new ApiError(403, "forbidden", { reason: "other_browser" }, 0));
    renderWithToken("#token=abc123");
    fireEvent.click(confirmButton() as HTMLElement);
    await screen.findByText("Open this link in the browser where you asked to sign in.");
    // The button's name carries "loading" until the request has settled, so the test waits for the button to be named again.
    await waitFor(() => expect(confirmButton()).not.toBeNull());
    expect(mocks.establish).not.toHaveBeenCalled();
  });

  it("gives an expired or used link its own message, and no button", async () => {
    mocks.confirmDevice.mockRejectedValue(new ApiError(400, "invalid_request", {}, 0));
    renderWithToken("#token=abc123");
    fireEvent.click(confirmButton() as HTMLElement);
    await screen.findByText("This link has expired or was already used. Sign in again to get a new one.");
    expect(confirmButton()).toBeNull();
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/api";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";
import EmailChangePage from "./EmailChangePage";

// The confirmation call is replaced, so the tests read what the page sent and show what it does with the answer.
const mocks = vi.hoisted(() => ({ confirm: vi.fn() }));
vi.mock("../lib/auth", async (orig) => ({
  ...(await orig<typeof import("../lib/auth")>()),
  confirmEmailChange: mocks.confirm,
}));

function renderPage() {
  return render(
    <ThemeProvider>
      <EmailChangePage />
    </ThemeProvider>,
  );
}

describe("EmailChangePage confirms a new address from its link", () => {
  beforeEach(() => {
    setLang("en");
    mocks.confirm.mockReset();
    window.history.replaceState(null, "", "/email-change");
  });
  afterEach(() => {
    window.history.replaceState(null, "", "/");
  });

  it("sends the token from the fragment, shows done, and takes the token out of the address bar", async () => {
    mocks.confirm.mockResolvedValue({ id: "usr_1" });
    window.history.replaceState(null, "", "/email-change#token=abc123");
    renderPage();
    expect(await screen.findByText("Your new email address is confirmed. Sign in with it from now on.")).toBeTruthy();
    expect(mocks.confirm).toHaveBeenCalledWith("abc123");
    expect(window.location.hash).toBe("");
    expect(screen.getByRole("button", { name: "Go to sign-in" })).toBeTruthy();
  });

  it("shows that a used, expired or replaced link no longer works", async () => {
    mocks.confirm.mockRejectedValue(new ApiError(400, "invalid_request", {}, 0));
    window.history.replaceState(null, "", "/email-change#token=old");
    renderPage();
    expect(
      await screen.findByText("This link is no longer valid. It was used, has expired, or a newer request replaced it."),
    ).toBeTruthy();
  });

  it("shows a failure that is not the link's fault as a failure, not as a dead link", async () => {
    mocks.confirm.mockRejectedValue(new ApiError(500, "internal", {}, 0));
    window.history.replaceState(null, "", "/email-change#token=abc");
    renderPage();
    expect(await screen.findByText("Your new address could not be confirmed. Try again later.")).toBeTruthy();
  });

  it("does not send anything when the link has no token", async () => {
    renderPage();
    await waitFor(() =>
      expect(screen.getByText("This link is no longer valid. It was used, has expired, or a newer request replaced it.")).toBeTruthy(),
    );
    expect(mocks.confirm).not.toHaveBeenCalled();
  });
});

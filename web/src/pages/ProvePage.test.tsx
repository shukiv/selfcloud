// SPDX-License-Identifier: AGPL-3.0-or-later

import { StrictMode } from "react";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { ThemeProvider } from "../theme/theme";
import { setLang } from "../lib/i18n";
import { fail, stubFetch } from "../test/fetchStub";
import ProvePage from "./ProvePage";

const BAD_LINK = "This link has expired or was already used. Ask whoever invited you to send a new one.";

function renderPage() {
  return render(
    <ThemeProvider>
      <ProvePage />
    </ThemeProvider>,
  );
}

describe("ProvePage proves the address from its mailed link", () => {
  beforeEach(async () => {
    await setLang("en");
    window.history.replaceState(null, "", "/prove");
  });
  afterEach(() => {
    window.history.replaceState(null, "", "/");
  });

  it("posts the fragment's token once, clears the address bar, and shows done with the way to Shared with me", async () => {
    const { calls } = stubFetch(() => ({ status: 204 }));
    window.history.replaceState(null, "", "/prove#token=tok_proof");
    renderPage();
    expect(await screen.findByText("Your email address is proved.")).toBeTruthy();
    expect(window.location.hash).toBe("");
    const posts = calls.filter((c) => c.url === "/api/v1/auth/email-proof");
    expect(posts).toHaveLength(1);
    expect(posts[0].method).toBe("POST");
    expect(posts[0].body).toEqual({ token: "tok_proof" });
    // The token travels in the body only, never in an address.
    expect(calls.every((c) => !c.url.includes("tok_proof"))).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "Shared with me" }));
    await waitFor(() => expect(window.location.pathname).toBe("/shared"));
  });

  it("a refused token shows the bad-link message the verify page shows", async () => {
    stubFetch(() => fail(400, "invalid_request"));
    window.history.replaceState(null, "", "/prove#token=tok_bad");
    renderPage();
    expect(await screen.findByText(BAD_LINK)).toBeTruthy();
    expect(window.location.hash).toBe("");
  });

  it("a link with no token is the bad-link message, and nothing is sent", async () => {
    const { calls } = stubFetch(() => ({ status: 204 }));
    renderPage();
    expect(await screen.findByText(BAD_LINK)).toBeTruthy();
    expect(calls).toHaveLength(0);
  });

  it("the token is read once even when the effect runs twice", async () => {
    const { calls } = stubFetch(() => ({ status: 204 }));
    window.history.replaceState(null, "", "/prove#token=tok_once");
    render(
      <ThemeProvider>
        <StrictMode>
          <ProvePage />
        </StrictMode>
      </ThemeProvider>,
    );
    expect(await screen.findByText("Your email address is proved.")).toBeTruthy();
    expect(calls.filter((c) => c.url === "/api/v1/auth/email-proof")).toHaveLength(1);
  });
});
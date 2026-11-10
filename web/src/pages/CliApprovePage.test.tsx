// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { loadCliText } from "../lib/cli/cliText";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import CliApprovePage from "./CliApprovePage";
import { ThemeProvider } from "../theme/theme";

const page = () => (
  <ThemeProvider>
    <CliApprovePage />
  </ThemeProvider>
);

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const request = {
  client_name: "selfcloud CLI on laptop",
  client_ip: "203.0.113.7",
  user_agent: "selfcloud/1.0",
  created_at: "2026-11-10T08:00:00Z",
  expires_at: "2026-11-10T08:10:00Z",
};

// The page's words are their own catalogue, which the page loads itself. These tests read them at once, so they preload it;
// the lazy path is tested in CliApprovePage.lazy.test.tsx.
beforeAll(() => loadCliText("en"));
beforeEach(() => session.set(signedAs("olivia")));
afterEach(() => {
  session.set(null);
  window.history.replaceState(null, "", "/");
});

// The code is typed into the page and sent in the body. It is never read from the address, and the page does not put it there.
describe("the approval page", () => {
  it("looks up the typed code in the body, never in the address", async () => {
    const before = window.location.href;
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/lookup") return { status: 200, body: request };
      return fail(404, "not_found");
    });
    render(page());
    fireEvent.change(screen.getByLabelText("Code from the terminal"), { target: { value: "bcdf-ghjk" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await screen.findByText("selfcloud CLI on laptop");
    expect(screen.getByText("203.0.113.7")).toBeTruthy();
    expect(calls).toHaveLength(1);
    expect(calls[0].body).toEqual({ user_code: "bcdf-ghjk" });
    expect(window.location.href).toBe(before);
  });

  // The name is the terminal's own choice, and the page says so; it warns against a code someone else gave the person
  // (device-code phishing, decision 125).
  it("says the terminal named the sign-in, and warns against a code someone else gave", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/lookup") return { status: 200, body: request };
      return fail(404, "not_found");
    });
    render(page());
    fireEvent.change(screen.getByLabelText("Code from the terminal"), { target: { value: "BCDFGHJK" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await screen.findByText("selfcloud CLI on laptop");
    expect(screen.getByText("The name was chosen by the terminal. It is not checked.")).toBeTruthy();
    expect(screen.getByText(/Approve only a code you started yourself/)).toBeTruthy();
  });

  it("approves the shown sign-in with the same code, then says it is done", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/lookup") return { status: 200, body: request };
      if (c.url === "/api/v1/auth/cli/approve") return { status: 200, body: { status: "approved" } };
      return fail(404, "not_found");
    });
    render(page());
    fireEvent.change(screen.getByLabelText("Code from the terminal"), { target: { value: "BCDFGHJK" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.click(await screen.findByRole("button", { name: "Approve sign-in" }));
    await screen.findByText("Approved. You can go back to the terminal.");
    const approve = calls.find((c) => c.url === "/api/v1/auth/cli/approve");
    expect(approve?.body).toEqual({ user_code: "BCDFGHJK" });
  });

  it("'Not me' leaves the sign-in unapproved and sends nothing", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/lookup") return { status: 200, body: request };
      return fail(404, "not_found");
    });
    render(page());
    fireEvent.change(screen.getByLabelText("Code from the terminal"), { target: { value: "BCDFGHJK" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.click(await screen.findByRole("button", { name: "Not me" }));
    await waitFor(() => expect(screen.getByLabelText("Code from the terminal")).toBeTruthy());
    expect(calls.some((c) => c.url === "/api/v1/auth/cli/approve")).toBe(false);
  });

  it("a wrong code says so, and nothing is approved", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/lookup") return fail(400, "wrong_code", { reason: "wrong_code" });
      return fail(404, "not_found");
    });
    render(page());
    fireEvent.change(screen.getByLabelText("Code from the terminal"), { target: { value: "BCDFGHJK" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    await screen.findByText("That code doesn't match a sign-in. Check it and try again.");
    expect(calls.some((c) => c.url === "/api/v1/auth/cli/approve")).toBe(false);
  });

  it("a must-change user is told to change the password first", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/cli/lookup") return { status: 200, body: request };
      if (c.url === "/api/v1/auth/cli/approve") return fail(403, "forbidden", { reason: "password_change_required" });
      return fail(404, "not_found");
    });
    render(page());
    fireEvent.change(screen.getByLabelText("Code from the terminal"), { target: { value: "BCDFGHJK" } });
    fireEvent.click(screen.getByRole("button", { name: "Continue" }));
    fireEvent.click(await screen.findByRole("button", { name: "Approve sign-in" }));
    await screen.findByText("Change your password first, then approve the sign-in.");
  });
});

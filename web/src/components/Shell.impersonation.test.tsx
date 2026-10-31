// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../lib/session";
import { setLang } from "../lib/i18n";
import { get } from "../lib/store";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import Shell from "./Shell";

const member = { id: "usr_m", email: "mateo@example.com", display_name: "Mateo Member", role: "member", locale: "en", status: "active" };

function impersonating(): Session {
  return {
    user: member,
    account: { id: "acc_1", name: "Acme" },
    csrf: "imp-csrf",
    publicLinks: false,
    impersonation: { endsAt: "2026-10-31T13:00:00Z" },
  } as unknown as Session;
}

function renderShell(path: string) {
  const router = createMemoryRouter([{ path: "*", element: <Shell><p>page body</p></Shell> }], { initialEntries: [path] });
  render(
    <ThemeProvider>
      <Direction>
        <RouterProvider router={router} />
      </Direction>
    </ThemeProvider>,
  );
  return router;
}

beforeEach(async () => {
  window.localStorage.clear();
  await setLang("en");
});

describe("the impersonation banner", () => {
  it("names the person across the page, with the way back, and no sign-out", async () => {
    session.set(impersonating());
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderShell("/files");
    const banner = screen.getByTestId("impersonation-banner");
    expect(banner.getAttribute("role")).toBe("status");
    expect(banner.textContent).toContain("Signed in as Mateo Member.");
    expect(screen.getByRole("button", { name: "Go back to admin" })).toBeTruthy();
    expect(screen.getByText("page body")).toBeTruthy();
    fireEvent.click(screen.getByTestId("account-menu"));
    expect(await screen.findByText("Change password")).toBeTruthy();
    expect(screen.queryByText("Sign out")).toBeNull();
  });

  it("is not shown to a person who is not impersonated", () => {
    session.set({ ...impersonating(), impersonation: null });
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderShell("/files");
    expect(screen.queryByTestId("impersonation-banner")).toBeNull();
  });

  it("the way back restores the admin session and sends the browser to the tenants list", async () => {
    session.set(impersonating());
    const admin = { id: "adm_1", email: "ada@example.com", display_name: "Ada", role: "admin", locale: "en", status: "active" };
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/auth/impersonation/end") return { status: 200, body: { user: admin, csrf_token: "admin-csrf" } };
      if (c.url === "/api/v1/me") return { status: 200, body: { user: admin, account: null, csrf_token: "admin-csrf" } };
      return fail(500, "internal");
    });
    renderShell("/files");
    fireEvent.click(screen.getByRole("button", { name: "Go back to admin" }));
    await waitFor(() => expect(get(session)?.user.role).toBe("admin"));
    expect(get(session)?.csrf).toBe("admin-csrf");
    expect(calls.filter((c) => c.url === "/api/v1/auth/impersonation/end")).toHaveLength(1);
  });
});

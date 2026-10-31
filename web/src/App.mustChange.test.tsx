// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";
import { Direction, Gate } from "./App";
import { session, type Session } from "./lib/session";
import { setLang } from "./lib/i18n";
import { stubFetch } from "./test/fetchStub";
import { ThemeProvider } from "./theme/theme";

// A person who must change the password is kept on that page: every other address goes there.
function renderAt(path: string) {
  const router = createMemoryRouter([{ path: "*", element: <Gate /> }], { initialEntries: [path] });
  render(
    <ThemeProvider>
      <Direction>
        <RouterProvider router={router} />
      </Direction>
    </ThemeProvider>,
  );
  return router;
}

const member = { id: "usr_m", email: "mateo@example.com", display_name: "Mateo", role: "member", locale: "en", status: "active" };

beforeEach(async () => {
  window.localStorage.clear();
  stubFetch(() => ({ status: 200, body: { items: [] } }));
  await setLang("en");
  session.set({ user: member, account: null, csrf: "csrf", publicLinks: false, mustChange: true } as unknown as Session);
});

describe("a person who must change the password", () => {
  it.each(["/files", "/", "/trash", "/login"])("is sent from %s to the change-password page", async (path) => {
    const router = renderAt(path);
    await waitFor(() => expect(router.state.location.pathname).toBe("/change-password"));
    expect(await screen.findByText("Choose a new password before you continue. Nothing else is open until you do.")).toBeTruthy();
  });
});

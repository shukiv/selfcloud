// SPDX-License-Identifier: AGPL-3.0-or-later

// A second move to the root while the gate's redirect is running: the router completes the redirect's destination and
// the move back to the root before React draws either, so the gate draws the root again with the same redirect still
// mounted. The redirect must run again and leave the root, not sit there drawing nothing (the x2 matrix's sign-out
// clicks a link to the root while the boot is pending, and the page stayed blank at /).

import { act, render } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { Gate } from "./App";
import { session, type Session } from "./lib/session";
import { setLang } from "./lib/i18n";
import { ThemeProvider } from "./theme/theme";

vi.mock("./components/Shell", async () => (await import("./test/stubPage")).stubShell());
vi.mock("./pages/FilesPage", async () => (await import("./test/stubPage")).stubPage("files"));

const member = {
  user: { id: "usr_m", email: "mateo@example.com", display_name: "Mateo", role: "member", locale: "en", status: "active" },
  account: null,
  csrf: "csrf",
  publicLinks: false,
} as unknown as Session;

// race renders the gate at the address, and the moment the redirect's destination lands in the router, sends a second
// move back to the starting address — before React draws the destination, as the link click lands while the boot is
// pending. The router's own updates are batched into one draw, so the redirect stays mounted across the move.
function race(start: string, destination: string) {
  const router = createMemoryRouter([{ path: "*", element: <Gate /> }], { initialEntries: [start] });
  let moved = false;
  router.subscribe((state) => {
    if (!moved && state.location.pathname === destination) {
      moved = true;
      void router.navigate(start);
    }
  });
  return router;
}

async function drawn(router: ReturnType<typeof race>) {
  await act(async () => {
    render(
      <ThemeProvider>
        <RouterProvider router={router} />
      </ThemeProvider>,
    );
    await new Promise((r) => setTimeout(r, 50));
  });
  return router.state.location.pathname;
}

beforeEach(async () => {
  window.localStorage.clear();
  session.set(null);
  await setLang("en");
});

afterEach(() => {
  session.set(null);
});

describe("a second move to the root while its redirect runs", () => {
  it("a signed-in person still leaves the root for My files", async () => {
    session.set(member);
    expect(await drawn(race("/", "/files"))).toBe("/files");
  });

  it("a signed-out visitor still leaves the root for the sign-in page", async () => {
    expect(await drawn(race("/", "/login"))).toBe("/login");
  });

  it("a signed-in person on the sign-in page still leaves it for home", async () => {
    session.set(member);
    expect(await drawn(race("/login", "/files"))).toBe("/files");
  });
});
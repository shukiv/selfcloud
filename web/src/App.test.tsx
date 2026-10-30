// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";
import { Direction, Gate, viewFor } from "./App";
import { session } from "./lib/session";
import { setLang } from "./lib/i18n";
import { ThemeProvider } from "./theme/theme";

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

describe("viewFor", () => {
  it("reads the address the way the Svelte build does", () => {
    expect(viewFor("/")).toBe("home");
    expect(viewFor("/files/abc")).toBe("files");
    expect(viewFor("/n/abc")).toBe("node");
    expect(viewFor("/manage/people")).toBe("manage");
    expect(viewFor("/admin/tenants")).toBe("admin");
    expect(viewFor("/sign-in")).toBe("sign-in");
    expect(viewFor("/nope")).toBe("notfound");
  });
});

describe("Gate", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    session.set(null);
    await setLang("en");
  });

  it("sends a signed-out visitor from a shell page to sign-in, with a way back", async () => {
    const router = renderAt("/trash");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toBe("?next=%2Ftrash");
  });

  it("shows the sign-in page to a signed-out visitor", async () => {
    renderAt("/forgot");
    expect(await screen.findByText("Forgot password")).toBeTruthy();
  });

  it("does not send a signed-in person from sign-in back to sign-in: a next of /login goes straight home", async () => {
    session.set({
      user: { id: "u1", email: "m@example.com", display_name: "M", role: "member", locale: "en" } as never,
      account: null,
      csrf: "csrf",
      publicLinks: false,
    });
    // Every address the router visits is recorded, from before the first render.
    const router = createMemoryRouter([{ path: "*", element: <Gate /> }], { initialEntries: ["/login?next=/login"] });
    const visited: string[] = [];
    router.subscribe((state) => visited.push(state.location.pathname));
    render(
      <ThemeProvider>
        <Direction>
          <RouterProvider router={router} />
        </Direction>
      </ThemeProvider>,
    );
    await waitFor(() => expect(router.state.location.pathname).toBe("/"));
    expect(visited).not.toContain("/login");
  });
});

describe("Direction", () => {
  it("sets the document direction from the language", async () => {
    await setLang("he");
    render(
      <ThemeProvider>
        <Direction>
          <p>x</p>
        </Direction>
      </ThemeProvider>,
    );
    expect(document.documentElement.dir).toBe("rtl");
    expect(document.documentElement.lang).toBe("he");
    await setLang("en");
    expect(document.documentElement.dir).toBe("ltr");
  });
});

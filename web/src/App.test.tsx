// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { Direction, Gate, viewFor } from "./App";
import { officeEdit } from "./lib/files/editor";
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
    expect(viewFor("/")).toBe("files");
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

  it("does not send a signed-in person from sign-in back to sign-in: a next of /login goes straight to My files", async () => {
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
    await waitFor(() => expect(router.state.location.pathname).toBe("/files"));
    expect(visited).not.toContain("/login");
  });
});

describe("the office editor page (decision 102)", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    session.set(null);
    officeEdit.set(true);
    await setLang("en");
  });

  afterEach(() => {
    officeEdit.set(false);
  });

  it("keeps the sign-in gate: a signed-out visitor is sent to sign-in with the editor's address as next", async () => {
    const router = renderAt("/edit/nod_a1?mode=edit");
    await waitFor(() => expect(router.state.location.pathname).toBe("/login"));
    expect(router.state.location.search).toBe("?next=%2Fedit%2Fnod_a1%3Fmode%3Dedit");
  });

  it("refuses an installation admin the page, as the drive pages do", async () => {
    session.set({
      user: { id: "a1", email: "a@example.com", display_name: "A", role: "admin", locale: "en" } as never,
      account: null,
      csrf: "csrf",
      publicLinks: false,
    });
    renderAt("/edit/nod_a1?mode=edit");
    await waitFor(() => expect(screen.queryByTestId("office-editor")).toBeNull());
    expect(document.querySelector(".ant-layout")).toBeNull();
  });

  it("draws a signed-in person's editor full page: no shell around it, with the bar", async () => {
    session.set({
      user: { id: "u1", email: "m@example.com", display_name: "M", role: "member", locale: "en" } as never,
      account: null,
      csrf: "csrf",
      publicLinks: false,
    });
    renderAt("/edit/nod_a1?mode=edit&from=%2Ffiles%2Ffld_b");
    expect(await screen.findByTestId("office-editor")).toBeTruthy();
    expect(document.querySelector(".ant-layout")).toBeNull();
    expect(screen.getByRole("button", { name: /close/i })).toBeTruthy();
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

// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../lib/session";
import { setLang } from "../lib/i18n";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import Shell from "./Shell";

function signIn(role: "owner" | "member" | "admin") {
  const s: Session = {
    user: { id: "u1", email: "olivia@example.com", display_name: "Olivia", role, locale: "en" } as Session["user"],
    account: null,
    csrf: "csrf",
    publicLinks: false,
  };
  session.set(s);
}

function renderShell(path: string) {
  const router = createMemoryRouter([{ path: "*", element: <Shell><p>page body</p></Shell> }], {
    initialEntries: [path],
  });
  return render(
    <ThemeProvider>
      <Direction>
        <RouterProvider router={router} />
      </Direction>
    </ThemeProvider>,
  );
}

// The test document has no layout, so the page is drawn at phone width: the sidebar is the drawer,
// which opens from the menu button.
async function openNav(): Promise<HTMLElement> {
  fireEvent.click(screen.getByTestId("nav-open"));
  const drawer = await waitFor(() => {
    const el = document.querySelector<HTMLElement>(".ant-drawer-open");
    if (!el) throw new Error("the drawer is not open");
    return el;
  });
  return drawer;
}

describe("Shell", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
  });

  it("draws the page inside and lists the member's destinations", async () => {
    signIn("member");
    renderShell("/files");
    expect(screen.getByText("page body")).toBeTruthy();
    const nav = await openNav();
    const names = within(nav).getAllByRole("menuitem").map((m) => m.textContent);
    expect(names).toContain("My files");
    expect(names).not.toContain("Requests");
    expect(names).not.toContain("People");
  });

  it("gives an owner the Requests entry and the owner's pages", async () => {
    signIn("owner");
    renderShell("/");
    const nav = await openNav();
    const names = within(nav).getAllByRole("menuitem").map((m) => m.textContent);
    expect(names).toContain("Requests");
    expect(names).toContain("People");
  });

  it("gives an installation admin the administration pages only", async () => {
    signIn("admin");
    renderShell("/admin/tenants");
    const nav = await openNav();
    const names = within(nav).getAllByRole("menuitem").map((m) => m.textContent);
    expect(names).toContain("Tenants");
    expect(names).not.toContain("My files");
  });

  it("offers the account menu with the account's name", async () => {
    signIn("member");
    renderShell("/files");
    expect(screen.getByRole("button", { name: "Account menu" }).textContent).toContain("Olivia");
  });

  it("opens the drawer on the reading side: the right, in Hebrew", async () => {
    await setLang("he");
    signIn("member");
    renderShell("/files");
    await openNav();
    expect(document.querySelector(".ant-drawer-right")).not.toBeNull();
    expect(document.querySelector(".ant-drawer-left")).toBeNull();
  });

  it("folds the sidebar to the 88px rail and remembers the choice", async () => {
    // A wide screen: the sidebar is the sider, not the drawer.
    const original = window.matchMedia;
    window.matchMedia = (query: string) => ({ ...original(query), matches: true }) as MediaQueryList;
    try {
      signIn("member");
      renderShell("/files");
      fireEvent.click(screen.getByRole("button", { name: "Hide" }));
      expect(window.localStorage.getItem("selfcloud.navRail")).toBe("1");
      const sider = await waitFor(() => {
        const el = document.querySelector<HTMLElement>(".ant-layout-sider-collapsed");
        if (!el) throw new Error("the sider is not collapsed");
        return el;
      });
      expect(sider.style.width).toBe("88px");
    } finally {
      window.matchMedia = original;
    }
  });
});

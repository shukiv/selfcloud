// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../lib/session";
import { setLang } from "../lib/i18n";
import { ThemeProvider } from "../theme/theme";
import { Direction } from "../App";
import type { FolderAccess } from "../lib/files/types";
import Shell from "./Shell";
import { ApiError } from "../lib/apiError";
import { get } from "../lib/store";

// The folder lists and the hide call are replaced: each test states what the server answers.
const mocks = vi.hoisted(() => ({
  loadFolders: vi.fn(async () => []),
  loadHiddenFolders: vi.fn(async () => []),
  setHidden: vi.fn(async () => undefined),
  listAccessRequests: vi.fn(async () => ({ items: [] as unknown[] })),
}));
vi.mock("../lib/files/folders", async () => {
  const { userWritable } = await import("../lib/store");
  return {
    folders: userWritable([]),
    hiddenFolders: userWritable([]),
    loadFolders: mocks.loadFolders,
    loadHiddenFolders: mocks.loadHiddenFolders,
  };
});
vi.mock("../lib/sharing/api", () => ({ setHidden: mocks.setHidden, listAccessRequests: mocks.listAccessRequests }));

import { folders, hiddenFolders } from "../lib/files/folders";

function team(id: string, name: string, extra: Partial<FolderAccess> = {}): FolderAccess {
  return {
    id,
    name,
    kind: "team",
    access: "read",
    role: "viewer",
    members_only: false,
    sharing_managers_only: false,
    no_download_for_viewers: false,
    allow_links: false,
    ...extra,
  };
}

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
    folders.set([]);
    hiddenFolders.set([]);
    mocks.loadFolders.mockClear();
    mocks.loadHiddenFolders.mockReset().mockResolvedValue([]);
    mocks.setHidden.mockClear();
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
      // The upload tray has a "Hide" button of its own, so the rail toggle is found by its test id.
      fireEvent.click(screen.getByTestId("rail-toggle"));
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

  describe("team spaces and hidden spaces", () => {
    function teamsIn(drawer: HTMLElement): string[] {
      return within(drawer.querySelector('ul[aria-label="Team spaces"]') as HTMLElement)
        .queryAllByRole("link")
        .map((a) => a.querySelector("bdi")?.textContent ?? "");
    }

    it("lists the visible team spaces, and no hidden one until the caller asks for them", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A")]);
      hiddenFolders.set([team("fld_b", "Team B", { hidden: true })]);
      renderShell("/files");
      const nav = await openNav();
      expect(teamsIn(nav)).toEqual(["Team A"]);
      expect(within(nav).queryByText("Team B")).toBeNull();
      fireEvent.click(within(nav).getByRole("checkbox", { name: "Show hidden spaces" }));
      await waitFor(() => expect(teamsIn(nav)).toEqual(["Team A", "Team B"]));
      expect(within(nav).getByText("Hidden")).toBeTruthy();
      expect(mocks.loadHiddenFolders).toHaveBeenCalledTimes(1);
    });

    it("lists only team spaces, never a personal folder", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A"), team("fld_p", "Mine", { kind: "personal" })]);
      renderShell("/files");
      const nav = await openNav();
      expect(teamsIn(nav)).toEqual(["Team A"]);
    });

    it("Hide calls setHidden for that space and then reads both lists again", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A")]);
      renderShell("/files");
      const nav = await openNav();
      fireEvent.click(within(nav).getByRole("button", { name: "Options for Team A" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Hide" }));
      await waitFor(() => expect(mocks.setHidden).toHaveBeenCalledWith("fld_a", true));
      await waitFor(() => expect(mocks.loadFolders).toHaveBeenCalled());
    });

    it("Show on a hidden space calls setHidden with false", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A")]);
      hiddenFolders.set([team("fld_b", "Team B", { hidden: true })]);
      renderShell("/files");
      const nav = await openNav();
      fireEvent.click(within(nav).getByRole("checkbox", { name: "Show hidden spaces" }));
      fireEvent.click(await within(nav).findByRole("button", { name: "Options for Team B" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Show" }));
      await waitFor(() => expect(mocks.setHidden).toHaveBeenCalledWith("fld_b", false));
    });

    it("a failed read of the hidden list keeps the list that was there", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A")]);
      hiddenFolders.set([team("fld_b", "Team B", { hidden: true })]);
      mocks.loadHiddenFolders.mockRejectedValue(new Error("network"));
      renderShell("/files");
      const nav = await openNav();
      fireEvent.click(within(nav).getByRole("checkbox", { name: "Show hidden spaces" }));
      await waitFor(() => expect(mocks.loadHiddenFolders).toHaveBeenCalled());
      await waitFor(() => expect(teamsIn(nav)).toEqual(["Team A", "Team B"]));
    });

    // A failure is shown by its code. The server's own text (here "server text") never reaches the page.
    it("a failed read of the spaces shows its code's message, never the server text", async () => {
      signIn("member");
      mocks.loadFolders.mockRejectedValueOnce(new ApiError(500, "internal", { detail: "server text" }, 0));
      renderShell("/files");
      const nav = await openNav();
      const alert = await within(nav).findByRole("alert");
      expect(alert.textContent).toContain("The server had a problem");
      expect(alert.textContent).not.toContain("server text");
    });

    it("a refused hide shows the refusal by its code, and the page stays", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A")]);
      mocks.setHidden.mockRejectedValueOnce(new ApiError(403, "forbidden", { detail: "server text" }, 0));
      renderShell("/files");
      const nav = await openNav();
      fireEvent.click(within(nav).getByRole("button", { name: "Options for Team A" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Hide" }));
      const alert = await within(nav).findByRole("alert");
      expect(alert.textContent).not.toContain("server text");
      expect(screen.getByText("page body")).toBeTruthy();
    });

    it("a change of person clears the error", async () => {
      signIn("member");
      mocks.loadFolders.mockRejectedValueOnce(new ApiError(500, "internal", {}, 0));
      renderShell("/files");
      const nav = await openNav();
      await within(nav).findByRole("alert");
      act(() => session.set({ ...(get(session) as Session), csrf: "other" } as Session));
      await waitFor(() => expect(within(nav).queryByRole("alert")).toBeNull());
    });

    it("offers Space settings to a manager of the space", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A", { role: "manager" })]);
      renderShell("/files");
      const nav = await openNav();
      fireEvent.click(within(nav).getByRole("button", { name: "Options for Team A" }));
      expect(await screen.findByRole("menuitem", { name: "Space settings" })).toBeTruthy();
    });

    it("offers no Space settings to a viewer of the space", async () => {
      signIn("member");
      folders.set([team("fld_c", "Team C", { role: "viewer" })]);
      renderShell("/files");
      const nav = await openNav();
      fireEvent.click(within(nav).getByRole("button", { name: "Options for Team C" }));
      await screen.findByRole("menuitem", { name: "Hide" });
      expect(screen.queryByRole("menuitem", { name: "Space settings" })).toBeNull();
    });

    it("a change of person clears the hidden toggle", async () => {
      signIn("member");
      hiddenFolders.set([team("fld_b", "Team B", { hidden: true })]);
      folders.set([team("fld_a", "Team A")]);
      renderShell("/files");
      const nav = await openNav();
      fireEvent.click(within(nav).getByRole("checkbox", { name: "Show hidden spaces" }));
      await waitFor(() => expect(teamsIn(nav)).toEqual(["Team A", "Team B"]));
      // Another person (another ID and token), not a renewed session of the same one.
      act(() =>
        session.set({
          user: { id: "u2", email: "pat@example.com", display_name: "Pat", role: "member", locale: "en" } as Session["user"],
          account: null,
          csrf: "other",
          publicLinks: false,
        }),
      );
      await waitFor(() => expect((within(nav).getByRole("checkbox", { name: "Show hidden spaces" }) as HTMLInputElement).checked).toBe(false));
    });
  });
});

describe("the Requests entry", () => {
  beforeEach(() => {
    mocks.listAccessRequests.mockResolvedValue({ items: [] });
  });

  // An installation admin is not an owner of any requests: the read is refused (403) and the console shows it. Svelte
  // reads the count for members and owners only.
  it("an installation admin sends no access-request read", async () => {
    signIn("admin");
    renderShell("/admin/tenants");
    await openNav();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 30));
    });
    expect(mocks.listAccessRequests).not.toHaveBeenCalled();
  });

  it("is shown with its count to a member who has requests waiting to be decided, and is not shown otherwise", async () => {
    signIn("member");
    mocks.listAccessRequests.mockResolvedValue({ items: [{ id: "arq_a" }, { id: "arq_b" }] });
    renderShell("/");
    const drawer = await openNav();
    const entry = await waitFor(() => within(drawer).getByRole("menuitem", { name: /Requests/ }));
    expect(entry.textContent).toContain("2 waiting");
  });

  it("is not shown to a member with nothing waiting", async () => {
    signIn("member");
    mocks.listAccessRequests.mockResolvedValue({ items: [] });
    renderShell("/");
    const drawer = await openNav();
    await waitFor(() => within(drawer).getByRole("menuitem", { name: /Shared with me/ }));
    expect(within(drawer).queryByRole("menuitem", { name: /Requests/ })).toBeNull();
  });
});


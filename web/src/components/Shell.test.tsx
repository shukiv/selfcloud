// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../lib/session";
import { setLang } from "../lib/i18n";
import { ThemeProvider, antdTokens } from "../theme/theme";
import { contrast } from "../lib/brand";
import { Direction } from "../App";
import type { FolderAccess } from "../lib/files/types";
import Shell from "./Shell";
import { ApiError } from "../lib/apiError";
import { get } from "../lib/store";

import { createQueryClient } from "../lib/query";
import { treePlace } from "../lib/files/treePlace";
import { myFilesPlace, newPlace, newPlaceFailure, type NewPlace } from "../lib/files/newPlace";
import { myStorage } from "../lib/storage/api";
import { officeEdit } from "../lib/files/editor";
import { manager } from "../lib/files/uploads";
import { formatBytes } from "../lib/files/format";
import { PALETTE, SIDEBAR } from "../theme/tokens";
import { sidebarRow } from "../theme/sidebarRow";
import { theme as antdTheme } from "antd";

// hexOfRgb turns the rgb() string the DOM reports for an inline colour into #RRGGBB, for the contrast function.
function hexOfRgb(css: string): string {
  const [r, g, b] = (css.match(/\d+/g) ?? ["0", "0", "0"]).map(Number);
  return `#${[r, g, b].map((v) => (v ?? 0).toString(16).padStart(2, "0")).join("")}`.toUpperCase();
}

// The folder lists and the hide call are replaced: each test states what the server answers.
const mocks = vi.hoisted(() => ({
  loadFolders: vi.fn(async () => []),
  loadHiddenFolders: vi.fn(async () => []),
  setHidden: vi.fn(async () => undefined),
  listAccessRequests: vi.fn(async () => ({ items: [] as unknown[] })),
  mkdir: vi.fn(async (_folder: string, path: string) => ({ id: "nod_new", name: path.split("/").pop() ?? "", kind: "folder" })),
}));
vi.mock("../lib/files/api", async (importOriginal) => ({ ...(await importOriginal<typeof import("../lib/files/api")>()), mkdir: mocks.mkdir }));
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
// Space settings stands in as a button that reports a save, so the shell's re-read after a save can be driven.
vi.mock("./share/SpaceSettings", () => ({
  default: ({ onsaved }: { onsaved: () => void }) => (
    <button type="button" onClick={onsaved}>
      settings saved
    </button>
  ),
}));

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
  // The bell of the top bar and the folder tree read through a query client, as the app does (main.tsx).
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ThemeProvider>
        <Direction>
          <RouterProvider router={router} />
        </Direction>
      </ThemeProvider>
    </QueryClientProvider>,
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

// tooltipText waits for a tooltip popup to open and returns its text.
const tooltipText = () =>
  waitFor(() => {
    const text = document.querySelector('[role="tooltip"]')?.textContent;
    if (!text) throw new Error("no tooltip yet");
    return text;
  });

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

  it("makes the sidebar's Trash entry a drop target for the drag gesture, and no other menu entry (decision 104)", async () => {
    signIn("member");
    folders.set([team("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    renderShell("/files");
    const nav = await openNav();
    const trash = within(nav).getByRole("menuitem", { name: "Trash" });
    expect(trash.dataset.dropTrash).toBe("");
    expect(trash.dataset.dropName).toBe("Trash");
    // Every other entry of the menu is no Trash target: none of them carries the attribute.
    const others = within(nav).getAllByRole("menuitem").filter((item) => item !== trash);
    expect(others.length).toBeGreaterThan(1);
    for (const item of others) expect(item.dataset.dropTrash, item.textContent ?? "").toBeUndefined();
  });

  it("draws the page inside and lists the member's destinations", async () => {
    signIn("member");
    folders.set([team("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    renderShell("/files");
    expect(screen.getByText("page body")).toBeTruthy();
    const nav = await openNav();
    // My files is the first row of the folder tree, above the menu; the menu holds the other pages.
    const names = within(nav).getAllByRole("menuitem").map((m) => m.textContent);
    expect(names).not.toContain("My files");
    expect(names).toContain("Starred");
    // The tree is lazy: it is there once its chunk has loaded.
    await waitFor(() => expect(nav.querySelector('[role="tree"] > [role="treeitem"] .tree-row bdi')?.textContent).toBe("My files"));
    expect(names).not.toContain("Home");
    expect(names).not.toContain("Requests");
    expect(names).not.toContain("People");
  });

  // Decision 69: the top bar has no brand, so it has no link to the home page; the search box starts it.
  it("the top bar holds no brand link, for a member or an admin", async () => {
    signIn("member");
    const { unmount } = renderShell("/files");
    expect(document.querySelector("header a")).toBeNull();
    unmount();
    signIn("admin");
    renderShell("/admin/tenants");
    expect(document.querySelector("header a")).toBeNull();
  });

  it("gives an owner the Requests entry and the owner's pages", async () => {
    signIn("owner");
    renderShell("/files");
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

  it("names the installation admin's last entry Settings, for /admin/settings", async () => {
    signIn("admin");
    renderShell("/admin/settings");
    const nav = await openNav();
    const names = within(nav).getAllByRole("menuitem").map((m) => m.textContent);
    expect(names).toEqual(["Tenants", "Admins", "Settings"]);
    expect(within(nav).getByRole("menuitem", { name: "Settings" }).className).toContain("ant-menu-item-selected");
  });

  // Decision 68: the account is a round avatar with the first letter of the name; the name is its tooltip and its name.
  it("shows the first letter of the name in the account button, and the name in its tooltip and name", async () => {
    signIn("member");
    renderShell("/files");
    const button = screen.getByRole("button", { name: "Olivia" });
    expect(button.textContent).toBe("O");
    fireEvent.pointerEnter(button, { pointerType: "mouse" });
    expect(await tooltipText()).toBe("Olivia");
  });

  // Review fix (decision 68): the phone's menu button says what it does (its name and tooltip).
  it("names the phone's menu button Open the menu, and shows that tooltip", async () => {
    renderShell("/files");
    const button = screen.getByTestId("nav-open");
    expect(button.getAttribute("aria-label")).toBe("Open the menu");
    fireEvent.pointerEnter(button.parentElement as HTMLElement, { pointerType: "mouse" });
    expect(await tooltipText()).toBe("Open the menu");
  });

  // Review fix (decision 68): with no name and no email the button shows a person icon, and is named "Account menu".
  it("shows a person icon, named Account menu, when there is no name and no email", async () => {
    session.set({
      user: { id: "u1", email: "", display_name: "", role: "member", locale: "en" } as Session["user"],
      account: null,
      csrf: "csrf",
      publicLinks: false,
    });
    renderShell("/files");
    const button = screen.getByRole("button", { name: "Account menu" });
    expect(button.textContent).toBe("");
    expect(button.querySelector("svg")).not.toBeNull();
    fireEvent.pointerEnter(button, { pointerType: "mouse" });
    expect(await tooltipText()).toBe("Account menu");
  });

  // The account button is one letter, so a long name never widens the header; the full email is its tooltip.
  it("shows one letter for a long email, and the full email in its tooltip", async () => {
    const long = `${"a".repeat(50)}@selfcloud.test`;
    session.set({
      user: { id: "u1", email: long, display_name: "", role: "member", locale: "en" } as Session["user"],
      account: null,
      csrf: "csrf",
      publicLinks: false,
    });
    renderShell("/files");
    const button = screen.getByRole("button", { name: long });
    expect(button.textContent).toBe("A");
    fireEvent.pointerEnter(button, { pointerType: "mouse" });
    expect(await tooltipText()).toBe(long);
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
      // Review fix (decision 68): its name says what it does, before and after the press.
      expect(screen.getByTestId("rail-toggle").getAttribute("aria-label")).toBe("Hide the sidebar");
      // The chevrons point along the reading direction, so they mirror in a right-to-left page (index.css .icon-dir).
      expect(screen.getByTestId("rail-toggle").querySelector("svg")?.classList.contains("icon-dir")).toBe(true);
      fireEvent.click(screen.getByTestId("rail-toggle"));
      expect(screen.getByTestId("rail-toggle").getAttribute("aria-label")).toBe("Show the sidebar");
      expect(screen.getByTestId("rail-toggle").querySelector("svg")?.classList.contains("icon-dir")).toBe(true);
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
    // The teams are the tree's top rows after My files (each space is a row of the one tree, named in its own bdi).
    function teamsIn(drawer: HTMLElement): string[] {
      return Array.from(drawer.querySelectorAll<HTMLElement>('[role="tree"] > [role="treeitem"] > .tree-row'))
        .map((row) => row.querySelector("bdi")?.textContent ?? "")
        .filter((name) => name !== "My files");
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

    it("marks a team space as the current place, and not My files as well", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A"), team("fld_p", "Mine", { kind: "personal" })]);
      treePlace.set({ folder: "fld_a", chain: [] });
      renderShell("/files/fld_a");
      const nav = await openNav();
      expect(nav.querySelector('[data-tree-key="fld_a"]')?.getAttribute("aria-current")).toBe("location");
      expect(nav.querySelector('[data-tree-key="fld_p"]')?.getAttribute("aria-current")).toBeNull();
    });

    it("draws the current team's options button in the row's mark colour, and the others in the sidebar's text", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A"), team("fld_b", "Team B")]);
      treePlace.set({ folder: "fld_a", chain: [] });
      renderShell("/files/fld_a");
      const nav = await openNav();
      // The button takes the colour of its row: the current row is the mark colour (decision 69, option B), another row is white on the blue.
      const ink = async (name: string) => {
        const button = await within(nav).findByRole("button", { name: `Options for ${name}` });
        const row = button.closest<HTMLElement>(".tree-row");
        return { button: button.style.color, row: row?.style.color };
      };
      const css = (c: string) => {
        const d = document.createElement("div");
        d.style.color = c;
        return d.style.color;
      };
      // The top-level current place is filled with the active colour, and its options button takes the on-active text (decision 72).
      expect(await ink("Team A")).toEqual({ button: "inherit", row: css(SIDEBAR.onActive) });
      expect(await ink("Team B")).toEqual({ button: "inherit", row: css(SIDEBAR.text) });
    });

    it("keeps My files as the current place inside the caller's own folder", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A"), team("fld_p", "Mine", { kind: "personal" })]);
      treePlace.set({ folder: "fld_p", chain: [] });
      renderShell("/files/fld_p");
      const nav = await openNav();
      expect(nav.querySelector('[data-tree-key="fld_p"]')?.getAttribute("aria-current")).toBe("location");
    });

    // The place is marked on the files pages only: on another page the tree is there, and marks nothing.
    it("marks no folder on a page that is not a files page", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A"), team("fld_p", "Mine", { kind: "personal" })]);
      treePlace.set({ folder: "fld_p", chain: [] });
      renderShell("/starred");
      const nav = await openNav();
      expect(nav.querySelector('[role="tree"]')).not.toBeNull();
      expect(nav.querySelector('[aria-current="location"]')).toBeNull();
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

    // With the hidden spaces shown off, a hide still reads the hidden list again. The drive lists name a
    // location from that list, and no list mounts again to read it, so without this read a hidden space's items lose its name.
    it("hiding a space with the hidden spaces shown off reads the hidden list again", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A")]);
      renderShell("/files");
      const nav = await openNav();
      const before = mocks.loadHiddenFolders.mock.calls.length;
      fireEvent.click(await within(nav).findByRole("button", { name: "Options for Team A" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Hide" }));
      await waitFor(() => expect(mocks.setHidden).toHaveBeenCalledWith("fld_a", true));
      await waitFor(() => expect(mocks.loadHiddenFolders.mock.calls.length).toBeGreaterThan(before));
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

    // pending 85-86: the re-read after a save of Space settings swallowed its failure.
    it("shows a failed re-read of the spaces after Space settings saves", async () => {
      signIn("member");
      folders.set([team("fld_a", "Team A", { role: "manager" })]);
      renderShell("/files");
      const nav = await openNav();
      fireEvent.click(within(nav).getByRole("button", { name: "Options for Team A" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Space settings" }));
      mocks.loadFolders.mockRejectedValueOnce(new ApiError(500, "internal", { detail: "server text" }, 0));
      fireEvent.click(await screen.findByRole("button", { name: "settings saved" }));
      const alert = await within(await openNav()).findByRole("alert");
      expect(alert.textContent).not.toContain("server text");
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
    renderShell("/files");
    const drawer = await openNav();
    const entry = await waitFor(() => within(drawer).getByRole("menuitem", { name: /Requests/ }));
    expect(entry.textContent).toContain("2 waiting");
  });

  // Home read the count each time it was opened, and My files is the landing page now (decision 53): the count is read again
  // when the person comes to My files, so a request that came in while signed in shows there.
  it("reads the count again when the person comes to My files, where Home read it before", async () => {
    signIn("member");
    mocks.listAccessRequests.mockResolvedValue({ items: [] });
    const router = createMemoryRouter([{ path: "*", element: <Shell><p>page body</p></Shell> }], { initialEntries: ["/trash"] });
    render(
      <QueryClientProvider client={createQueryClient()}>
        <ThemeProvider>
          <Direction>
            <RouterProvider router={router} />
          </Direction>
        </ThemeProvider>
      </QueryClientProvider>,
    );
    await waitFor(() => expect(mocks.listAccessRequests).toHaveBeenCalledTimes(1));
    mocks.listAccessRequests.mockResolvedValue({ items: [{ id: "arq_a" }] });
    await act(async () => {
      await router.navigate("/files");
    });
    await waitFor(() => expect(mocks.listAccessRequests).toHaveBeenCalledTimes(2));
    const drawer = await openNav();
    const entry = await waitFor(() => within(drawer).getByRole("menuitem", { name: /Requests/ }));
    expect(entry.textContent).toContain("1 waiting");
  });

  it("is not shown to a member with nothing waiting", async () => {
    signIn("member");
    mocks.listAccessRequests.mockResolvedValue({ items: [] });
    renderShell("/files");
    const drawer = await openNav();
    await waitFor(() => within(drawer).getByRole("menuitem", { name: /Shared with me/ }));
    expect(within(drawer).queryByRole("menuitem", { name: /Requests/ })).toBeNull();
  });

  // The size is the stylesheet's (index.css, icon-btn): 44 px on a finger, 40 with a mouse. jsdom has no stylesheet, so the
  // check is that the account button is an icon button, which the stylesheet sizes by the pointer.
  it("the account button is an icon button, which the stylesheet sizes for a finger", async () => {
    signIn("member");
    renderShell("/files");
    expect((await screen.findByTestId("account-menu")).className).toMatch(/\bicon-btn\b/);
  });

  // Signed-in devices (decision 125) is in the account menu of every signed-in person except an installation admin, who
  // holds no device sign-in. A member and an owner see it after Change password; an admin sees the menu without it.
  it("the account menu offers Signed-in devices to a member and an owner, and not to an installation admin", async () => {
    for (const role of ["member", "owner"] as const) {
      signIn(role);
      const { unmount } = renderShell("/files");
      fireEvent.click(await screen.findByTestId("account-menu"));
      const menu = await screen.findByRole("menu", { name: `Signed in as olivia@example.com` });
      const items = within(menu).getAllByRole("menuitem").map((m) => m.textContent);
      expect(items, `${role} menu`).toContain("Signed-in devices");
      expect(items.indexOf("Signed-in devices")).toBe(items.indexOf("Change password") + 1);
      unmount();
    }
    signIn("admin");
    const { unmount } = renderShell("/files");
    fireEvent.click(await screen.findByTestId("account-menu"));
    const menu = await screen.findByRole("menu", { name: `Signed in as olivia@example.com` });
    const items = within(menu).getAllByRole("menuitem").map((m) => m.textContent);
    expect(items).toContain("Change password");
    expect(items, "admin menu").not.toContain("Signed-in devices");
    unmount();
  });

  // Decision 95: "Signed in as" is a header on the menu, not a disabled item. It is not a menu item (no menuitem, no aria-disabled),
  // the menu is named by it so a screen reader hears it once, and the first item a keyboard reaches is Change password.
  it("the account menu's Signed in as line is a header, not a disabled menu item, and Change password comes first", async () => {
    signIn("member");
    renderShell("/files");
    fireEvent.click(await screen.findByTestId("account-menu"));
    const menu = await screen.findByRole("menu", { name: "Signed in as olivia@example.com" });
    const items = within(menu).getAllByRole("menuitem");
    expect(items[0]?.textContent).toBe("Change password");
    expect(items.some((m) => m.textContent?.includes("olivia@example.com"))).toBe(false);
    expect(items.some((m) => m.getAttribute("aria-disabled") === "true")).toBe(false);
    expect(menu.querySelector("[aria-disabled='true']")).toBeNull();
  });

  // User-event keys, not a script. This checks that Enter on the account button opens the menu, and that Tab moves
  // focus into the menu and the menu stays open. It does not check the arrow keys or Escape, and jsdom does not reproduce the
  // round-1 fault (it passes on both trees). The browser run checks the rest: e2e/accountKeys.mjs (arrow, Escape, focus back).
  it("keyboard: Enter on the account button opens the menu, and Tab moves focus into it and keeps it open", async () => {
    signIn("member");
    renderShell("/files");
    const trigger = await screen.findByTestId("account-menu");
    trigger.focus();
    const user = userEvent.setup();
    await user.keyboard("{Enter}");
    const popup = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".ant-dropdown:not(.ant-dropdown-hidden) ul[role='menu']");
      if (!el) throw new Error("the account menu is not open yet");
      return el;
    });
    await user.tab();
    expect(document.querySelector(".ant-dropdown:not(.ant-dropdown-hidden) ul[role='menu']")).not.toBeNull();
    expect(popup.contains(document.activeElement)).toBe(true);
  });

  // The header's drawn colours, read from the DOM in dark, are 4.5:1 or more on the menu's ground (the palette's own
  // colorBgElevated, which the popup is drawn on). A mutant ink (for example the description colour) fails this.
  it("in dark, the Signed in as label and email are drawn at 4.5:1 or more on the menu's ground", async () => {
    window.localStorage.setItem("selfcloud.theme", "dark");
    try {
      signIn("member");
      renderShell("/files");
      fireEvent.click(await screen.findByTestId("account-menu"));
      const email = await waitFor(() => {
        const el = document.querySelector<HTMLElement>(".account-menu-who");
        if (!el) throw new Error("the header is not shown yet");
        return el;
      });
      const ground = antdTokens("dark", PALETTE.dark, SIDEBAR).token?.colorBgElevated ?? "";
      const label = email.previousElementSibling as HTMLElement;
      expect(contrast(hexOfRgb(label.style.color), String(ground))).toBeGreaterThanOrEqual(4.5);
      expect(contrast(hexOfRgb(email.style.color), String(ground))).toBeGreaterThanOrEqual(4.5);
    } finally {
      window.localStorage.removeItem("selfcloud.theme");
    }
  });

  it("the Signed in as header shows the email in a bdi, and cuts a long one with a title", async () => {
    signIn("member");
    renderShell("/files");
    fireEvent.click(await screen.findByTestId("account-menu"));
    const email = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".account-menu-who bdi");
      if (!el) throw new Error("the header is not shown yet");
      return el;
    });
    expect(email.textContent).toBe("olivia@example.com");
    // The header is in browse mode, not hidden from it (no aria-hidden on or above the email).
    expect(email.closest("[aria-hidden='true']")).toBeNull();
    expect(email.parentElement?.style.textOverflow).toBe("ellipsis");
    // The cut is at the end of the address in Hebrew too. The box that is cut is left to right, so its overflow runs to
    // the end side of the address (on a right-to-left page the box would otherwise cut the start).
    expect(email.parentElement?.getAttribute("dir")).toBe("ltr");
    expect(email.parentElement?.title).toBe("olivia@example.com");
    // The box is as wide as its address (up to the header's width), so a short address sits at the header's start edge.
    expect(email.parentElement?.style.width).toBe("fit-content");
  });
});

// wideScreen draws the sidebar beside the page (the lg breakpoint), as a desktop does, for the test's own run.
function wideScreen<T>(run: () => Promise<T>): Promise<T> {
  const original = window.matchMedia;
  window.matchMedia = (query: string) => ({ ...original(query), matches: query.includes("min-width") }) as MediaQueryList;
  return run().finally(() => {
    window.matchMedia = original;
  });
}

describe("the sidebar's New (decision 66)", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
    folders.set([]);
    hiddenFolders.set([]);
    newPlace.set(null);
    myStorage.set(null);
    mocks.mkdir.mockClear();
  });

  const readOnlyFolder: NewPlace = { where: { folder: "fld_tttttttt", dirID: null, path: "/" }, canFolder: false, canUpload: false, canUploadFolder: false, readOnly: true, canEdit: false };

  // Item 7 (fu3b, decision 77): a dialog belongs to the address it was opened at. The sidebar's New folder dialog closes when Back
  // (or any change of address) moves the page, as the files page's own dialogs already do.
  it("a New folder dialog the sidebar opened closes when Back changes the address (fu3b)", async () => {
    await wideScreen(async () => {
      signIn("member");
      folders.set([team("fld_mine", "Mine", { kind: "personal", role: "manager" })]);
      const router = createMemoryRouter([{ path: "*", element: <Shell><p>page body</p></Shell> }], { initialEntries: ["/trash", "/starred"], initialIndex: 1 });
      render(
        <QueryClientProvider client={createQueryClient()}>
          <ThemeProvider>
            <Direction>
              <RouterProvider router={router} />
            </Direction>
          </ThemeProvider>
        </QueryClientProvider>,
      );
      fireEvent.click(await screen.findByRole("button", { name: "New" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "New folder" }));
      await waitFor(() => expect(document.getElementById("newfolder-name")).not.toBeNull());
      await act(async () => {
        await router.navigate(-1);
      });
      await waitFor(() => expect(router.state.location.pathname).toBe("/trash"));
      await waitFor(() => expect(document.getElementById("newfolder-name")).toBeNull());
    });
  });

  // Round 3: the sidebar's New takes its place through placeForRoute, as the documents do. A place stored for another folder
  // (FilesPage writes it after render) is not the place on screen, so the button waits, disabled, and no upload or New folder
  // can target it.
  const placeIn = (folder: string, dir: string): NewPlace => ({
    where: { folder, dirID: dir, path: "/d" },
    canFolder: true,
    canUpload: true,
    canUploadFolder: true,
    readOnly: false,
    canEdit: true,
    nodeID: dir,
  });

  function renderOnFiles(path: string) {
    const router = createMemoryRouter([{ path: "*", element: <Shell><p>page body</p></Shell> }], { initialEntries: [path] });
    render(
      <QueryClientProvider client={createQueryClient()}>
        <ThemeProvider>
          <Direction>
            <RouterProvider router={router} />
          </Direction>
        </ThemeProvider>
      </QueryClientProvider>,
    );
  }

  it("a New place stored for folder A does not feed the sidebar's New while folder B's directory is shown", async () => {
    await wideScreen(async () => {
      signIn("member");
      folders.set([
        team("fld_mine", "Mine", { kind: "personal", role: "manager" }),
        team("fld_aaaaaaaa", "A", { role: "manager" }),
        team("fld_bbbbbbbb", "B", { role: "manager" }),
      ]);
      newPlace.set(placeIn("fld_aaaaaaaa", "nod_aaaaaaaa"));
      renderOnFiles("/files/fld_bbbbbbbb/nod_bbbbbbbb");
      const button = await screen.findByRole("button", { name: "New" });
      expect((button as HTMLButtonElement).disabled).toBe(true);
    });
  });

  it("the sidebar's New is enabled for the place the page shows", async () => {
    await wideScreen(async () => {
      signIn("member");
      folders.set([
        team("fld_mine", "Mine", { kind: "personal", role: "manager" }),
        team("fld_bbbbbbbb", "B", { role: "manager" }),
      ]);
      newPlace.set(placeIn("fld_bbbbbbbb", "nod_bbbbbbbb"));
      renderOnFiles("/files/fld_bbbbbbbb/nod_bbbbbbbb");
      const button = await screen.findByRole("button", { name: "New" });
      await waitFor(() => expect((button as HTMLButtonElement).disabled).toBe(false));
    });
  });

  it("off the files pages, New folder makes the folder in My files, from the dialog the sidebar holds", async () => {
    await wideScreen(async () => {
      signIn("member");
      folders.set([team("fld_mine", "Mine", { kind: "personal", role: "manager" })]);
      renderShell("/starred");
      fireEvent.click(await screen.findByRole("button", { name: "New" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "New folder" }));
      const field = await waitFor(() => {
        const el = document.getElementById("newfolder-name");
        if (!el) throw new Error("the folder name is not shown yet");
        return el;
      });
      fireEvent.change(field, { target: { value: "Plans" } });
      fireEvent.click(screen.getByRole("button", { name: "Create folder" }));
      await waitFor(() => expect(mocks.mkdir).toHaveBeenCalledWith("fld_mine", "/Plans"));
    });
  });

  it("on a files page, New acts on the folder shown, and a folder the person cannot add to is disabled with the reason", async () => {
    await wideScreen(async () => {
      signIn("member");
      folders.set([team("fld_mine", "Mine", { kind: "personal", role: "manager" })]);
      newPlace.set(readOnlyFolder);
      renderShell("/files/fld_tttttttt");
      fireEvent.click(await screen.findByRole("button", { name: "New" }));
      expect(await screen.findByText("You cannot add to this folder.")).toBeTruthy();
      expect((await screen.findByRole("menuitem", { name: "Upload files" })).getAttribute("aria-disabled")).toBe("true");
      expect(mocks.mkdir).not.toHaveBeenCalled();
    });
  });

  it("on a files page whose folder is not read yet, New waits disabled and does not fall back to My files", async () => {
    await wideScreen(async () => {
      signIn("member");
      folders.set([team("fld_mine", "Mine", { kind: "personal", role: "manager" })]);
      renderShell("/files/fld_tttttttt");
      expect((screen.getByRole("button", { name: "New" }) as HTMLButtonElement).disabled).toBe(true);
    });
  });

  it("an installation admin has no New button", async () => {
    await wideScreen(async () => {
      signIn("admin");
      renderShell("/admin/tenants");
      await waitFor(() => expect(document.querySelector(".shell-sider")).not.toBeNull());
      expect(screen.queryByRole("button", { name: "New" })).toBeNull();
    });
  });

  it("a person with no My files has no New button", async () => {
    await wideScreen(async () => {
      signIn("member");
      renderShell("/starred");
      await waitFor(() => expect(document.querySelector(".shell-sider")).not.toBeNull());
      expect(screen.queryByRole("button", { name: "New" })).toBeNull();
    });
  });

  it("the drawer on a phone shows the same New button at its top, and New folder there makes the folder in My files", async () => {
    signIn("member");
    folders.set([team("fld_mine", "Mine", { kind: "personal", role: "manager" })]);
    renderShell("/starred");
    const nav = await openNav();
    fireEvent.click(within(nav).getByRole("button", { name: "New" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "New folder" }));
    const field = await waitFor(() => {
      const el = document.getElementById("newfolder-name");
      if (!el) throw new Error("the folder name is not shown yet");
      return el;
    });
    fireEvent.change(field, { target: { value: "Plans" } });
    fireEvent.click(screen.getByRole("button", { name: "Create folder" }));
    await waitFor(() => expect(mocks.mkdir).toHaveBeenCalledWith("fld_mine", "/Plans"));
  });

  it("the Storage entry is a row with its title, and a line with the figures, in the menu", async () => {
    signIn("member");
    folders.set([team("fld_mine", "Mine", { kind: "personal", role: "manager" })]);
    myStorage.set({ used_bytes: 610, reserved_bytes: 0, quota_bytes: 1000, available_bytes: 390 });
    renderShell("/starred");
    const nav = await openNav();
    const entry = await waitFor(() => {
      const el = within(nav).getAllByRole("menuitem").find((m) => m.textContent?.startsWith("Storage"));
      if (!el) throw new Error("no storage entry");
      return el;
    });
    expect(entry.textContent).toContain("Storage (61% full)");
    expect(entry.textContent).toContain(`${formatBytes(610, "en")} of ${formatBytes(1000, "en")} used`);
    expect(entry.querySelector('[data-testid="sidebar-meter"]')).not.toBeNull();
  });
});

describe("the sidebar's picker (decision 66)", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
    folders.set([]);
    hiddenFolders.set([]);
  });

  it("the picked files go to the place the New menu picked, and the input is cleared so the same file can be picked again", async () => {
    await wideScreen(async () => {
      signIn("member");
      folders.set([team("fld_mine", "Mine", { kind: "personal", role: "manager" })]);
      const add = vi.spyOn(manager, "add").mockImplementation((() => undefined) as never);
      try {
        renderShell("/starred");
        const input = document.querySelector<HTMLInputElement>('[data-testid="sidebar-file-input"]');
        if (!input) throw new Error("the file picker is not in the shell");
        // The input's value is recorded, so the clearing is seen whatever the browser does with a picked file list.
        const cleared: string[] = [];
        Object.defineProperty(input, "value", { configurable: true, get: () => "", set: (v: string) => cleared.push(v) });
        const file = new File(["hello"], "notes.txt", { type: "text/plain" });

        fireEvent.click(await screen.findByRole("button", { name: "New" }));
        fireEvent.click(await screen.findByRole("menuitem", { name: "Upload files" }));
        fireEvent.change(input, { target: { files: [file] } });
        expect(add).toHaveBeenCalledTimes(1);
        expect(add.mock.calls[0][0][0].target.folder).toBe("fld_mine");
        expect(cleared).toEqual([""]);

        fireEvent.click(screen.getByRole("button", { name: "New" }));
        fireEvent.click(await screen.findByRole("menuitem", { name: "Upload files" }));
        fireEvent.change(input, { target: { files: [file] } });
        expect(add).toHaveBeenCalledTimes(2);
        expect(cleared).toEqual(["", ""]);
      } finally {
        add.mockRestore();
      }
    });
  });
});

// Decision 81: the sidebar's menu items take the row metrics of sidebarRow (theme/sidebarRow.ts): the start indent that holds the
// icon column (antd's inline padding, from the Menu's inlineIndent) and the item height. The expected values are read from the
// theme's design token, not written as numbers. Checked on a mouse and a finger, in left-to-right and right-to-left.
describe("the sidebar's menu takes the row metrics (decision 81)", () => {
  beforeEach(async () => {
    window.localStorage.clear();
    folders.set([]);
    hiddenFolders.set([]);
    mocks.loadHiddenFolders.mockReset().mockResolvedValue([]);
  });

  for (const lang of ["en", "he"] as const) {
    for (const coarse of [false, true]) {
      it(`an item's start indent and height are the row metrics (${lang}, ${coarse ? "finger" : "mouse"})`, async () => {
        const original = window.matchMedia;
        window.matchMedia = (query: string) => ({ ...original(query), matches: coarse && query.includes("pointer: coarse") }) as MediaQueryList;
        try {
          await setLang(lang);
          signIn("member");
          renderShell("/starred");
          const nav = await openNav();
          const starred = await waitFor(() => {
            const el = within(nav).getAllByRole("menuitem").find((m) => (m.getAttribute("data-menu-id") ?? "").includes("starred"));
            if (!el) throw new Error("no Starred item");
            return el;
          });
          const metrics = sidebarRow(antdTheme.getDesignToken(), coarse);
          // antd's menu sets the start indent as a physical padding: the right in a right-to-left page, the left otherwise.
          const indentProp = `padding${lang === "he" ? "Right" : "Left"}`;
          expect((starred.style as unknown as Record<string, string>)[indentProp]).toBe(`${metrics.indent}px`);
          expect(getComputedStyle(starred).height).toBe(`${metrics.height}px`);
        } finally {
          window.matchMedia = original;
          await setLang("en");
        }
      });
    }
  }
});

// wf1, decision 91: the sidebar's New menu takes its document entries from documentKinds, with the place on screen, the office
// editing switch and the impersonation (Shell.tsx). The wiring is checked here at the sidebar, where a viewer and a commenter see
// the menu: FilesPage shows no New menu to them. Each case also checks a plain entry of the menu.
describe("the sidebar's document entries follow the place, the role and the impersonation (decision 91, wf1)", () => {
  const DOCUMENTS = ["Document", "Spreadsheet", "Presentation"];
  const FOLDER = "fld_bbbbbbbb";
  const place = (over: Partial<NewPlace>): NewPlace => ({
    where: { folder: FOLDER, dirID: null, path: "/" },
    canFolder: true,
    canUpload: true,
    canUploadFolder: true,
    readOnly: false,
    canEdit: true,
    nodeID: "nod_root",
    ...over,
  });
  // A viewer's and a commenter's place: nothing can be added, so it is read-only (placeOfHere).
  const readOnlyPlace = () => place({ canFolder: false, canUpload: false, canUploadFolder: false, readOnly: true, canEdit: false });

  // Opens the sidebar's New menu on the folder's page, and waits for its entries.
  // Review item 2 (wf1): the failure the files page records reaches the sidebar's disabled New as its reason.
  it("a folder that could not be read gives the sidebar's disabled New the error as its reason", async () => {
    await wideScreen(async () => {
      setUp("member", "contributor", myFilesPlace("fld_mine"));
      newPlace.set(null);
      newPlaceFailure.set("Could not read this folder.");
      try {
        renderShell(`/files/${FOLDER}`);
        const button = await screen.findByRole("button", { name: "New" });
        expect(button.hasAttribute("disabled")).toBe(true);
        const id = button.getAttribute("aria-describedby");
        expect(id && document.getElementById(id)?.textContent).toBe("Could not read this folder.");
      } finally {
        newPlaceFailure.set(null);
      }
    });
  });

  async function openSidebarNew() {
    renderShell(`/files/${FOLDER}`);
    fireEvent.click(await screen.findByRole("button", { name: "New" }));
    await screen.findByRole("menuitem", { name: "New folder" });
  }

  function setUp(role: "owner" | "member" | "admin" | "viewer" | "commenter", folderRole: FolderAccess["role"], shown: NewPlace, impersonated = false) {
    signIn(role === "viewer" || role === "commenter" ? "member" : role);
    folders.set([
      team("fld_mine", "Mine", { kind: "personal", role: "manager" }),
      team(FOLDER, "B", { role: folderRole }),
    ]);
    newPlace.set(shown);
    if (impersonated) session.set({ ...(get(session) as Session), impersonation: { endsAt: "2099-01-01T00:00:00Z" } });
  }

  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
    folders.set([]);
    hiddenFolders.set([]);
    newPlace.set(null);
    officeEdit.set(true);
  });
  afterEach(() => {
    officeEdit.set(false);
  });

  it("offers the three documents to a person who edits the folder, while office editing is on (the control)", async () => {
    await wideScreen(async () => {
      setUp("member", "editor", place({}));
      await openSidebarNew();
      // Each kind is a submenu now (nt2, decision 108): its title is the kind's name, and the blank entry sits under it.
      for (const name of DOCUMENTS) expect(screen.getByText(name)).toBeTruthy();
    });
  });

  it("hides the three documents from a viewer's place, and keeps the reason and the disabled entries", async () => {
    await wideScreen(async () => {
      setUp("viewer", "viewer", readOnlyPlace());
      await openSidebarNew();
      expect(screen.getByRole("menuitem", { name: "Upload files" }).getAttribute("aria-disabled")).toBe("true");
      for (const name of DOCUMENTS) expect(screen.queryByRole("menuitem", { name })).toBeNull();
    });
  });

  it("hides the three documents from a commenter's place, and keeps the reason and the disabled entries", async () => {
    await wideScreen(async () => {
      setUp("commenter", "commenter", readOnlyPlace());
      await openSidebarNew();
      expect(screen.getByRole("menuitem", { name: "Upload files" }).getAttribute("aria-disabled")).toBe("true");
      for (const name of DOCUMENTS) expect(screen.queryByRole("menuitem", { name })).toBeNull();
    });
  });

  it("hides the three documents when an installation admin is signed in as the person, and keeps the other entries", async () => {
    await wideScreen(async () => {
      setUp("member", "editor", place({}), true);
      await openSidebarNew();
      expect(screen.getByRole("menuitem", { name: "Upload files" }).getAttribute("aria-disabled")).not.toBe("true");
      for (const name of DOCUMENTS) expect(screen.queryByRole("menuitem", { name })).toBeNull();
    });
  });
});

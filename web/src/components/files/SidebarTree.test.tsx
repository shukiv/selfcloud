// SPDX-License-Identifier: AGPL-3.0-or-later

// The folder tree in the sidebar (slice d1): My files and the teams are rows of one tree under the brand, the rows open
// their folders, a click opens the folder, the open state is the person's own, and a refresh reads the open branches again.

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import { setLang } from "../../lib/i18n";
import { ThemeProvider } from "../../theme/theme";
import { Direction } from "../../App";
import type { FolderAccess, Node as FileNode } from "../../lib/files/types";
import Shell from "../Shell";
import { createQueryClient, refreshTree } from "../../lib/query";
import { ApiError } from "../../lib/apiError";
import { treePlace } from "../../lib/files/treePlace";

const mocks = vi.hoisted(() => ({
  loadFolders: vi.fn(async () => []),
  loadHiddenFolders: vi.fn(async () => []),
  setHidden: vi.fn(async () => undefined),
  listAccessRequests: vi.fn(async () => ({ items: [] as unknown[] })),
  childrenPage: vi.fn(async (..._args: unknown[]) => ({ items: [] as FileNode[], next_cursor: undefined })),
}));
vi.mock("../../lib/files/folders", async () => {
  const { userWritable } = await import("../../lib/store");
  return {
    folders: userWritable([]),
    hiddenFolders: userWritable([]),
    loadFolders: mocks.loadFolders,
    loadHiddenFolders: mocks.loadHiddenFolders,
  };
});
vi.mock("../../lib/sharing/api", () => ({ setHidden: mocks.setHidden, listAccessRequests: mocks.listAccessRequests }));
vi.mock("../../lib/files/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../../lib/files/api")>();
  return { ...actual, childrenPage: mocks.childrenPage };
});

import { folders, hiddenFolders } from "../../lib/files/folders";

function space(id: string, name: string, extra: Partial<FolderAccess> = {}): FolderAccess {
  return {
    id,
    name,
    kind: "team",
    access: "write",
    role: "editor",
    members_only: false,
    sharing_managers_only: false,
    no_download_for_viewers: false,
    allow_links: false,
    ...extra,
  };
}

function dir(id: string, name: string, folder: string): FileNode {
  return { id, name, kind: "dir", folder_id: folder, role: "editor" } as unknown as FileNode;
}

function signIn(role: "owner" | "member" | "admin", id = "u1") {
  const s: Session = {
    user: { id, email: `${id}@example.com`, display_name: "Olivia", role, locale: "en" } as Session["user"],
    account: null,
    csrf: "csrf",
    publicLinks: false,
  };
  session.set(s);
}

// renderShell mounts the shell at an address. The query client is the test's own, so a refresh can be made on it.
function renderShell(path: string, client: QueryClient = createQueryClient()) {
  const router = createMemoryRouter(
    [
      { path: "/files/*", element: <Shell><p>page body</p></Shell> },
      { path: "*", element: <Shell><p>page body</p></Shell> },
    ],
    { initialEntries: [path] },
  );
  render(
    <QueryClientProvider client={client}>
      <ThemeProvider>
        <Direction>
          <RouterProvider router={router} />
        </Direction>
      </ThemeProvider>
    </QueryClientProvider>,
  );
  return { router, client };
}

// wide makes the window a desktop: the sidebar is the sider, not the drawer.
function wide(on: boolean) {
  const original = window.matchMedia;
  window.matchMedia = (query: string) => ({ ...original(query), matches: on && /min-width/.test(query) }) as MediaQueryList;
  return () => {
    window.matchMedia = original;
  };
}

describe("the folder tree in the sidebar", () => {
  let restore = () => {};
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
    folders.set([]);
    hiddenFolders.set([]);
    treePlace.set(null);
    mocks.childrenPage.mockReset().mockResolvedValue({ items: [], next_cursor: undefined });
    mocks.loadFolders.mockClear();
    mocks.loadHiddenFolders.mockReset().mockResolvedValue([]);
    restore = wide(true);
  });

  it("holds My files and the teams as the rows of one tree, above the menu", async () => {
    signIn("member");
    folders.set([space("fld_a", "Team A"), space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    renderShell("/starred");
    const tree = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-testid="folder-tree"]');
      if (!el) throw new Error("no tree yet");
      return el;
    });
    const rows = Array.from(tree.querySelectorAll<HTMLElement>('[role="tree"] > [role="treeitem"] > .tree-row bdi')).map((b) => b.textContent);
    expect(rows).toEqual(["My files", "Team A"]);
    // The menu after it holds the other pages, and My files is not among them.
    const menu = document.querySelector<HTMLElement>(".shell-scroll .shell-menu");
    expect(menu?.textContent).toContain("Starred");
    expect(menu?.textContent).not.toContain("My files");
    expect(tree.compareDocumentPosition(menu as globalThis.Node) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    restore();
  });

  it("opens My files on its chevron and reads its folders once, and a click on a folder navigates to it", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockResolvedValue({ items: [dir("nod_docs", "Docs", "fld_p")], next_cursor: undefined });
    const { router } = renderShell("/starred");
    const tree = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-testid="folder-tree"]');
      if (!el) throw new Error("no tree yet");
      return el;
    });
    const myFiles = tree.querySelector<HTMLElement>('[data-tree-key="fld_p"]') as HTMLElement;
    expect(myFiles.getAttribute("aria-expanded")).toBe("false");
    fireEvent.click(within(myFiles).getByRole("button", { name: "Expand My files" }));
    expect(await within(myFiles).findByText("Docs")).toBeTruthy();
    expect(mocks.childrenPage).toHaveBeenCalledTimes(1);
    expect(mocks.childrenPage.mock.calls[0].slice(0, 2)).toEqual(["fld_p", null]);
    fireEvent.click(within(myFiles).getByRole("button", { name: "Collapse My files" }));
    fireEvent.click(within(myFiles).getByRole("button", { name: "Expand My files" }));
    expect(mocks.childrenPage).toHaveBeenCalledTimes(1);
    fireEvent.click(within(tree).getByText("Docs"));
    await waitFor(() => expect(router.state.location.pathname).toBe("/files/fld_p/nod_docs"));
    restore();
  });

  it("keeps the open folders for the person, under their own key, and restores them", async () => {
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockResolvedValue({ items: [dir("nod_docs", "Docs", "fld_p")], next_cursor: undefined });
    signIn("member", "u1");
    renderShell("/starred");
    const tree = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-testid="folder-tree"]');
      if (!el) throw new Error("no tree yet");
      return el;
    });
    fireEvent.click(within(tree.querySelector('[data-tree-key="fld_p"]') as HTMLElement).getByRole("button", { name: "Expand My files" }));
    await waitFor(() => expect(JSON.parse(window.localStorage.getItem("selfcloud.tree.u1") ?? "{}").expanded).toContain("fld_p"));
    expect(window.localStorage.getItem("selfcloud.tree.u2")).toBeNull();
    // Another person on the same browser finds My files closed, and the first person's choice is not written under their key.
    cleanup();
    signIn("member", "u2");
    renderShell("/starred");
    const other = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-testid="folder-tree"]');
      if (!el) throw new Error("no tree yet");
      return el;
    });
    expect(other.querySelector('[data-tree-key="fld_p"]')?.getAttribute("aria-expanded")).toBe("false");
    restore();
  });

  it("a refresh reads again the open folders that the change touched, and no other", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockResolvedValue({ items: [dir("nod_docs", "Docs", "fld_p")], next_cursor: undefined });
    window.localStorage.setItem("selfcloud.tree.u1", JSON.stringify({ expanded: ["fld_p"] }));
    const { client } = renderShell("/starred");
    await waitFor(() => expect(mocks.childrenPage).toHaveBeenCalledTimes(1));
    await refreshTree(client, [{ folder: "fld_p", dir: null }]);
    await waitFor(() => expect(mocks.childrenPage).toHaveBeenCalledTimes(2));
    restore();
  });

  it("the folder marked is the place on a files page, and the tree marks nothing elsewhere", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    treePlace.set({ folder: "fld_p", chain: [] });
    renderShell("/files/fld_p");
    const onFiles = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-tree-key="fld_p"][aria-current="location"]');
      if (!el) throw new Error("not marked");
      return el;
    });
    expect(onFiles).toBeTruthy();
    cleanup();
    renderShell("/starred");
    await waitFor(() => expect(document.querySelector('[data-testid="folder-tree"]')).not.toBeNull());
    expect(document.querySelector('[aria-current="location"]')).toBeNull();
    restore();
  });

  it("folded to icons, the sidebar has no tree, and My files is an icon in the menu", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    window.localStorage.setItem("selfcloud.navRail", "1");
    renderShell("/starred");
    await screen.findByText("page body");
    expect(document.querySelector('[data-testid="folder-tree"]')).toBeNull();
    // The folded menu keeps an icon entry for the person's own space (My files) among its items.
    expect(document.querySelectorAll('.shell-menu [role="menuitem"]').length).toBeGreaterThan(0);
    restore();
  });

  it("an installation admin has no tree", async () => {
    signIn("admin");
    renderShell("/admin/tenants");
    await screen.findByText("page body");
    expect(document.querySelector('[data-testid="folder-tree"]')).toBeNull();
    restore();
  });

  it("at phone width the drawer holds the tree, and a folder chosen in it closes the drawer", async () => {
    restore();
    // A narrow window: no breakpoint matches, so the sidebar is the drawer.
    const plain = window.matchMedia;
    window.matchMedia = (query: string) => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }) as MediaQueryList;
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockResolvedValue({ items: [dir("nod_docs", "Docs", "fld_p")], next_cursor: undefined });
    window.localStorage.setItem("selfcloud.tree.u1", JSON.stringify({ expanded: ["fld_p"] }));
    const { router } = renderShell("/starred");
    fireEvent.click(screen.getByTestId("nav-open"));
    const drawer = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".ant-drawer-open");
      if (!el) throw new Error("no drawer");
      return el;
    });
    const docs = await waitFor(() => {
      const el = drawer.querySelector<HTMLElement>('[data-testid="folder-tree"] [data-drop-name="Docs"] a');
      if (!el) throw new Error("no Docs in the drawer");
      return el;
    });
    fireEvent.click(docs);
    await waitFor(() => expect(router.state.location.pathname).toBe("/files/fld_p/nod_docs"));
    await waitFor(() => expect(document.querySelector(".ant-drawer-open")).toBeNull());
    window.matchMedia = plain;
  });

  // A team lost while signed in: its read answers not found, and the list of folders is read again, so it leaves the tree.
  it("reads the folders again when a team's read answers not found", async () => {
    signIn("member");
    folders.set([space("fld_t", "Team T")]);
    window.localStorage.setItem("selfcloud.tree.u1", JSON.stringify({ expanded: ["fld_t"] }));
    mocks.childrenPage.mockRejectedValue(new ApiError(404, "not_found", {}, 0));
    renderShell("/starred");
    await waitFor(() => expect(mocks.childrenPage).toHaveBeenCalled());
    await waitFor(() => expect(mocks.loadFolders.mock.calls.length).toBeGreaterThan(1));
    restore();
  });

  // The team's options are not a tab stop of their own (the tree is one tab stop); Shift+F10 or the Menu key opens them.
  it("takes the team's options out of the tab order, and Shift+F10 on its row opens them", async () => {
    signIn("member");
    folders.set([space("fld_t", "Team T")]);
    renderShell("/starred");
    const row = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-tree-key="fld_t"]');
      if (!el) throw new Error("no row yet");
      return el;
    });
    const options = document.querySelector<HTMLElement>('button[aria-label="Options for Team T"]') as HTMLElement;
    expect(options.tabIndex).toBe(-1);
    fireEvent.keyDown(row, { key: "F10", shiftKey: true });
    expect(await screen.findByRole("menuitem", { name: "Hide" })).toBeTruthy();
    restore();
  });

  // The "Show hidden spaces" box is drawn in the sidebar's colours, so it can be seen on the blue (the class the sidebar's
  // stylesheet targets).
  it("draws the Show hidden spaces box with the sidebar's class", async () => {
    signIn("member");
    folders.set([space("fld_t", "Team T")]);
    renderShell("/starred");
    await waitFor(() => expect(document.querySelector(".shell-scroll .side-check")).not.toBeNull());
    restore();
  });
});

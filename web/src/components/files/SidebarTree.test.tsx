// SPDX-License-Identifier: AGPL-3.0-or-later

// The folder tree in the sidebar (slice d1): My files and the teams are rows of one tree under the brand, the rows open
// their folders, a click opens the folder, the open state is the person's own, and a refresh reads the open branches again.

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider, type QueryClient } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import { resetUserStores } from "../../lib/store";
import { setLang } from "../../lib/i18n";
import { ThemeProvider } from "../../theme/theme";
import { Direction } from "../../App";
import type { FolderAccess, Node as FileNode } from "../../lib/files/types";
import Shell from "../Shell";
import { createQueryClient, refreshTree } from "../../lib/query";
import { ApiError } from "../../lib/apiError";
import { treePlace } from "../../lib/files/treePlace";
import { brand, brandLoaded } from "../../lib/brand";
import { DEFAULT_ACCENT } from "../../theme/tokens";

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

  it("opens My files on its chevron and reads its folders on opening, and a click on a folder navigates to it", async () => {
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
    // Opened again inside the window, the folder is read again (fu8, decision 94): a close and an open are two visits.
    await waitFor(() => expect(mocks.childrenPage).toHaveBeenCalledTimes(2));
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
    await waitFor(() => expect(JSON.parse(window.localStorage.getItem("selfcloud.tree.v2.u1") ?? "{}").expanded).toContain("fld_p"));
    expect(window.localStorage.getItem("selfcloud.tree.v2.u2")).toBeNull();
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
    window.localStorage.setItem("selfcloud.tree.v2.u1", JSON.stringify({ expanded: ["fld_p"] }));
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
    window.localStorage.setItem("selfcloud.tree.v2.u1", JSON.stringify({ expanded: ["fld_p"] }));
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
    window.localStorage.setItem("selfcloud.tree.v2.u1", JSON.stringify({ expanded: ["fld_t"] }));
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

  // Decision 88 (changes decision 69): a deep place opens the folders on the way to it, and the folder shown is marked on its own row.
  it("a deep place opens the way to it, saves it, and marks the folder shown", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockResolvedValue({ items: [dir("nod_docs", "Docs", "fld_p")], next_cursor: undefined });
    treePlace.set({ folder: "fld_p", chain: ["nod_docs"] });
    renderShell("/files/fld_p/nod_docs");
    const tree = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-testid="folder-tree"]');
      if (!el) throw new Error("no tree yet");
      return el;
    });
    const docs = await waitFor(() => {
      const el = tree.querySelector<HTMLElement>('[data-tree-key="nod_docs"][aria-current="location"]');
      if (!el) throw new Error("not marked");
      return el;
    });
    expect(docs.getAttribute("aria-level")).toBe("2");
    expect(tree.querySelector<HTMLElement>('[data-tree-key="fld_p"]')?.getAttribute("aria-expanded")).toBe("true");
    expect(mocks.childrenPage).toHaveBeenCalledTimes(1);
    expect(mocks.childrenPage).toHaveBeenCalledWith("fld_p", null, undefined, expect.anything());
    expect(JSON.parse(window.localStorage.getItem("selfcloud.tree.v2.u1") ?? '{"expanded":[]}').expanded).toEqual(["fld_p"]);
    restore();
  });

  // Decision 69, review fix 1: with no New drawn (an admin page), the gap New leaves stays, 10 px (6 px on a finger), so the first item
  // is not on the top bar's edge. On a files page New is drawn and there is no gap.
  it("keeps the gap New leaves on an admin page, 10 px, and 6 px on a finger; a files page has no gap", async () => {
    signIn("admin");
    renderShell("/admin/tenants");
    // Decision 109: the brand's block carries the gap, so the menu starts a gap below it.
    const gap = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
      if (!el) throw new Error("no brand yet");
      return el;
    });
    expect(gap.style.marginBlockEnd).toBe("10px");
    expect(document.querySelector('.shell-sider [data-testid="new-button"]')).toBeNull();
    cleanup();
    const plain = window.matchMedia;
    window.matchMedia = (query: string) => ({ ...plain(query), matches: /pointer: coarse/.test(query) || (/min-width/.test(query) && plain(query).matches) }) as MediaQueryList;
    try {
      signIn("admin");
      renderShell("/admin/tenants");
      const finger = await waitFor(() => {
        const el = document.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
        if (!el) throw new Error("no brand yet");
        return el;
      });
      expect(finger.style.marginBlockEnd).toBe("6px");
    } finally {
      window.matchMedia = plain;
    }
    cleanup();
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    treePlace.set({ folder: "fld_p", chain: [] });
    renderShell("/files/fld_p");
    await waitFor(() => expect(document.querySelector('[data-testid="new-button"]')).not.toBeNull());
    // Decision 109: on a files page New follows the brand block, one edge (10 px) below it; the block keeps its own top edge.
    const brandBlock = document.querySelector<HTMLElement>('[data-testid="sidebar-brand"]');
    expect(brandBlock?.style.paddingBlockStart).toBe("10px");
    expect(brandBlock?.style.marginBlockEnd).toBe("10px");
    restore();
  });

  // Decision 88: a place changed after mount opens the way to it. The folder below My files is read and opened, and the place's
  // folder (not in the tree here) is not opened: the nearest row on screen is marked until the way has been read.
  it("a place changed after mount opens the way to it, and marks the nearest row on screen", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockImplementation(async (...args: unknown[]) => ({
      items: args[1] === null ? [dir("nod_docs", "Docs", "fld_p")] : [],
      next_cursor: undefined,
    }));
    const { router } = renderShell("/starred");
    const tree = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-testid="folder-tree"]');
      if (!el) throw new Error("no tree yet");
      return el;
    });
    expect(tree.querySelector('[aria-current="location"]')).toBeNull();
    treePlace.set({ folder: "fld_p", chain: ["nod_docs", "nod_deep"] });
    await router.navigate("/files/fld_p/nod_deep");
    await waitFor(() => expect(tree.querySelector('[data-tree-key="nod_docs"]')?.getAttribute("aria-expanded")).toBe("true"));
    const docs = await waitFor(() => {
      const el = tree.querySelector<HTMLElement>('[data-tree-key="nod_docs"][aria-current="location"]');
      if (!el) throw new Error("not marked");
      return el;
    });
    expect(docs).toBeTruthy();
    expect(mocks.childrenPage).toHaveBeenCalledWith("fld_p", null, undefined, expect.anything());
    expect(mocks.childrenPage).toHaveBeenCalledWith("fld_p", "nod_docs", undefined, expect.anything());
    expect(JSON.parse(window.localStorage.getItem("selfcloud.tree.v2.u1") ?? '{"expanded":[]}').expanded).toEqual(["fld_p", "nod_docs"]);
    restore();
  });

  // Decision 69, review fix 6: the phone drawer's dialog has a name (the sidebar's region name), read from its title.
  it("names the phone drawer's dialog with the sidebar's name", async () => {
    const plain = window.matchMedia;
    window.matchMedia = (query: string) => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }) as MediaQueryList;
    try {
      signIn("member");
      folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
      renderShell("/starred");
      fireEvent.click(await screen.findByTestId("nav-open"));
      const dialog = await screen.findByRole("dialog", { name: "Sidebar" });
      expect(dialog.getAttribute("aria-labelledby")).toBeTruthy();
    } finally {
      window.matchMedia = plain;
    }
  });

  // Leaving the files pages takes the mark off the tree: the tree marks the place only on a files page (decision 69).
  it("takes the mark off the tree when the person leaves the files pages", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    treePlace.set({ folder: "fld_p", chain: [] });
    const { router } = renderShell("/files/fld_p");
    await waitFor(() => expect(document.querySelector('.shell-sider [data-tree-key="fld_p"][aria-current="location"]')).not.toBeNull());
    await router.navigate("/starred");
    // The Starred view shows a folder's listing, so the page writes the place again after the change (as FilesPage does).
    treePlace.set({ folder: "fld_p", chain: [] });
    await waitFor(() => expect(document.querySelector('.shell-sider [aria-current="location"]')).toBeNull());
    restore();
  });

  // The open folders saved before this slice are not read: the key is new, so everyone starts folded once.
  it("does not read the open folders saved under the key of before this slice", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    window.localStorage.setItem("selfcloud.tree.u1", JSON.stringify({ expanded: ["fld_p"] }));
    renderShell("/starred");
    const tree = await waitFor(() => {
      const el = document.querySelector<HTMLElement>('[data-testid="folder-tree"]');
      if (!el) throw new Error("no tree yet");
      return el;
    });
    expect(tree.querySelector('[data-tree-key="fld_p"]')?.getAttribute("aria-expanded")).toBe("false");
    expect(mocks.childrenPage).not.toHaveBeenCalled();
    restore();
  });

  // Decision 69: the shell draws no name and no logo, on the desktop, in the rail and in the phone's drawer. The name stays in the
  // browser tab and the sign-in pages (not tested here). The sidebar is a named region, and the top bar a banner.
  // Decision 109 reverses 69's no-logo rule for the sidebar: the logo or the mark is at its top, in the rail and in the drawer.
  it("draws the logo at the top of the sidebar, the rail and the phone drawer, and no brand in the top bar", async () => {
    brandLoaded.set(true);
    brand.set({ name: "Acme Drive", accent: DEFAULT_ACCENT, logo_url: "/brand/logo.png" });
    try {
      signIn("member");
      folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
      renderShell("/starred");
      const sider = await waitFor(() => {
        const el = document.querySelector<HTMLElement>(".shell-sider");
        if (!el) throw new Error("no sidebar yet");
        return el;
      });
      // Decision 109: the logo is at the top of the sidebar, and the name is not drawn beside it; the top bar draws neither.
      expect(sider.textContent).not.toContain("Acme Drive");
      expect(sider.querySelector("img")?.getAttribute("src")).toBe("/brand/logo.png");
      expect(document.querySelector("header")?.textContent).not.toContain("Acme Drive");
      expect(document.querySelector("header img")).toBeNull();
      expect(screen.getByRole("complementary", { name: "Sidebar" })).toBeTruthy();
      expect(screen.getByRole("banner")).toBeTruthy();
      cleanup();
      restore();

      // Folded to the rail: the same logo, small; the name is not drawn.
      window.localStorage.setItem("selfcloud.navRail", "1");
      const rail = wide(true);
      renderShell("/starred");
      await waitFor(() => expect(document.querySelector(".ant-layout-sider-collapsed")).not.toBeNull());
      expect(document.querySelector(".ant-layout-sider-collapsed")?.textContent).not.toContain("Acme Drive");
      expect(document.querySelector(".ant-layout-sider-collapsed img")?.getAttribute("src")).toBe("/brand/logo.png");
      cleanup();
      rail();
      window.localStorage.removeItem("selfcloud.navRail");

      // The phone: the drawer holds the sidebar, and it draws no name either.
      const plain = window.matchMedia;
      window.matchMedia = (query: string) => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }) as MediaQueryList;
      renderShell("/starred");
      fireEvent.click(await screen.findByTestId("nav-open"));
      const drawer = await waitFor(() => {
        const el = document.querySelector<HTMLElement>(".ant-drawer-open");
        if (!el) throw new Error("no drawer");
        return el;
      });
      expect(drawer.textContent).not.toContain("Acme Drive");
      expect(drawer.querySelector("img")?.getAttribute("src")).toBe("/brand/logo.png");
      expect(document.querySelector("header")?.textContent).not.toContain("Acme Drive");
      window.matchMedia = plain;
    } finally {
      brand.set({ name: "SelfCloud", accent: DEFAULT_ACCENT, logo_url: null });
      brandLoaded.set(false);
    }
  });
});

// The sidebar's scroll follow (decision 88): when the mark moves to a row below the fold of the scroll area, the area scrolls to it, and
// the page does not. jsdom has no layout, so the rects are given: the scroll area is 100 px tall, and the row that carries aria-current
// (its tree row) sits at 150 to 190 px. A scroll position is kept as a plain value, because jsdom does not scroll.
function fakeLayout() {
  const original = Element.prototype.getBoundingClientRect;
  const box = (top: number, bottom: number) => ({ top, bottom, width: 200, height: bottom - top, x: 0, y: top, toJSON() {} }) as DOMRect;
  Element.prototype.getBoundingClientRect = function (this: Element) {
    if (this.classList.contains("shell-scroll")) return box(0, 100);
    if (this.classList.contains("tree-row") && this.parentElement?.getAttribute("aria-current") === "location") return box(150, 190);
    return box(0, 0);
  };
  return () => {
    Element.prototype.getBoundingClientRect = original;
  };
}
function ownScroll(el: HTMLElement) {
  let value = 0;
  Object.defineProperty(el, "scrollTop", { configurable: true, get: () => value, set: (v: number) => { value = v; } });
  return () => value;
}
// narrowWindow is a phone-width window: no breakpoint matches, so the sidebar is the drawer.
function narrowWindow() {
  const plain = window.matchMedia;
  window.matchMedia = (query: string) => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }) as MediaQueryList;
  return () => {
    window.matchMedia = plain;
  };
}

describe("the sidebar's scroll follow (decision 88)", () => {
  let restore = () => {};
  let unlayout = () => {};
  beforeEach(async () => {
    window.localStorage.clear();
    await setLang("en");
    resetUserStores();
    folders.set([]);
    hiddenFolders.set([]);
    treePlace.set(null);
    mocks.childrenPage.mockReset().mockResolvedValue({ items: [], next_cursor: undefined });
    mocks.loadHiddenFolders.mockReset().mockResolvedValue([]);
    restore = wide(true);
  });
  afterEach(() => {
    unlayout();
    unlayout = () => {};
    restore();
    cleanup();
  });

  // The mark moves to the place's row once its folder is read: the area scrolls to it by the amount the row is below the fold, and the page
  // does not move. This test fails when the observer on aria-current is removed from the shell.
  it("scrolls the sidebar's area to the mark when it moves below the fold, and does not scroll the page", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockImplementation(async (...args: unknown[]) => ({ items: args[1] === null ? [dir("nod_docs", "Docs", "fld_p")] : [], next_cursor: undefined }));
    unlayout = fakeLayout();
    treePlace.set({ folder: "fld_p", chain: ["nod_docs"] });
    renderShell("/files/fld_p/nod_docs");
    const region = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".shell-scroll");
      if (!el) throw new Error("no sidebar area yet");
      return el;
    });
    const scrolled = ownScroll(region);
    await waitFor(() => expect(region.querySelector('[data-tree-key="nod_docs"][aria-current="location"]')).not.toBeNull());
    expect(scrolled()).toBe(90);
    expect(document.scrollingElement?.scrollTop ?? 0).toBe(0);
  });

  // A person who has scrolled the sidebar within the last second keeps that position: a mark that moves then is not followed. A mark
  // that moves a second later is.
  it("does not pull the sidebar's area while the person has scrolled it within the last second", async () => {
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockResolvedValue({ items: [dir("nod_docs", "Docs", "fld_p"), dir("nod_pics", "Pics", "fld_p")], next_cursor: undefined });
    window.localStorage.setItem("selfcloud.tree.v2.u1", JSON.stringify({ expanded: ["fld_p"] }));
    unlayout = fakeLayout();
    renderShell("/starred");
    const region = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".shell-scroll");
      if (!el) throw new Error("no sidebar area yet");
      return el;
    });
    const scrolled = ownScroll(region);
    const row = (key: string) => region.querySelector<HTMLElement>(`[data-tree-key="${key}"]`);
    await waitFor(() => expect(row("nod_docs")).not.toBeNull());
    await waitFor(() => expect(row("nod_pics")).not.toBeNull());
    // The person scrolls the area, which takes it to the top.
    fireEvent.wheel(region);
    row("nod_docs")!.setAttribute("aria-current", "location");
    // The observer runs after the change, so the check waits for it: a held follow leaves the position where the person put it.
    await new Promise((r) => setTimeout(r, 20));
    expect(scrolled()).toBe(0);
    // Later than a second, the same mark moved to a row below the fold is followed.
    row("nod_docs")!.removeAttribute("aria-current");
    const later = Date.now() + 5000;
    const now = vi.spyOn(Date, "now").mockReturnValue(later);
    row("nod_pics")!.setAttribute("aria-current", "location");
    await waitFor(() => expect(scrolled()).toBe(90));
    now.mockRestore();
  });

  // A phone: the drawer's first opening attaches the follow to the drawer's own area, so the mark's row is in view on the first opening.
  it("on a phone, the drawer's first opening keeps the mark's row in view", async () => {
    restore();
    const unphone = narrowWindow();
    signIn("member");
    folders.set([space("fld_p", "Mine", { kind: "personal", role: "manager" })]);
    mocks.childrenPage.mockImplementation(async (...args: unknown[]) => ({ items: args[1] === null ? [dir("nod_docs", "Docs", "fld_p")] : [], next_cursor: undefined }));
    unlayout = fakeLayout();
    treePlace.set({ folder: "fld_p", chain: ["nod_docs"] });
    renderShell("/files/fld_p/nod_docs");
    fireEvent.click(screen.getByTestId("nav-open"));
    const region = await waitFor(() => {
      const el = document.querySelector<HTMLElement>(".ant-drawer-open .shell-scroll");
      if (!el) throw new Error("no drawer area yet");
      return el;
    });
    const scrolled = ownScroll(region);
    await waitFor(() => expect(region.querySelector('[data-tree-key="nod_docs"][aria-current="location"]')).not.toBeNull());
    expect(scrolled()).toBe(90);
    unphone();
  });
});

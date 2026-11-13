// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { App as AntdApp } from "antd";
import { createQueryClient, refreshTree } from "../lib/query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/apiError";
import type { FolderAccess, Node, Role } from "../lib/files/types";
import { session, type Session } from "../lib/session";
import { writable } from "../lib/store";
import { folders } from "../lib/files/folders";
import { locate } from "../lib/files/location";
import { clipboard, setClip } from "../lib/files/clipboard";
import { get } from "../lib/store";
import { fileErrorText } from "../lib/files/fileErrors";
import { translate } from "../lib/i18n";
import { myStorage } from "../lib/storage/api";
import { newPlace, newPlaceFailure } from "../lib/files/newPlace";
import { treePlace } from "../lib/files/treePlace";
import { officeEdit } from "../lib/files/editor";
import { officeConvert } from "../lib/files/convert";

// The API, the place reader, the uploads and the stars are replaced, so each test states what the server answers.
const api = vi.hoisted(() => ({
  getMe: vi.fn(),
  childrenPage: vi.fn(),
  statNode: vi.fn(),
  statOrNull: vi.fn(),
  renameNode: vi.fn(),
  deleteNode: vi.fn(),
  restoreNode: vi.fn(),
  moveNode: vi.fn(),
  copyNode: vi.fn(),
  mkdir: vi.fn(),
  getJob: vi.fn(),
  readLimits: vi.fn(),
}));
// truncated gives the place of a folder more than 64 levels down (decision 140): the trail is the 64 nearest, the root is not
// known, and so there is no space name and no path.
const place = vi.hoisted(() => ({ role: "editor" as Role, noDownload: false, folderName: "Team A", rootNodeID: null as string | null, trail: [] as Node[], truncated: false }));
const uploadsMock = vi.hoisted(() => ({ add: vi.fn(), setChunkSize: vi.fn() }));
const uploadList = vi.hoisted(() => {
  return { store: null as unknown as { set: (v: unknown[]) => void } };
});

vi.mock("../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../lib/files/api")>()), ...api }));
const started = vi.hoisted(() => vi.fn());
vi.mock("../lib/files/playback", async (orig) => ({
  ...(await orig<typeof import("../lib/files/playback")>()),
  startPlayback: (...args: unknown[]) => started(...args),
}));
vi.mock("../lib/files/location", async (orig) => ({
  ...(await orig<typeof import("../lib/files/location")>()),
  locate: vi.fn(async (folder: string, dirID: string | null) => ({
    folder,
    folderName: place.truncated ? null : place.folderName,
    folderRole: place.role,
    dir: dirID ? { id: dirID, name: "Reports", kind: "dir", role: place.role } : null,
    dirRole: place.role,
    trail: place.trail,
    rootReachable: !place.truncated,
    truncated: place.truncated,
    rootNodeID: place.rootNodeID,
    reach: [],
    spaceNoDownload: place.noDownload,
    path: place.truncated ? null : dirID ? "/Reports" : "/",
  })),
}));
// swallowed waits out the window in which the page swallows a click after a drag (useGestures, swallowClick: 50 ms). A dialog's button
// pressed inside that window gets no click, so a test that presses one after a release waits for it first.
const swallowed = () => new Promise<void>((r) => setTimeout(r, 60));

// refreshTree is the real one, spied on: the tests read which folder-tree branches a change asked to read again.
vi.mock("../lib/query", async (orig) => {
  const real = await orig<typeof import("../lib/query")>();
  return { ...real, refreshTree: vi.fn(real.refreshTree) };
});
vi.mock("../lib/files/folders", async () => {
  const { userWritable } = await import("../lib/store");
  return { folders: userWritable([]), hiddenFolders: userWritable([]), loadFolders: async () => [], loadHiddenFolders: async () => [] };
});
vi.mock("../lib/files/uploads", async () => {
  const { writable } = await import("../lib/store");
  uploadList.store = writable<unknown[]>([]);
  return { uploads: uploadList.store, manager: uploadsMock, conflictAsk: writable<string | null>(null) };
});
vi.mock("../lib/stars", () => ({ starNode: vi.fn(), unstarNode: vi.fn() }));
import { starNode, unstarNode } from "../lib/stars";
// The storage figures are read by the notice on My files; the test states them in the store and does not read them.
vi.mock("../lib/storage/api", async (orig) => ({
  ...(await orig<typeof import("../lib/storage/api")>()),
  refreshMyStorage: vi.fn(async () => undefined),
}));
// The access request on a missing directory is replaced by a stub that names the item it was given.
vi.mock("../components/share/RequestAccess", async () => {
  const React = await import("react");
  return {
    default: ({ nodeID }: { nodeID: string }) => React.createElement("p", null, `request for ${nodeID}`),
  };
});

// The share dialog is replaced by a stub that shows the props it was given, so these tests state what the page passes.
const shareStub = vi.hoisted(() => ({ mounted: 0, props: [] as Array<{ node: { id: string }; space: { id: string } | null }> }));
vi.mock("../components/share/ShareDialog", async () => {
  const React = await import("react");
  return {
    default: (p: { node: { id: string }; space: { id: string } | null; onclose: () => void; onchanged: () => void }) => {
      React.useEffect(() => {
        shareStub.mounted++;
      }, []);
      shareStub.props.push({ node: p.node, space: p.space });
      return React.createElement(
        "div",
        { role: "dialog", "aria-label": "stub share" },
        React.createElement("button", { onClick: p.onclose }, "stub close"),
        React.createElement("button", { onClick: p.onchanged }, "stub changed"),
      );
    },
  };
});

import FilesPage from "./FilesPage";

export function signedAs(id: string, csrf = "t1"): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: `Name ${id}`, role: "member", locale: "en", status: "active" },
    account: null,
    csrf,
    publicLinks: false,
  };
}

export function node(id: string, name: string, extra: Partial<Node> = {}): Node {
  return {
    id,
    folder_id: "fld_a",
    parent_id: null,
    name,
    kind: "file",
    size: 10,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: place.role,
    no_download: false,
    editors_can_share: false,
    ...extra,
  };
}

// renderAt mounts the page at an address, in a router and a query client of its own.
// menuEntry finds an entry of the open menu by its name. An entry in a submenu (decision 70) is reached by a hover on the
// submenu's title, which opens it: the caller names the submenu.
async function menuEntry(name: string, submenu?: "Share" | "Organize" | "Information"): Promise<HTMLElement> {
  if (submenu) {
    // The title is found by its text: a submenu's title is a menu item of its own, and the entry is read once it is open.
    const title = await waitFor(() => {
      const el = Array.from(document.querySelectorAll<HTMLElement>(".ant-dropdown-menu-submenu-title")).find((x) => x.textContent?.trim() === submenu);
      if (!el) throw new Error(`no submenu ${submenu}`);
      return el;
    });
    fireEvent.mouseEnter(title);
  }
  return screen.findByRole("menuitem", { name });
}

// inPanel finds a button of the selection panel. The rows have the same icons at their end (decision 70), so the panel is asked
// for by its own test id.
async function inPanel(name: string): Promise<HTMLElement> {
  return within(await screen.findByTestId("selection-panel")).findByRole("button", { name });
}

// submenuOf names the submenu an entry is in, for the entries the tests pick (decision 70).
function submenuOf(name: string): "Share" | "Organize" | "Information" | undefined {
  if (["Details", "Versions"].includes(name)) return "Information";
  if (["Move to…", "Copy to…", "Add shortcut", "Cut", "Copy", "Paste"].includes(name)) return "Organize";
  if (["Share…", "Copy link"].includes(name)) return "Share";
  return undefined;
}

// chooseMore opens the toolbar's More menu and picks the action with this name from it.
async function chooseMore(name: string) {
  fireEvent.click(screen.getByRole("button", { name: "More" }));
  fireEvent.click(await menuEntry(name, submenuOf(name)));
}

// chooseNew opens the New menu and picks the item with this name from it.
async function chooseNew(name: string) {
  fireEvent.click(screen.getByRole("button", { name: "New" }));
  fireEvent.click(await screen.findByRole("menuitem", { name }));
}

function renderAt(path: string, client?: QueryClient) {
  // The app's own client: the same stale window and retry rules, so a test sees what a person sees.
  const qc = client ?? createQueryClient();
  const router = createMemoryRouter([{ path: "*", element: <FilesPage /> }], { initialEntries: [path] });
  const view = render(
    <QueryClientProvider client={qc}>
      <AntdApp>
        <RouterProvider router={router} />
      </AntdApp>
    </QueryClientProvider>,
  );
  return { qc, router, ...view };
}

export const FOLDER = "/files/fld_aaaaaaaa";
export const rows = (name: string) => document.querySelector(`[role="row"][aria-label="${name}"]`);

beforeEach(() => {
  vi.clearAllMocks();
  place.role = "editor";
  place.noDownload = false;
  place.trail = [];
  place.folderName = "Team A";
  place.rootNodeID = null;
  place.truncated = false;
  session.set(signedAs("u1"));
  api.getMe.mockResolvedValue({ personal_folder_id: "fld_me" });
  api.readLimits.mockResolvedValue({ max_chunk_size: 1048576, max_file_size: 1 });
  api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
  api.statOrNull.mockImplementation(async (id: string) => (id === "n1" ? node("n1", "alpha.txt") : id === "n2" ? node("n2", "beta.txt") : null));
  uploadList.store.set([]);
});

afterEach(() => {
  session.set(null);
  document.title = "";
});

// An office file opens in the editor, full page, in its own tab (decision 102): a double click or Enter calls the
// open helper, which opens /edit/<node> with noopener; with the editor off, the preview opens as before.
describe("an office file's open (decision 102)", () => {
  const DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document";
  beforeEach(() => {
    place.role = "editor";
    officeEdit.set(true);
    officeConvert.set(true);
    api.childrenPage.mockResolvedValue({ items: [node("n1", "letter.docx", { mime_type: DOCX })], next_cursor: undefined });
  });
  afterEach(() => {
    officeEdit.set(false);
    officeConvert.set(false);
    vi.restoreAllMocks();
  });

  it("a double click opens the editor in a new tab with noopener, in edit mode for an editor", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "letter.docx" });
    fireEvent.doubleClick(rows("letter.docx") as Element);
    await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
    expect(open).toHaveBeenCalledWith(expect.stringMatching(/^\/edit\/n1\?mode=edit/), "_blank", "noopener");
  });

  it("Enter on a selected office file opens the editor the same way", async () => {
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "letter.docx" });
    fireEvent.click(rows("letter.docx") as Element);
    fireEvent.keyDown(rows("letter.docx") as Element, { key: "Enter" });
    await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
    expect(open).toHaveBeenCalledWith(expect.stringMatching(/^\/edit\/n1\?mode=edit/), "_blank", "noopener");
  });

  it("a viewer's double click opens the editor in view mode, in a new tab", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "letter.docx", { mime_type: DOCX, role: "viewer" })], next_cursor: undefined });
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "letter.docx" });
    fireEvent.doubleClick(rows("letter.docx") as Element);
    await waitFor(() => expect(open).toHaveBeenCalledTimes(1));
    expect(open).toHaveBeenCalledWith(expect.stringMatching(/^\/edit\/n1\?mode=view/), "_blank", "noopener");
  });

  it("with the editor off, a double click opens the preview, and no tab", async () => {
    officeEdit.set(false);
    const open = vi.spyOn(window, "open").mockImplementation(() => null);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "letter.docx" });
    fireEvent.doubleClick(rows("letter.docx") as Element);
    await screen.findByTestId("preview");
    expect(open).not.toHaveBeenCalled();
  });
});

describe("the listing survives a failed read", () => {
  it("keeps the listed items and the selection when a load-more fails, and says so", async () => {
    api.childrenPage
      .mockResolvedValueOnce({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: "c2" })
      .mockRejectedValue(new ApiError(500, "internal", {}, 0));
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    // The failure is the notice (an info alert) and the listing stays, with its items.
    await waitFor(() => expect(document.querySelector(".ant-alert-info")).not.toBeNull(), { timeout: 8000 });
    expect(rows("alpha.txt")).not.toBeNull();
    expect(rows("beta.txt")).not.toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});

describe("the listing is sorted by the server (decision 47)", () => {
  // The sort a test picks is kept in local storage, and the next test starts from it; each test starts at the default.
  beforeEach(() => localStorage.clear());

  it("asks for the new sort from the first page when a column is clicked", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(api.childrenPage).toHaveBeenLastCalledWith("fld_aaaaaaaa", null, undefined, { key: "name", dir: "asc" }, expect.any(AbortSignal));
    api.childrenPage.mockClear();
    fireEvent.click(screen.getByRole("button", { name: /size/i }));
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalled());
    expect(api.childrenPage).toHaveBeenLastCalledWith("fld_aaaaaaaa", null, undefined, { key: "size", dir: "asc" }, expect.any(AbortSignal));
  });

  it("keeps the rows on screen, with the failure notice, when the first read after a sort change fails", async () => {
    api.childrenPage.mockImplementation(async (_f: string, _d: string | null, _c: string | undefined, sort?: { key: string }) => {
      if (sort?.key === "size") throw new ApiError(500, "internal", {}, 0);
      return { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined };
    });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(screen.getByRole("button", { name: /size/i }));
    await waitFor(() => expect(document.querySelector(".ant-alert-info")).not.toBeNull(), { timeout: 8000 });
    expect(rows("alpha.txt")).not.toBeNull();
    expect(rows("beta.txt")).not.toBeNull();
    expect(document.querySelector('[role="alert"]')).toBeNull();
  }, 15000);

  it("still shows the error, with a retry, when a folder's first read fails", async () => {
    api.childrenPage.mockRejectedValue(new ApiError(500, "internal", {}, 0));
    renderAt(FOLDER);
    await waitFor(() => expect(document.querySelector('[role="alert"]')).not.toBeNull(), { timeout: 8000 });
    expect(rows("alpha.txt")).toBeNull();
  }, 15000);
});

describe("the listing is read fresh on every visit", () => {
  it("reads the folder again when the page is mounted a second time, inside the stale window", async () => {
    const qc = createQueryClient();
    const first = renderAt(FOLDER, qc);
    await screen.findByRole("row", { name: "alpha.txt" });
    first.unmount();
    api.childrenPage.mockClear();
    renderAt(FOLDER, qc);
    await screen.findByRole("row", { name: "alpha.txt" });
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalled());
  });

  it("does not read again on mount just because an upload finished before the page opened", async () => {
    const qc = createQueryClient();
    const first = renderAt(FOLDER, qc);
    await screen.findByRole("row", { name: "alpha.txt" });
    first.unmount();
    api.childrenPage.mockClear();
    uploadList.store.set([{ id: 1, status: "done", name: "x", size: 1, offset: 1, speed: 0, suggested: null, error: null }]);
    renderAt(FOLDER, qc);
    await screen.findByRole("row", { name: "alpha.txt" });
    await new Promise((r) => setTimeout(r, 50));
    expect(api.childrenPage).toHaveBeenCalledTimes(1);
  });
});

describe("a folder revisited shows what it holds now (fu8)", () => {
  // The page stays mounted while it moves from one folder to another. A folder that gains an item while the page is away is
  // read again when the page comes back to it, inside the window in which the client keeps a read as fresh.
  // The clock is frozen, so the page comes back inside the window whatever the speed of the machine.
  afterEach(() => vi.useRealTimers());
  it("lists an item added to a folder while the page was away, when the page comes back to it", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const other = "/files/fld_bbbbbbbb";
    let aItems = [node("n1", "alpha.txt"), node("n2", "beta.txt")];
    api.childrenPage.mockImplementation(async (folder: string) =>
      folder === "fld_aaaaaaaa" ? { items: aItems, next_cursor: undefined } : { items: [node("n9", "elsewhere.txt", { folder_id: "fld_bbbbbbbb" })], next_cursor: undefined },
    );
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await act(async () => {
      await router.navigate(other);
    });
    await screen.findByRole("row", { name: "elsewhere.txt" });
    // The folder gains an item while the page is on the other one.
    aItems = [...aItems, node("n3", "gamma.txt")];
    await act(async () => {
      await router.navigate(FOLDER);
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  it("reads the folder again after the window has passed (31 s), and shows what it holds", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const other = "/files/fld_bbbbbbbb";
    let aItems = [node("n1", "alpha.txt")];
    api.childrenPage.mockImplementation(async (folder: string) =>
      folder === "fld_aaaaaaaa" ? { items: aItems, next_cursor: undefined } : { items: [node("n9", "elsewhere.txt", { folder_id: "fld_bbbbbbbb" })], next_cursor: undefined },
    );
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await act(async () => {
      await router.navigate(other);
    });
    await screen.findByRole("row", { name: "elsewhere.txt" });
    aItems = [...aItems, node("n3", "gamma.txt")];
    vi.setSystemTime(Date.now() + 31_000);
    api.childrenPage.mockClear();
    await act(async () => {
      await router.navigate(FOLDER);
    });
    await screen.findByRole("row", { name: "gamma.txt" });
    expect(api.childrenPage.mock.calls.filter((c) => c[0] === "fld_aaaaaaaa").length).toBeGreaterThan(0);
  });

  it("reads the folder again when it is opened from the address it is shown at", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    let aItems = [node("n1", "alpha.txt")];
    api.childrenPage.mockImplementation(async (folder: string) =>
      folder === "fld_aaaaaaaa" ? { items: aItems, next_cursor: undefined } : { items: [node("n9", "elsewhere.txt", { folder_id: "fld_bbbbbbbb" })], next_cursor: undefined },
    );
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    aItems = [...aItems, node("n3", "gamma.txt")];
    await act(async () => {
      await router.navigate(FOLDER);
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  it("does not read the folder again on a plain re-render at the same address", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const before = api.childrenPage.mock.calls.filter((c) => c[0] === "fld_aaaaaaaa").length;
    fireEvent.click(screen.getByTestId("view-toggle"));
    await new Promise((r) => setTimeout(r, 50));
    expect(api.childrenPage.mock.calls.filter((c) => c[0] === "fld_aaaaaaaa").length).toBe(before);
    // The view is kept in the page's preferences: it is put back, so later tests see the list they expect.
    fireEvent.click(screen.getByTestId("view-toggle"));
  });

  it("shows a folder's new name when it is revisited after someone else renamed it", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const other = "/files/fld_bbbbbbbb";
    api.childrenPage.mockImplementation(async (folder: string) =>
      folder === "fld_aaaaaaaa" ? { items: [node("n1", "alpha.txt")], next_cursor: undefined } : { items: [node("n9", "elsewhere.txt", { folder_id: "fld_bbbbbbbb" })], next_cursor: undefined },
    );
    const { router } = renderAt(FOLDER);
    await waitFor(() => expect(document.title).toContain("Team A"));
    await act(async () => {
      await router.navigate(other);
    });
    await screen.findByRole("row", { name: "elsewhere.txt" });
    place.folderName = "Team Renamed";
    await act(async () => {
      await router.navigate(FOLDER);
    });
    await waitFor(() => expect(document.title).toContain("Team Renamed"));
  });

  it("a revisit reads only the first page, and the rest as the person asks, as a first visit does (six pages)", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    const other = "/files/fld_bbbbbbbb";
    const pages = new Map<string | undefined, { items: Node[]; next_cursor: string | undefined }>();
    for (let i = 0; i < 6; i++) {
      pages.set(i === 0 ? undefined : `c${i}`, { items: [node(`n${i}`, `file${i}.txt`)], next_cursor: i < 5 ? `c${i + 1}` : undefined });
    }
    api.childrenPage.mockImplementation(async (folder: string, _d: string | null, cursor?: string) =>
      folder === "fld_aaaaaaaa" ? pages.get(cursor) : { items: [node("n9", "elsewhere.txt", { folder_id: "fld_bbbbbbbb" })], next_cursor: undefined },
    );
    const count = () => api.childrenPage.mock.calls.filter((c) => c[0] === "fld_aaaaaaaa").length;
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "file0.txt" });
    for (let i = 1; i < 6; i++) {
      fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
      await screen.findByRole("row", { name: `file${i}.txt` });
    }
    const first = count();
    expect(first).toBe(6);
    api.childrenPage.mockClear();
    await act(async () => {
      await router.navigate(other);
    });
    await screen.findByRole("row", { name: "elsewhere.txt" });
    await act(async () => {
      await router.navigate(FOLDER);
    });
    await screen.findByRole("row", { name: "file0.txt" });
    await new Promise((r) => setTimeout(r, 50));
    // The revisit costs one request for its first page, as a first visit does before anything is loaded.
    expect(count()).toBe(1);
    for (let i = 1; i < 6; i++) {
      fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
      await screen.findByRole("row", { name: `file${i}.txt` });
    }
    await new Promise((r) => setTimeout(r, 50));
    expect(count()).toBe(first);
  });

  it("cancels the listing read when the page leaves the folder before the read arrives", async () => {
    const other = "/files/fld_bbbbbbbb";
    const seen: Array<AbortSignal | undefined> = [];
    api.childrenPage.mockImplementation(async (folder: string, _d: string | null, _c: string | undefined, _s: unknown, signal?: AbortSignal) => {
      if (folder === "fld_aaaaaaaa") {
        seen.push(signal);
        return new Promise(() => undefined);
      }
      return { items: [node("n9", "elsewhere.txt", { folder_id: "fld_bbbbbbbb" })], next_cursor: undefined };
    });
    const { router } = renderAt(FOLDER);
    await waitFor(() => expect(seen.length).toBeGreaterThan(0));
    await act(async () => {
      await router.navigate(other);
    });
    await screen.findByRole("row", { name: "elsewhere.txt" });
    expect(seen[0]?.aborted).toBe(true);
  });
});

describe("the address selects what it names, each time it names it", () => {
  it("selects again when the page is returned to the same address after another", async () => {
    const { router } = renderAt(`${FOLDER}?select=n2`);
    await screen.findByRole("row", { name: "beta.txt" });
    await waitFor(() => expect(rows("beta.txt")?.getAttribute("aria-selected")).toBe("true"));
    await act(async () => {
      await router.navigate(`${FOLDER}/nod_bbbbbbbb`);
    });
    // Back to the very address it had before: the same path and the same query.
    await act(async () => {
      await router.navigate(`${FOLDER}?select=n2`);
    });
    await waitFor(() => expect(rows("beta.txt")?.getAttribute("aria-selected")).toBe("true"));
  });

  it("reads on when the item is not in the listing it has, rather than giving up", async () => {
    api.childrenPage
      .mockResolvedValueOnce({ items: [node("n1", "alpha.txt")], next_cursor: undefined })
      .mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    renderAt(`${FOLDER}?select=n2`);
    await waitFor(() => expect(rows("beta.txt")?.getAttribute("aria-selected")).toBe("true"));
  });
});

describe("what a role may do on the listing", () => {
  it("a viewer is offered no upload", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(screen.queryByRole("button", { name: /upload files/i })).toBeNull();
  });

  it("a viewer's selection offers no move, and the Delete key opens no dialog", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(`${FOLDER}?select=n1`);
    await waitFor(() => expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true"));
    expect(screen.queryByRole("button", { name: "Move" })).toBeNull();
    fireEvent.keyDown(rows("alpha.txt") as Element, { key: "Delete" });
    expect(document.querySelector(".ant-modal")).toBeNull();
    expect(api.deleteNode).not.toHaveBeenCalled();
  });

  it("offers no download of a file in a space that forbids downloads to viewers", async () => {
    place.role = "viewer";
    place.noDownload = true;
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(`${FOLDER}?select=n1`);
    await waitFor(() => expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true"));
    expect(screen.queryByRole("button", { name: /download/i })).toBeNull();
  });

  it("the header box clears the selection when it is unticked", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const header = () => screen.getAllByRole("checkbox").find((c) => !c.closest("tr[data-id]")) as HTMLElement;
    fireEvent.click(header());
    await waitFor(() => expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true"));
    fireEvent.click(header());
    await waitFor(() => expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("false"));
    expect(rows("beta.txt")?.getAttribute("aria-selected")).toBe("false");
  });
});

describe("the page's rows keep their own height", () => {
  it("does not stretch the header, the toolbar and the listing to fill the page's minimum height", async () => {
    api.childrenPage.mockResolvedValue({ items: [], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByText(/folder is empty/i);
    const grid = document.querySelector("section") as HTMLElement;
    expect(grid.style.alignContent).toBe("start");
  });
});

describe("a rename refreshes what shows the name", () => {
  it("renaming a folder invalidates its place, so the breadcrumbs and title read the new name", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "Reports", { kind: "dir" })], next_cursor: undefined });
    api.renameNode.mockResolvedValue({ id: "n1", name: "Archive", etag: "e2" });
    const { qc } = renderAt(`${FOLDER}?select=n1`);
    await waitFor(() => expect(rows("Reports")?.getAttribute("aria-selected")).toBe("true"));
    const spy = vi.spyOn(qc, "invalidateQueries");
    await chooseMore("Rename");
    const input = document.querySelector("input.rename") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Archive" } });
    fireEvent.keyDown(input, { key: "Enter" });
    await waitFor(() => expect(api.renameNode).toHaveBeenCalled());
    await waitFor(() => {
      const keys = spy.mock.calls.map((c) => (c[0] as { queryKey: unknown[] }).queryKey.join("/"));
      expect(keys.some((k) => k.endsWith("/place"))).toBe(true);
    });
  });
});

describe("a session change stops the work it started", () => {
  it("stops a move after the person signs out in the middle of it", async () => {
    api.moveNode.mockImplementation(async () => {
      session.set(null);
      return node("n1", "alpha.txt");
    });
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", role: "editor" } as never]);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(rows("beta.txt") as Element, { ctrlKey: true });
    fireEvent.click(within(await screen.findByTestId("selection-panel")).getByRole("button", { name: "Move to…" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(await within(dialog).findByText("Team A"));
    fireEvent.click(await within(dialog).findByRole("button", { name: "Move here" }));
    await new Promise((r) => setTimeout(r, 50));
    expect(api.moveNode).toHaveBeenCalled();
    expect(api.moveNode.mock.calls.length).toBe(1);
  });

  it("stops a delete after the person signs out in the middle of it", async () => {
    api.deleteNode.mockImplementation(async () => {
      session.set(null);
    });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(rows("beta.txt") as Element, { ctrlKey: true });
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await new Promise((r) => setTimeout(r, 50));
    expect(api.deleteNode.mock.calls.length).toBe(1);
  });
});

// A folder more than 64 levels down (decision 140): the place is the 64 nearest folders, and the space's root is not known. What
// needs the root is off, not guessed.
describe("a place more than 64 levels down", () => {
  const A = { folder_id: "fld_aaaaaaaa", kind: "dir" as const, mime_type: undefined };
  beforeEach(() => {
    place.truncated = true;
    place.trail = [node("nod_top00001", "Top", { ...A, parent_id: "nod_above001" }), node("nod_mid00001", "Mid", { ...A, parent_id: "nod_top00001" })];
  });

  it("starts its breadcrumbs with the mark for the folders above, and has no crumb for the space's root", async () => {
    renderAt(`${FOLDER}/nod_shown001`);
    await screen.findByRole("row", { name: "alpha.txt" });
    const nav = screen.getByRole("navigation", { name: "Location" });
    const items = Array.from(nav.querySelectorAll("ol > li:not(.ant-breadcrumb-separator)"));
    expect(items[0].textContent).toContain("More folders above");
    expect(items[0].querySelector("a")).toBeNull();
    const links = within(nav).getAllByRole("link").map((a) => [a.textContent, a.getAttribute("href")]);
    expect(links).toEqual([
      ["Top", `${FOLDER}/nod_top00001`],
      ["Mid", `${FOLDER}/nod_mid00001`],
    ]);
    expect(within(nav).queryByRole("link", { name: "Team A" })).toBeNull();
    expect(items[items.length - 1].textContent).toBe("Reports");
  });

  it("gives the folder tree the trail and the folder shown, and no top folder in place of the space", async () => {
    renderAt(`${FOLDER}/nod_shown001`);
    await screen.findByRole("row", { name: "alpha.txt" });
    await waitFor(() => expect(get(treePlace)).toEqual({ folder: "fld_aaaaaaaa", chain: ["nod_top00001", "nod_mid00001", "nod_shown001"], top: undefined }));
  });

  it("offers no New folder and no folder upload, which go by the path from the root, and still takes files", async () => {
    renderAt(`${FOLDER}/nod_shown001`);
    await screen.findByRole("row", { name: "alpha.txt" });
    await waitFor(() => expect(get(newPlace)).toMatchObject({ canFolder: false, canUploadFolder: false, canUpload: true }));
  });
});

// Slice sd1 (decision 104): a folder of the sidebar's tree is a drag source, and the sidebar's Trash takes a drop. The sidebar is not
// rendered here: a tree row and the Trash entry are the DOM the sidebar draws, with the attributes the sidebar draws (Shell, FolderTree).
describe("slice sd1: the sidebar's tree and Trash as drag places", () => {
  const pointer = (type: string, x: number, y: number, extra: PointerEventInit = {}) =>
    new PointerEvent(type, { bubbles: true, cancelable: true, button: 0, buttons: type === "pointerup" ? 0 : 1, pointerType: "mouse", isPrimary: true, clientX: x, clientY: y, ...extra });
  const send = (el: Element | Window | Document, type: string, x: number, y: number, extra: PointerEventInit = {}) => {
    el.dispatchEvent(pointer(type, x, y, extra));
  };
  const dirNode = (id: string, name: string, extra: Partial<Node> = {}) => node(id, name, { kind: "dir", mime_type: undefined, ...extra });
  const A = { folder_id: "fld_aaaaaaaa" };
  let under: Element | null = null;
  let sidebarEls: HTMLElement[] = [];

  // treeRow draws a folder of the sidebar's tree the way FolderTree does: the drag source, then the drop place, with the same values.
  function treeRow(id: string, name: string, parent: string, role = "editor", into: HTMLElement = document.body): HTMLElement {
    const el = document.createElement("div");
    el.className = "tree-row";
    Object.assign(el.dataset, {
      treeDrag: id,
      treeSpace: "fld_aaaaaaaa",
      treeParent: parent,
      treeName: name,
      treeRole: role,
      dropFolder: "fld_aaaaaaaa",
      dropDir: id,
      dropRole: role,
      dropName: name,
      dropPath: ["fld_aaaaaaaa", ...(parent ? [parent] : []), id].join(","),
    });
    into.appendChild(el);
    sidebarEls.push(el);
    return el;
  }
  // trashEntry draws the sidebar's Trash entry as the menu does, with the attributes Shell gives it.
  function trashEntry(): HTMLElement {
    const el = document.createElement("li");
    el.setAttribute("role", "menuitem");
    el.dataset.dropTrash = "";
    el.dataset.dropName = "Trash";
    document.body.appendChild(el);
    sidebarEls.push(el);
    return el;
  }

  beforeEach(() => {
    localStorage.clear();
    under = null;
    sidebarEls = [];
    document.elementFromPoint = () => under;
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true }]);
    api.childrenPage.mockResolvedValue({
      items: [dirNode("d1", "Docs", A), dirNode("d2", "Pics", A), node("n1", "alpha.txt", A), node("n2", "beta.txt", A)],
      next_cursor: undefined,
    });
    api.statOrNull.mockImplementation(async (id: string) => (id === "d1" ? dirNode("d1", "Docs", { ...A, etag: "fresh-d1" }) : node(id, `${id}.txt`, { ...A, etag: `fresh-${id}` })));
    api.moveNode.mockResolvedValue(node("n1", "alpha.txt"));
    api.deleteNode.mockResolvedValue(undefined);
  });
  afterEach(() => {
    for (const el of sidebarEls) el.remove();
    vi.restoreAllMocks();
    folders.set([]);
  });

  // The pointer presses the tree row, moves over the place, and returns the place's state before the release.
  function dragFrom(from: Element, over: Element | null, extra: PointerEventInit = {}) {
    send(from, "pointerdown", 10, 10, extra);
    send(from, "pointermove", 40, 40, extra);
    under = over;
    send(from, "pointermove", 60, 60, extra);
  }
  const release = (extra: PointerEventInit = {}) => send(document.body, "pointerup", 60, 60, extra);

  it("moves a folder of the tree onto a folder row of the listing, as one transfer with Undo", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const mine = treeRow("nod_mine0001", "Mine", "");
    dragFrom(mine, rows("Docs"));
    expect((rows("Docs") as HTMLElement).dataset.dropState).toBe("ok");
    await waitFor(() => expect(screen.getByTestId("drag-ghost").textContent).toContain("Move to"));
    release();
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(1));
    expect(api.moveNode.mock.calls[0][0]).toBe("nod_mine0001");
    expect(api.moveNode.mock.calls[0][1]).toMatchObject({ folder: "fld_aaaaaaaa", parent: "d1" });
    expect(api.copyNode).not.toHaveBeenCalled();
    expect(await screen.findByRole("button", { name: "Undo" })).toBeTruthy();
  });

  it("refuses a folder of the tree dropped on a folder below it in the listing, and says why", async () => {
    // Docs is shown, under the space's root: the page reads the way down from the root to it. (Its ids are as the server's, so the
    // route accepts them.)
    api.statOrNull.mockImplementation(async (id: string) => {
      if (id === "nod_root0001") return dirNode("nod_root0001", "Team A", { parent_id: null });
      if (id === "nod_docs0001") return dirNode("nod_docs0001", "Docs", { ...A, parent_id: "nod_root0001", etag: "fresh-docs" });
      return node(id, `${id}.txt`, { ...A, etag: `fresh-${id}` });
    });
    api.childrenPage.mockResolvedValue({ items: [dirNode("nod_pics0001", "Pics", A), node("n1", "alpha.txt", A)], next_cursor: undefined });
    renderAt(`${FOLDER}/nod_docs0001`);
    await screen.findByRole("row", { name: "Pics" });
    // Pics is a folder inside Docs, so Docs cannot go into Pics. A listing row does not carry the folders above it: the page adds the
    // chain of the folder shown to the drop's place (decision 104).
    const docs = treeRow("nod_docs0001", "Docs", "");
    dragFrom(docs, rows("Pics"));
    expect((rows("Pics") as HTMLElement).dataset.dropState).toBe("refused");
    release();
    await screen.findAllByText("A folder cannot go into itself.");
    expect(api.moveNode).not.toHaveBeenCalled();
  });

  it("moves a listing item onto the sidebar's Trash through Delete, and deletes it after the confirmation", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const trash = trashEntry();
    dragFrom(rows("alpha.txt") as Element, trash);
    expect(trash.dataset.dropState).toBe("ok");
    await waitFor(() => expect(screen.getByTestId("drag-ghost").textContent).toContain("Move to Trash"));
    release();
    await swallowed();
    const dialog = await screen.findByRole("dialog");
    expect(api.deleteNode).not.toHaveBeenCalled();
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledWith("n1"));
    expect(api.moveNode).not.toHaveBeenCalled();
  });

  it("deletes a folder of the tree dropped on the Trash the same way", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const mine = treeRow("nod_mine0001", "Mine", "");
    dragFrom(mine, trashEntry());
    release();
    await swallowed();
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledWith("nod_mine0001"));
  });

  // branchesFrom lists the folder-tree branches the page asked to read again since call number from (decision 104, reviewed).
  const branchesFrom = (from: number) =>
    vi.mocked(refreshTree).mock.calls.slice(from).flatMap((c) => (c[1] ?? []) as Array<{ folder: string; dir: string | null }>);

  it("after a tree folder is moved to the Trash, the tree reads its parent again, and Undo reads it again", async () => {
    // Docs is shown, so the shown branch is not the parent of the folder dragged (Mine, under the space's root).
    window.history.replaceState(null, "", "/");
    renderAt(`${FOLDER}/nod_docs0001`);
    await screen.findByRole("row", { name: "alpha.txt" });
    api.restoreNode.mockResolvedValue(undefined);
    dragFrom(treeRow("nod_mine0001", "Mine", ""), trashEntry());
    release();
    await swallowed();
    const dialog = await screen.findByRole("dialog");
    const beforeDelete = vi.mocked(refreshTree).mock.calls.length;
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledWith("nod_mine0001"));
    await waitFor(() => expect(branchesFrom(beforeDelete)).toContainEqual({ folder: "fld_aaaaaaaa", dir: null }));
    const beforeUndo = vi.mocked(refreshTree).mock.calls.length;
    fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
    await waitFor(() => expect(api.restoreNode).toHaveBeenCalledWith("nod_mine0001"));
    await waitFor(() => expect(branchesFrom(beforeUndo)).toContainEqual({ folder: "fld_aaaaaaaa", dir: null }));
    // Mine is not one of the folders Docs is in, so the page stays (no address was set).
    expect(window.location.pathname).toBe("/");
  });

  it("moving the folder shown to the Trash leaves the page on the space's root; Undo brings the folder back and stays there", async () => {
    window.history.replaceState(null, "", "/");
    renderAt(`${FOLDER}/nod_docs0001`);
    await screen.findByRole("row", { name: "alpha.txt" });
    const depth = window.history.length;
    api.restoreNode.mockResolvedValue(undefined);
    dragFrom(treeRow("nod_docs0001", "Docs", ""), trashEntry());
    release();
    await swallowed();
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledWith("nod_docs0001"));
    await waitFor(() => expect(window.location.pathname).toBe(FOLDER));
    // The trashed place's entry is replaced, so Back does not lead to it.
    expect(window.history.length).toBe(depth);
    fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
    await waitFor(() => expect(api.restoreNode).toHaveBeenCalledWith("nod_docs0001"));
    expect(window.location.pathname).toBe(FOLDER);
  });

  it("moving an ancestor of the folder shown to the Trash leaves the page on that ancestor's parent", async () => {
    // Shown: Pics, inside Reports, inside Docs. Reports goes to the Trash, so the page goes to Docs, its parent.
    window.history.replaceState(null, "", "/");
    place.trail = [dirNode("nod_docs0001", "Docs", A), dirNode("nod_reports1", "Reports", A)];
    renderAt(`${FOLDER}/nod_pics0001`);
    await screen.findByRole("row", { name: "alpha.txt" });
    const depth = window.history.length;
    dragFrom(treeRow("nod_reports1", "Reports", "nod_docs0001"), trashEntry());
    release();
    await swallowed();
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledWith("nod_reports1"));
    await waitFor(() => expect(window.location.pathname).toBe(`${FOLDER}/nod_docs0001`));
    expect(window.history.length).toBe(depth);
  });

  it("moving the topmost folder of a trail cut at 64 to the Trash leaves the page on that folder's parent, not the space's root", async () => {
    // The place is more than 64 levels down (decision 140): Top is the topmost folder of its trail, and its parent is above the trail.
    window.history.replaceState(null, "", "/");
    place.truncated = true;
    place.trail = [dirNode("nod_top00001", "Top", { ...A, parent_id: "nod_above001" }), dirNode("nod_mid00001", "Mid", { ...A, parent_id: "nod_top00001" })];
    renderAt(`${FOLDER}/nod_pics0001`);
    await screen.findByRole("row", { name: "alpha.txt" });
    dragFrom(treeRow("nod_top00001", "Top", "nod_above001"), trashEntry());
    release();
    await swallowed();
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledWith("nod_top00001"));
    await waitFor(() => expect(window.location.pathname).toBe(`${FOLDER}/nod_above001`));
  });

  it("a folder of the listing moved to the Trash leaves the page where it is, and so does its Undo", async () => {
    // Pics is a row of Docs, the folder shown: it is not one of the folders Docs is in.
    window.history.replaceState(null, "", "/");
    api.childrenPage.mockResolvedValue({ items: [dirNode("nod_pics0001", "Pics", A), node("n1", "alpha.txt", A)], next_cursor: undefined });
    api.restoreNode.mockResolvedValue(undefined);
    renderAt(`${FOLDER}/nod_docs0001`);
    await screen.findByRole("row", { name: "Pics" });
    dragFrom(rows("Pics") as Element, trashEntry());
    release();
    await swallowed();
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledWith("nod_pics0001"));
    fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
    await waitFor(() => expect(api.restoreNode).toHaveBeenCalledWith("nod_pics0001"));
    expect(window.location.pathname).toBe("/");
  });

  it("refuses Ctrl over the Trash: nothing is copied there, and nothing is deleted", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const trash = trashEntry();
    dragFrom(rows("alpha.txt") as Element, trash, { ctrlKey: true });
    expect(trash.dataset.dropState).toBe("refused");
    release({ ctrlKey: true });
    await waitFor(() => expect(screen.getAllByText("Nothing is copied to the Trash. Let go without Ctrl to move to it.").length).toBeGreaterThan(0));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.deleteNode).not.toHaveBeenCalled();
    expect(api.copyNode).not.toHaveBeenCalled();
  });

  it("shows the Trash as refused, and deletes nothing, when Delete is not allowed for the item", async () => {
    api.childrenPage.mockResolvedValue({ items: [dirNode("d1", "Docs", A), node("n2", "beta.txt", { ...A, role: "viewer" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "beta.txt" });
    const trash = trashEntry();
    dragFrom(rows("beta.txt") as Element, trash);
    expect(trash.dataset.dropState).toBe("refused");
    release();
    await waitFor(() => expect(screen.getAllByText("These items cannot go to the Trash: you may not delete them.").length).toBeGreaterThan(0));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.deleteNode).not.toHaveBeenCalled();
  });

  it("scrolls the sidebar when a drag holds in its bottom edge band, and not when the pointer is beside it", async () => {
    // The sidebar's scroll region (.shell-scroll) is stated with its box: 264 px wide, 400 px tall, 1000 px of content.
    const side = document.createElement("div");
    side.className = "shell-scroll";
    side.style.overflowY = "auto";
    side.getBoundingClientRect = () => new DOMRect(0, 0, 264, 400);
    Object.defineProperty(side, "scrollHeight", { value: 1000, configurable: true });
    Object.defineProperty(side, "clientHeight", { value: 400, configurable: true });
    let scrollTop = 0;
    Object.defineProperty(side, "scrollTop", { get: () => scrollTop, set: (v: number) => (scrollTop = v), configurable: true });
    document.body.appendChild(side);
    sidebarEls.push(side);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const mine = treeRow("nod_mine0001", "Mine", "", "editor", side);
    send(mine, "pointerdown", 10, 10);
    send(mine, "pointermove", 40, 40);
    // Beside the sidebar (x 600), at its bottom edge: the sidebar does not move.
    send(mine, "pointermove", 600, 380);
    await new Promise((r) => setTimeout(r, 60));
    expect(scrollTop).toBe(0);
    // In the sidebar, 20 px above its bottom edge: it scrolls down.
    send(mine, "pointermove", 100, 380);
    await waitFor(() => expect(scrollTop).toBeGreaterThan(0));
    release();
  });

});

describe("a drop onto a dialog is not a drop onto the folder behind it", () => {
  it("does not start an upload for a file dropped on an open dialog", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await chooseNew("New folder");
    const dialog = await screen.findByRole("dialog");
    const file = new File(["x"], "dropped.txt", { type: "text/plain" });
    fireEvent.drop(dialog, { dataTransfer: { files: [file], types: ["Files"], items: [] } });
    await new Promise((r) => setTimeout(r, 50));
    expect(uploadsMock.add).not.toHaveBeenCalled();
  });
});

describe("the page's title and the delete dialog", () => {
  it("gives the title back when the page is left", async () => {
    document.title = "Before the browser";
    const { unmount } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(document.title).toContain("Team A");
    unmount();
    expect(document.title).toBe("Before the browser");
  });

  it("focuses the confirm button of the delete dialog, as the Svelte page did", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    await waitFor(() => expect(document.activeElement).toBe(within(dialog).getByRole("button", { name: "Move to trash" })));
  });
});

describe("the new folder dialog on the files page", () => {
  it("finds its name input by the label, and the create button shows loading while the folder is made", async () => {
    api.mkdir.mockReturnValue(new Promise(() => undefined));
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await chooseNew("New folder");
    const dialog = await screen.findByRole("dialog");
    const input = within(dialog).getByLabelText("Folder name");
    expect(input.id).toBe("newfolder-name");
    fireEvent.change(input, { target: { value: "Plans" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Create folder" }));
    await waitFor(() => expect(api.mkdir).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(dialog.querySelector(".ant-modal-footer .ant-btn-primary")?.className).toContain("ant-btn-loading"));
  });
});

describe("the dialogs close on Escape only", () => {
  it("keeps the new-folder dialog open when the mask behind it is clicked", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await chooseNew("New folder");
    await screen.findByRole("dialog");
    const mask = document.querySelector(".ant-modal-wrap") as HTMLElement;
    fireEvent.mouseDown(mask);
    fireEvent.mouseUp(mask);
    fireEvent.click(mask);
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByRole("dialog")).not.toBeNull();
  });
});

describe("the page reads nothing without a session", () => {
  it("makes no request for the listing when there is no session", async () => {
    session.set(null);
    renderAt(FOLDER);
    await new Promise((r) => setTimeout(r, 50));
    expect(api.childrenPage).not.toHaveBeenCalled();
    expect(api.getMe).not.toHaveBeenCalled();
    expect(locate).not.toHaveBeenCalled();
  });
});

export const storeForTests = writable(0);
export type { RouterProvider as _unused };

describe("sharing from the browser", () => {
  beforeEach(() => {
    shareStub.mounted = 0;
    shareStub.props = [];
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true }]);
  });
  afterEach(() => folders.set([]));

  // The listing is read with the role the item gives the caller; node() reads place.role when it is called.
  function listing(role: Role, extra: Partial<Node> = {}) {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role, ...extra })], next_cursor: undefined });
  }

  function selectOnly(name: string) {
    fireEvent.click(rows(name) as Element);
  }

  it("offers Share in the selection bar to a manager of the item", async () => {
    place.role = "manager";
    listing("manager");
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    await screen.findByRole("button", { name: "Delete" });
    expect(await inPanel("Share")).toBeTruthy();
  });

  it.each([
    ["a viewer", "viewer", {}, false],
    ["a commenter", "commenter", {}, false],
    ["an editor who may not share", "editor", { editors_can_share: false }, false],
    ["a manager", "manager", {}, true],
  ] as const)("shows Share to %s only when the caller may share the item", async (_label, role, extra, shown) => {
    place.role = role;
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role, ...extra })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    const toolbar = await screen.findByTestId("selection-panel");
    expect(within(toolbar).queryByRole("button", { name: "Share" }) !== null).toBe(shown);
  });

  it("disables Share when the picks are not exactly one item, and does not hide it", async () => {
    place.role = "manager";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "manager" }), node("n2", "beta.txt", { role: "manager" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    expect(await inPanel("Share")).toBeTruthy();
    fireEvent.click(await screen.findByRole("row", { name: "beta.txt" }), { ctrlKey: true });
    await waitFor(() => expect((within(screen.getByTestId("selection-panel")).getByRole("button", { name: "Share" }) as HTMLButtonElement).disabled).toBe(true));
  });

  it("Details shows Who has access to every caller, and its own Share only where Share is offered", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "viewer" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    await chooseMore("Details");
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("button", { name: "Who has access" })).toBeTruthy();
    expect(within(dialog).queryByRole("button", { name: "Share" })).toBeNull();
  });

  it("opens the share dialog for the item, in the space the listing is in", async () => {
    place.role = "manager";
    listing("manager");
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    fireEvent.click(await inPanel("Share"));
    const dialog = await screen.findByRole("dialog", { name: "stub share" });
    expect(dialog).toBeTruthy();
    expect(shareStub.props.at(-1)?.node.id).toBe("n1");
    expect(shareStub.props.at(-1)?.space?.id).toBe("fld_aaaaaaaa");
  });

  it("closes the share dialog through its own onclose, and the page does not close it on its own", async () => {
    place.role = "manager";
    listing("manager");
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    fireEvent.click(await inPanel("Share"));
    await screen.findByRole("dialog", { name: "stub share" });
    expect(screen.getByRole("dialog", { name: "stub share" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "stub close" }));
    await waitFor(() => expect(screen.queryByRole("dialog", { name: "stub share" })).toBeNull());
  });

  it("reads the listing again when the dialog changes something", async () => {
    place.role = "manager";
    listing("manager");
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    fireEvent.click(await inPanel("Share"));
    await screen.findByRole("dialog", { name: "stub share" });
    api.childrenPage.mockClear();
    fireEvent.click(screen.getByRole("button", { name: "stub changed" }));
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalled());
  });

  it("a change of person closes an open share dialog", async () => {
    place.role = "manager";
    listing("manager");
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    fireEvent.click(await inPanel("Share"));
    await screen.findByRole("dialog", { name: "stub share" });
    act(() => {
      session.set(signedAs("u2", "t2"));
    });
    // The page reads the place again for the new session, and the dialog must not come back with the listing.
    await screen.findByRole("row", { name: "alpha.txt" });
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.queryByRole("dialog", { name: "stub share" })).toBeNull();
  });

  it("a directory that is not there offers the access request for that directory", async () => {
    vi.mocked(locate).mockResolvedValueOnce(null as never);
    renderAt(`${FOLDER}/nod_dddddddd`);
    expect(await screen.findByText("request for nod_dddddddd")).toBeTruthy();
  });
});

// Review item 2 (wf1): a folder that cannot be read says why to the sidebar's New, instead of "Loading the folder" for ever.
describe("a failed place read is named to the sidebar's New", () => {
  it("the page shows the error, and newPlaceFailure gives the sidebar the same text", async () => {
    const err = new ApiError(500, "internal", {}, 0);
    const real = vi.mocked(locate).getMockImplementation()!;
    vi.mocked(locate).mockRejectedValue(err);
    try {
      renderAt(FOLDER);
      const text = fileErrorText(err, "en");
      expect(await screen.findByText(text)).toBeTruthy();
      expect(get(newPlaceFailure)).toBe(text);
    } finally {
      vi.mocked(locate).mockImplementation(real);
    }
  });

  it("a read that works has no failure to name", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(get(newPlaceFailure)).toBeNull();
  });

  // Review item 3 (wf1): the failure is cleared when a later place reads fine, and when the page is left.
  it("the failure is cleared when a later place reads fine", async () => {
    const err = new ApiError(500, "internal", {}, 0);
    const real = vi.mocked(locate).getMockImplementation()!;
    vi.mocked(locate).mockRejectedValue(err);
    let router: ReturnType<typeof renderAt>["router"];
    try {
      ({ router } = renderAt(FOLDER));
      await screen.findByText(fileErrorText(err, "en"));
      expect(get(newPlaceFailure)).toBe(fileErrorText(err, "en"));
    } finally {
      vi.mocked(locate).mockImplementation(real);
    }
    await act(async () => {
      await router.navigate(`${FOLDER}/nod_bbbbbbbb`);
    });
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(get(newPlaceFailure)).toBeNull();
  });

  it("the failure is cleared when the page is left", async () => {
    const err = new ApiError(500, "internal", {}, 0);
    const real = vi.mocked(locate).getMockImplementation()!;
    vi.mocked(locate).mockRejectedValue(err);
    let view: ReturnType<typeof renderAt>;
    try {
      view = renderAt(FOLDER);
      await screen.findByText(fileErrorText(err, "en"));
      expect(get(newPlaceFailure)).not.toBeNull();
    } finally {
      vi.mocked(locate).mockImplementation(real);
    }
    view.unmount();
    expect(get(newPlaceFailure)).toBeNull();
  });
});

describe("a change of address closes what was open at the last one", () => {
  it("a preview that was open is closed when the address changes, so it is not left on top of the new place", async () => {
    const { router } = renderAt(`${FOLDER}?preview=n2`);
    await screen.findByRole("dialog");
    await act(async () => {
      await router.navigate(`${FOLDER}/nod_bbbbbbbb`);
    });
    // Wait for the new place to be shown, so the check is made on the page as a person sees it.
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it("a delete confirmation that was open is closed when the address changes, so it cannot act on the old selection", async () => {
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    await screen.findByRole("dialog");
    await act(async () => {
      await router.navigate(`${FOLDER}/nod_bbbbbbbb`);
    });
    // The new place's rows are there at once, since the page keeps the last place on screen while it loads (fu3b). jsdom runs no
    // close motion: a modal that is leaving has the motion's class, and is hidden by hand here as the motion does when it ends. A
    // modal that is still open has no such class, so the button stays and the check fails.
    await screen.findByRole("row", { name: "alpha.txt" });
    await waitFor(() => {
      const leaving = document.querySelector<HTMLElement>(".ant-modal.ant-zoom-leave");
      if (leaving) (leaving.closest(".ant-modal-wrap") as HTMLElement).style.display = "none";
      expect(screen.queryByRole("button", { name: "Move to trash" })).toBeNull();
    });
    expect(api.deleteNode).not.toHaveBeenCalled();
  });
});

// At phone width the selection bar is one row: the main action, a More menu with the rest, and Clear. Delete stays in
// the menu, marked as destructive.
describe("the selection bar at phone width", () => {
  beforeEach(() => {
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true }]);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    folders.set([]);
  });

  it("at phone width the selection panel is on top, with Clear, the count and icons, and the rest in More", async () => {
    place.role = "editor";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "notes.txt", { role: "editor" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "notes.txt" });
    fireEvent.click(rows("notes.txt") as Element);
    const panel = await screen.findByTestId("selection-panel");
    expect(within(panel).getByRole("button", { name: "Clear selection" })).toBeTruthy();
    expect(within(panel).getByRole("button", { name: "Download" })).toBeTruthy();
    expect(within(panel).getByRole("button", { name: "Delete" })).toBeTruthy();
    expect(within(panel).queryByRole("button", { name: "Rename" })).toBeNull();
    fireEvent.click(within(panel).getByRole("button", { name: "More" }));
    const del = await screen.findByText("Rename");
    expect(del.closest(".ant-dropdown-menu-item")).toBeTruthy();
    // No bar under the listing: the panel is the only selection UI.
    expect(screen.queryByTestId("selection-bar")).toBeNull();
  });
});

// The actions sit in the top toolbar, and the same list feeds the right-click menu. Ctrl+C, Ctrl+X and Ctrl+V work within
// the tab, Alt+Up goes up a folder, and a key typed in a field is left to the field.
describe("the toolbar, the right-click menu and the clipboard keys", () => {
  beforeEach(() => {
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true }]);
  });
  afterEach(() => {
    folders.set([]);
  });

  it("keeps the actions in the top toolbar: the usual row until something is selected, then the panel; no bar under the listing", async () => {
    place.role = "editor";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const toolbar = screen.getByRole("toolbar", { name: "Folder actions" });
    expect(within(toolbar).queryByTestId("selection-panel")).toBeNull();
    fireEvent.click(rows("alpha.txt") as Element);
    const panel = await within(toolbar).findByTestId("selection-panel");
    expect((within(panel).getByRole("button", { name: "Download" }) as HTMLButtonElement).disabled).toBe(false);
    expect(screen.queryByTestId("selection-bar")).toBeNull();
  });

  it("a viewer's toolbar and menu never offer Delete, Move, Rename, Cut or Paste", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "viewer" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    const toolbar = screen.getByRole("toolbar", { name: "Folder actions" });
    for (const name of ["Delete", "Move to…", "Rename", "Cut", "Paste"]) expect(within(toolbar).queryByRole("button", { name })).toBeNull();
    fireEvent.contextMenu(rows("alpha.txt") as Element);
    expect(await menuEntry("Copy to…", "Organize")).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "Move to trash" })).toBeNull();
  });

  it("a right-click on an item that is not selected selects it, and opens the menu with the same actions", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.contextMenu(rows("beta.txt") as Element, { clientX: 100, clientY: 200 });
    await waitFor(() => expect(rows("beta.txt")?.getAttribute("aria-selected")).toBe("true"));
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("false");
    expect(await screen.findByRole("menuitem", { name: "Download" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Rename" })).toBeTruthy();
  });

  it("a right-click on a selected item keeps the whole selection", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(rows("beta.txt") as Element, { ctrlKey: true });
    fireEvent.contextMenu(rows("alpha.txt") as Element);
    await screen.findByRole("menuitem", { name: "Move to trash" });
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true");
    expect(rows("beta.txt")?.getAttribute("aria-selected")).toBe("true");
  });

  it("a right-click on empty space offers New folder, Upload and Paste, and Paste is disabled with nothing copied", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.contextMenu(document.querySelector(".browser") as Element);
    expect(await screen.findByRole("menuitem", { name: "New folder" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Upload files" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Paste" }).getAttribute("aria-disabled")).toBe("true");
  });

  it("Ctrl+X and then Ctrl+V move the selected item into this folder, dim it until then, and empty the clipboard", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    api.moveNode.mockResolvedValue(node("n1", "alpha.txt"));
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    await waitFor(() => expect((rows("alpha.txt") as HTMLElement).style.opacity).toBe("0.5"));
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(1));
    expect(api.moveNode.mock.calls[0][0]).toBe("n1");
    expect(api.moveNode.mock.calls[0][1]).toMatchObject({ folder: "fld_aaaaaaaa", path: "/" });
    await waitFor(() => expect((rows("alpha.txt") as HTMLElement).style.opacity).toBe(""));
  });

  // A closed Delete dialog stays in the DOM, so the shortcut guard must look at what is on screen, not at what is there.
  it("Delete, then Cancel, then Ctrl+X still cuts the selected item", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(within(await screen.findByTestId("selection-panel")).getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    // jsdom runs no close motion. When the motion ends, rc-dialog sets the wrapper's inline display to none and leaves the
    // dialog in the DOM; that end state is set here, so the shortcut guard meets the same page a browser shows.
    await waitFor(() => expect(document.querySelector(".ant-modal-wrap")).not.toBeNull());
    (document.querySelector(".ant-modal-wrap") as HTMLElement).style.display = "none";
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    await waitFor(() => expect((rows("alpha.txt") as HTMLElement).style.opacity).toBe("0.5"));
  });

  // A row cannot take a text selection (tiles cannot either), so Shift+click then Ctrl+C copies the files, not stray text.
  it("a row cannot take a text selection, so Shift+click then Ctrl+C copies the files", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "beta.txt" });
    expect((rows("alpha.txt") as HTMLElement).style.userSelect).toBe("none");
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(rows("beta.txt") as Element, { shiftKey: true });
    fireEvent.keyDown(document.body, { key: "c", ctrlKey: true });
    await waitFor(() => expect(get(clipboard)?.mode).toBe("copy"));
    expect(get(clipboard)?.nodes.map((n) => n.id)).toEqual(["n1", "n2"]);
  });

  // Text selected elsewhere on the page is still copied as text: Ctrl+C does not take the files then.
  it("text selected on the page is copied as text, and Ctrl+C does not take the picked files", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    const range = document.createRange();
    range.selectNodeContents(document.querySelector(".ant-breadcrumb") ?? document.body);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    fireEvent.keyDown(document.body, { key: "c", ctrlKey: true });
    window.getSelection()?.removeAllRanges();
    expect(get(clipboard)).toBeNull();
  });

  // A long count is cut with an ellipsis, so its full text is in the title, and it is the live region that is read out.
  it("the selection count keeps its full text in a title, and is the live region", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(rows("beta.txt") as Element, { ctrlKey: true });
    const panel = await screen.findByTestId("selection-panel");
    const count = within(panel).getByText("2 selected");
    expect(count.getAttribute("title")).toBe("2 selected");
    expect(count.getAttribute("aria-live")).toBe("polite");
  });

  // The More button of the panel says what it is on hover, as the icons do.
  it("More in the selection panel has a tooltip", async () => {
    const original = window.matchMedia;
    window.matchMedia = ((query: string) => ({ ...original(query), matches: false })) as typeof window.matchMedia;
    try {
      renderAt(FOLDER);
      await screen.findByRole("row", { name: "alpha.txt" });
      fireEvent.click(rows("alpha.txt") as Element);
      const panel = await screen.findByTestId("selection-panel");
      fireEvent.pointerEnter(within(panel).getByRole("button", { name: "More" }), { pointerType: "mouse" });
      expect(await screen.findByRole("tooltip", { name: "More" })).toBeTruthy();
    } finally {
      window.matchMedia = original;
    }
  });

  // Rows keep the scroll margin of the sticky header and toolbar, so the focused row is never left under them.
  it("a row scrolls into view below the sticky header and toolbar", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    // The shell header (56), the toolbar row (56 with a mouse: the 40 px icon buttons), the pinned column header (48) and 4 of air.
    expect((rows("alpha.txt") as HTMLElement).style.scrollMarginTop).toBe("164px");
  });

  // After a confirmed delete from the panel, the panel goes with the selection, and the focus must not be lost to the body.
  it("after a confirmed delete from the panel, the focus goes to the listing", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    api.deleteNode.mockImplementation(async () => {
      api.childrenPage.mockResolvedValue({ items: [node("n2", "beta.txt")], next_cursor: undefined });
    });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "beta.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(within(await screen.findByTestId("selection-panel")).getByRole("button", { name: "Delete" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Move to trash" }));
    await waitFor(() => expect(api.deleteNode).toHaveBeenCalledTimes(1));
    await waitFor(() => expect(screen.queryByTestId("selection-panel")).toBeNull());
    await waitFor(() => expect((document.activeElement as HTMLElement | null)?.dataset.id).toBe("n2"));
  });

  it("with a dialog open, Ctrl+X does nothing to the listing", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(within(await screen.findByTestId("selection-panel")).getByRole("button", { name: "Delete" }));
    await screen.findByRole("dialog");
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    await new Promise((r) => setTimeout(r, 50));
    expect((rows("alpha.txt") as HTMLElement).style.opacity).toBe("");
  });

  it("Ctrl+C and then Ctrl+V copy into this folder, and the source stays where it is", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    api.copyNode.mockResolvedValue({ ...node("n3", "alpha.txt") });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "c", ctrlKey: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(api.copyNode).toHaveBeenCalledTimes(1));
    expect(api.copyNode.mock.calls[0][0]).toBe("n1");
    expect(api.moveNode).not.toHaveBeenCalled();
    expect((rows("alpha.txt") as HTMLElement).style.opacity).toBe("");
  });

  it("Ctrl+V is refused for a viewer, with a clear message, and nothing is sent", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "viewer" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "c", ctrlKey: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    expect(await screen.findByText("You cannot paste into this folder.")).toBeTruthy();
    expect(api.copyNode).not.toHaveBeenCalled();
    expect(api.moveNode).not.toHaveBeenCalled();
  });

  it("Ctrl+X on items the caller cannot move is refused, and nothing is cut", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "viewer" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    expect(await screen.findByText("You cannot move some of these items, so none were cut.")).toBeTruthy();
    expect((rows("alpha.txt") as HTMLElement).style.opacity).toBe("");
  });

  it("Escape cancels a cut, and the items are no longer dimmed", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    await waitFor(() => expect((rows("alpha.txt") as HTMLElement).style.opacity).toBe("0.5"));
    fireEvent.keyDown(document.body, { key: "Escape" });
    await waitFor(() => expect((rows("alpha.txt") as HTMLElement).style.opacity).toBe(""));
    expect(api.moveNode).not.toHaveBeenCalled();
  });

  it("Alt+ArrowUp goes up a folder, as Backspace does", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(`${FOLDER}/nod_reports1`);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.keyDown(document.body, { key: "ArrowUp", altKey: true });
    // Outside the app there is no router to go by, so the address is read from the window's history.
    await waitFor(() => expect(window.location.pathname).toBe(FOLDER));
  });

  it("Ctrl+V typed in the rename field pastes nothing", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "c", ctrlKey: true });
    fireEvent.keyDown(rows("alpha.txt") as Element, { key: "F2" });
    const field = await screen.findByRole("textbox", { name: "New name" });
    fireEvent.keyDown(field, { key: "v", ctrlKey: true });
    fireEvent.keyDown(field, { key: "Backspace" });
    expect(api.copyNode).not.toHaveBeenCalled();
    expect(api.moveNode).not.toHaveBeenCalled();
    expect(document.querySelector('[role="toolbar"]')).toBeTruthy();
  });
});

describe("the review round: pastes, the clipboard's lifetime, the menus and the narrow toolbar", () => {
  beforeEach(() => {
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false } as never]);
    clipboard.set(null);
  });
  afterEach(() => {
    folders.set([]);
    clipboard.set(null);
  });

  it("a paste that is running is not started again, and a key held down pastes once", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
    let finish: (v: Node) => void = () => undefined;
    api.copyNode.mockReturnValue(new Promise<Node>((resolve) => (finish = resolve)));
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "c", ctrlKey: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true, repeat: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(api.copyNode).toHaveBeenCalledTimes(1));
    finish(node("n3", "alpha.txt"));
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalled());
    expect(api.copyNode).toHaveBeenCalledTimes(1);
  });

  it("Ctrl+C with text selected on the page is left to the browser: no item is copied", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    const range = document.createRange();
    range.selectNodeContents(document.getElementById("browser-title") as HTMLElement);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(range);
    const notPrevented = fireEvent.keyDown(document.body, { key: "c", ctrlKey: true });
    window.getSelection()?.removeAllRanges();
    expect(notPrevented).toBe(true);
    expect(get(clipboard)).toBeNull();
  });

  it("a viewer's right-click on empty space keeps the browser's menu, since the place has nothing to offer", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "viewer" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const section = document.querySelector("section.browser") as HTMLElement;
    expect(fireEvent.contextMenu(section, { clientX: 40, clientY: 50 })).toBe(true);
    expect(document.querySelector(".ant-dropdown:not(.ant-dropdown-hidden)")).toBeNull();
  });

  it("the clipboard lasts for the tab: a visit that comes back keeps it, and a change of person empties it", async () => {
    setClip("cut", [node("n1", "alpha.txt")]);
    const first = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    first.unmount();
    expect(get(clipboard)?.mode).toBe("cut");
    const second = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(get(clipboard)?.mode).toBe("cut");
    second.unmount();
    session.set(signedAs("u2"));
    await waitFor(() => expect(get(clipboard)).toBeNull());
  });

  it("a sign-out empties the clipboard too, and the page need not be open for it", () => {
    setClip("copy", [node("n1", "alpha.txt")]);
    session.set(null);
    expect(get(clipboard)).toBeNull();
  });

  it("a paste where one item fails keeps that item cut, takes the moved one off the clipboard, and shows the reason", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    const refused = new ApiError(400, "invalid_request", {}, 0);
    api.moveNode.mockImplementation(async (id: string) => {
      if (id === "n2") throw refused;
      return node(id, id === "n1" ? "alpha.txt" : "beta.txt");
    });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "beta.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(rows("beta.txt") as Element, { ctrlKey: true });
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(get(clipboard)?.nodes.map((n) => n.id)).toEqual(["n2"]));
    expect(get(clipboard)?.mode).toBe("cut");
    expect(await screen.findByText(`beta.txt: ${fileErrorText(refused, "en")}`)).toBeTruthy();
  });

  // The clipboard holds the items as they were when they were cut, so each paste re-reads them: the move uses the etag the server
  // has now, not the one from the cut.
  it("a paste moves each item with its current etag, not the one it had when it was cut", async () => {
    setClip("cut", [node("n1", "alpha.txt", { etag: "old" })]);
    api.statOrNull.mockResolvedValue(node("n1", "alpha.txt", { etag: "fresh" }));
    api.moveNode.mockResolvedValue(node("n1", "alpha.txt"));
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(1));
    expect(api.moveNode.mock.calls[0][2]).toBe("fresh");
  });

  // An item deleted since the cut is not sent to the server: it leaves the clipboard, and the notice names it.
  it("a paste drops an item that no longer exists from the clipboard, and names it in the notice", async () => {
    setClip("cut", [node("n1", "alpha.txt", { etag: "e1" }), node("n9", "gone.txt")]);
    const refused = new ApiError(400, "invalid_request", {}, 0);
    api.moveNode.mockRejectedValue(refused);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(get(clipboard)?.nodes.map((n) => n.id)).toEqual(["n1"]));
    expect(api.moveNode.mock.calls.map((c) => c[0])).toEqual(["n1"]);
    expect(await screen.findByText(/^gone\.txt: /)).toBeTruthy();
  });

  // Seven items fail for two different reasons: the notice names the first five, each with its own reason, and says how many more.
  it("a paste where many items fail names five of them with their reasons, then says how many more", async () => {
    const names = ["a0.txt", "a1.txt", "a2.txt", "a3.txt", "a4.txt", "a5.txt", "a6.txt"];
    setClip("cut", names.map((nm, i) => node(`m${i}`, nm)));
    api.statOrNull.mockImplementation(async (id: string) => node(id, names[Number(id.slice(1))]));
    const refused = new ApiError(400, "invalid_request", {}, 0);
    const locked = new ApiError(423, "locked", {}, 0);
    api.moveNode.mockImplementation(async (id: string) => {
      throw Number(id.slice(1)) < 2 ? refused : locked;
    });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    expect(await screen.findByText("and 2 more")).toBeTruthy();
    expect(screen.getByText(`a0.txt: ${fileErrorText(refused, "en")}`)).toBeTruthy();
    expect(screen.getByText(`a1.txt: ${fileErrorText(refused, "en")}`)).toBeTruthy();
    expect(screen.getByText(`a4.txt: ${fileErrorText(locked, "en")}`)).toBeTruthy();
    expect(screen.queryByText(/^a5\.txt: /)).toBeNull();
    expect(get(clipboard)?.nodes.length).toBe(7);
  });

  it("on a narrow window the panel keeps Share and Download as icons, and Delete goes into its More menu", async () => {
    const original = window.matchMedia;
    window.matchMedia = ((query: string) => ({ ...original(query), matches: false })) as typeof window.matchMedia;
    try {
      renderAt(FOLDER);
      await screen.findByRole("row", { name: "alpha.txt" });
      fireEvent.click(rows("alpha.txt") as Element);
      const panel = await screen.findByTestId("selection-panel");
      expect(within(panel).getByRole("button", { name: "Download" })).toBeTruthy();
      expect(within(panel).queryByRole("button", { name: "Delete" })).toBeNull();
      fireEvent.click(within(panel).getByRole("button", { name: "More" }));
      expect(await screen.findByRole("menuitem", { name: "Move to trash" })).toBeTruthy();
    } finally {
      window.matchMedia = original;
    }
  });

  it("the right-click menu of one file leaves out Download as zip, and groups its actions with dividers", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.contextMenu(rows("alpha.txt") as Element, { clientX: 50, clientY: 60 });
    const menu = await screen.findByRole("menu");
    expect(within(menu).queryByRole("menuitem", { name: "Download as zip" })).toBeNull();
    expect(within(menu).getByRole("menuitem", { name: "Rename" })).toBeTruthy();
    expect(menu.querySelectorAll(".ant-dropdown-menu-item-divider").length).toBeGreaterThanOrEqual(2);
  });

  it("Shift+F10 on a focused row opens its menu with the focus in it, and Escape gives the focus back to the row", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const row = rows("alpha.txt") as HTMLElement;
    row.focus();
    fireEvent.keyDown(row, { key: "F10", shiftKey: true });
    const menu = await screen.findByRole("menu");
    await waitFor(() => expect(menu.contains(document.activeElement)).toBe(true));
    fireEvent.keyDown(document.activeElement as Element, { key: "Escape", keyCode: 27 });
    await waitFor(() => expect(document.activeElement).toBe(row));
  });
});

// Slice w2, the fixes found in the live reading of w1 (decision 49, "Notes for w2" of decision 44).
describe("slice w2: the five fixes from w1's notes", () => {
  beforeEach(() => {
    localStorage.clear();
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true }]);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    folders.set([]);
  });

  // (a) Copy link: this app's own address of the item, the signed-in page /n/{id}. It makes no public link.
  it("Copy link in the selection panel copies this app's own address for one item, and makes no link", async () => {
    const written: string[] = [];
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async (s: string) => void written.push(s) } });
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    const panel = await screen.findByTestId("selection-panel");
    fireEvent.click(within(panel).getByRole("button", { name: "Copy link" }));
    await waitFor(() => expect(written).toEqual([`${window.location.origin}/n/n1`]));
    expect(await screen.findByText(/Link copied/)).toBeTruthy();
    // The notice does not show the address, which has the item's raw ID in it.
    expect(document.body.textContent).not.toContain("/n/n1");
    // Two items: the button is there and off, as Share is.
    fireEvent.click(rows("beta.txt") as Element, { ctrlKey: true });
    await waitFor(() => expect((within(panel).getByRole("button", { name: "Copy link" }) as HTMLButtonElement).disabled).toBe(true));
  });

  it("Copy link says so when the browser will not copy", async () => {
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText: async () => Promise.reject(new Error("blocked")) } });
    const exec = vi.fn(() => false);
    Object.defineProperty(document, "execCommand", { configurable: true, value: exec });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(within(await screen.findByTestId("selection-panel")).getByRole("button", { name: "Copy link" }));
    expect(await screen.findByText("The link could not be copied.")).toBeTruthy();
  });

  // (b) The column header stays under the toolbar in a long folder.
  it("pins the list's column header under the toolbar", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    // The header is pinned in the list's own table (fu3b, item 1): its cells are sticky, at the place the list sets (the variable).
    const region = document.querySelector<HTMLElement>(".files-list");
    expect(document.querySelector(".ant-table-sticky-holder")).toBeNull();
    // The shell's header (56) and the toolbar's row (56 with a mouse).
    expect(region?.style.getPropertyValue("--sc-list-top")).toBe("112px");
    expect(document.querySelector(".files-list .ant-table-thead th")).not.toBeNull();
  });

  // (c) Select all says that it took only the loaded rows when more pages exist.
  it("Select all says it took only the loaded rows when more pages exist", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: "c2" });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(screen.getAllByRole("checkbox")[0]);
    expect(await screen.findByText(/Selected the items loaded so far: 2/)).toBeTruthy();
  });

  it("Ctrl+A says the same, and with every page loaded it says nothing", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    (rows("alpha.txt") as HTMLElement).focus();
    fireEvent.keyDown(rows("alpha.txt") as Element, { key: "a", ctrlKey: true });
    await screen.findByText("2 selected");
    expect(screen.queryByText(/loaded so far/)).toBeNull();
  });

  it("Ctrl+A with more pages to load says the same as the header box", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: "c2" });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    (rows("alpha.txt") as HTMLElement).focus();
    fireEvent.keyDown(rows("alpha.txt") as Element, { key: "a", ctrlKey: true });
    expect(await screen.findByText(/Selected the items loaded so far: 2/)).toBeTruthy();
  });

  // (d) A cut pasted where the items already are says so, instead of doing nothing.
  it("a cut pasted into the folder the items are in says that nothing moved, and sends nothing", async () => {
    setClip("cut", [node("n1", "alpha.txt", { parent_id: null, folder_id: "fld_aaaaaaaa" })]);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    expect(await screen.findByText("These items are already in this folder, so nothing was moved.")).toBeTruthy();
    expect(api.moveNode).not.toHaveBeenCalled();
    expect(get(clipboard)).toBeNull();
  });

  // (e) The panel's buttons are inset from the bar's top and bottom, and the row keeps its height.
  it("the selection panel keeps its buttons clear of the bar's top and bottom, and the row does not change height", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const bar = screen.getByRole("toolbar", { name: "Folder actions" });
    const rowHeight = bar.style.minHeight;
    fireEvent.click(rows("alpha.txt") as Element);
    const panel = await screen.findByTestId("selection-panel");
    expect(panel.style.height).toBe("48px");
    expect(panel.getAttribute("style")).toContain("padding-block: 4px");
    expect(bar.style.minHeight).toBe(rowHeight);
    expect(rowHeight).toBe("56px");
  });

  it("on a finger a tap on a row's checkbox selects the row (decision 128)", async () => {
    vi.stubGlobal("matchMedia", (query: string) => ({ matches: query === "(pointer: coarse)", media: query, addEventListener() {}, removeEventListener() {} }));
    renderAt(FOLDER);
    const row = (await screen.findByRole("row", { name: "alpha.txt" })) as HTMLElement;
    const box = row.querySelector('input[type="checkbox"]') as HTMLElement;
    expect(box).not.toBeNull();
    // A touch makes its pointer events first, then the mouse's events with the focus, then the click.
    fireEvent.pointerDown(box, { pointerType: "touch" });
    fireEvent.pointerUp(box, { pointerType: "touch" });
    fireEvent.mouseDown(box);
    act(() => box.focus());
    fireEvent.mouseUp(box);
    fireEvent.click(box);
    expect(await screen.findByTestId("selection-panel")).toBeTruthy();
    expect((rows("alpha.txt") as Element).getAttribute("aria-selected")).toBe("true");
  });

  it("on a finger the panel is 52 pixels with 44 pixel buttons inside it, and the row is the same 52", async () => {
    vi.stubGlobal("matchMedia", (query: string) => ({ matches: query === "(pointer: coarse)", media: query, addEventListener() {}, removeEventListener() {} }));
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const bar = screen.getByRole("toolbar", { name: "Folder actions" });
    fireEvent.click(rows("alpha.txt") as Element);
    const panel = await screen.findByTestId("selection-panel");
    expect(panel.style.height).toBe("52px");
    expect(bar.style.minHeight).toBe("52px");
    // The icon buttons are 44 px on a finger by the stylesheet (index.css, icon-btn); here the class is checked.
    expect((within(panel).getByRole("button", { name: "Download" }) as HTMLElement).className).toMatch(/\bicon-btn\b/);
  });
});

// Slice w2: drag to move or copy, and the rubber band. jsdom has no layout, so the places under the pointer and the boxes of
// the rows are stated by each test; the pointer events are real events that the page's own listeners take.
describe("slice w2: drag onto a folder, and the rubber band", () => {
  const pointer = (type: string, x: number, y: number, extra: PointerEventInit = {}) =>
    new PointerEvent(type, { bubbles: true, cancelable: true, button: 0, buttons: type === "pointerup" ? 0 : 1, pointerType: "mouse", isPrimary: true, clientX: x, clientY: y, ...extra });
  const send = (el: Element | Window | Document, type: string, x: number, y: number, extra: PointerEventInit = {}) => {
    act(() => {
      el.dispatchEvent(pointer(type, x, y, extra));
    });
  };
  let under: Element | null = null;
  const dir = (id: string, name: string, extra: Partial<Node> = {}) => node(id, name, { kind: "dir", mime_type: undefined, ...extra });
  const A = { folder_id: "fld_aaaaaaaa" };

  beforeEach(() => {
    localStorage.clear();
    under = null;
    document.elementFromPoint = () => under;
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true }]);
    api.childrenPage.mockResolvedValue({
      items: [dir("d1", "Docs", A), dir("d2", "Pics", A), node("n1", "alpha.txt", A), node("n2", "beta.txt", A), node("n3", "gamma.txt", A)],
      next_cursor: undefined,
    });
    api.statOrNull.mockImplementation(async (id: string) => node(id, `${id}.txt`, { ...A, etag: `fresh-${id}` }));
    api.moveNode.mockResolvedValue(node("n1", "alpha.txt"));
    api.copyNode.mockResolvedValue(node("n9", "alpha.txt"));
  });
  afterEach(() => {
    vi.restoreAllMocks();
    folders.set([]);
  });

  async function ready() {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
  }

  // drag presses an item, moves it over a place (null: over nothing), and returns the place's state before the release.
  function drag(from: string, over: Element | null, extra: PointerEventInit = {}) {
    const row = rows(from) as Element;
    send(row, "pointerdown", 10, 10, extra);
    send(row, "pointermove", 40, 40, extra);
    under = over;
    send(row, "pointermove", 60, 60, extra);
  }
  const release = (extra: PointerEventInit = {}) => send(document.body, "pointerup", 60, 60, extra);

  it("moves an item onto a folder row with its current etag, and shows the folder as taking it", async () => {
    await ready();
    drag("alpha.txt", rows("Docs"));
    expect((rows("Docs") as HTMLElement).dataset.dropState).toBe("ok");
    expect(screen.getByTestId("drag-ghost").textContent).toContain("Move to");
    release();
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(1));
    expect(api.moveNode.mock.calls[0][0]).toBe("n1");
    expect(api.moveNode.mock.calls[0][1]).toMatchObject({ folder: "fld_aaaaaaaa", parent: "d1" });
    expect(api.moveNode.mock.calls[0][2]).toBe("fresh-n1");
    expect(api.copyNode).not.toHaveBeenCalled();
    expect((rows("Docs") as HTMLElement).dataset.dropState).toBeUndefined();
    expect(screen.queryByTestId("drag-ghost")).toBeNull();
  });

  it("copies when Ctrl is held, and the clipboard is left alone", async () => {
    setClip("copy", [node("n2", "beta.txt", A)]);
    await ready();
    drag("alpha.txt", rows("Docs"), { ctrlKey: true });
    expect(screen.getByTestId("drag-ghost").dataset.mode).toBe("copy");
    release({ ctrlKey: true });
    await waitFor(() => expect(api.copyNode).toHaveBeenCalledTimes(1));
    expect(api.moveNode).not.toHaveBeenCalled();
    expect(api.copyNode.mock.calls[0][1]).toMatchObject({ folder: "fld_aaaaaaaa", parent: "d1" });
    expect(get(clipboard)?.nodes.map((n) => n.id)).toEqual(["n2"]);
  });

  it("pressing Ctrl in the middle of a drag turns the move into a copy, and releasing it turns it back", async () => {
    await ready();
    drag("alpha.txt", rows("Docs"));
    expect(screen.getByTestId("drag-ghost").dataset.mode).toBe("move");
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Control", ctrlKey: true, bubbles: true }));
    });
    expect(screen.getByTestId("drag-ghost").dataset.mode).toBe("copy");
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keyup", { key: "Control", bubbles: true }));
    });
    expect(screen.getByTestId("drag-ghost").dataset.mode).toBe("move");
    send(document.body, "pointercancel", 0, 0);
  });

  it("drags the whole selection when the pressed item is in it, and only the item when it is not", async () => {
    await ready();
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(rows("beta.txt") as Element, { ctrlKey: true });
    drag("beta.txt", rows("Docs"));
    release();
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(2));
    expect(api.moveNode.mock.calls.map((c) => c[0]).sort()).toEqual(["n1", "n2"]);
    api.moveNode.mockClear();
    // gamma is not selected: it goes alone, and becomes the selection.
    drag("gamma.txt", rows("Pics"));
    release();
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(1));
    expect(api.moveNode.mock.calls[0][0]).toBe("n3");
  });

  it("shows a place the caller may not add to as refused, says why, and sends nothing when released there", async () => {
    api.childrenPage.mockResolvedValue({ items: [dir("d1", "Docs", { ...A, role: "viewer" }), node("n1", "alpha.txt", A)], next_cursor: undefined });
    await ready();
    drag("alpha.txt", rows("Docs"));
    expect((rows("Docs") as HTMLElement).dataset.dropState).toBe("refused");
    expect(screen.getByTestId("drag-ghost").dataset.state).toBe("refused");
    expect(screen.getByTestId("drag-ghost").textContent).toContain("You cannot add items to this folder.");
    release();
    expect(await screen.findByText("You cannot add items to this folder.")).toBeTruthy();
    expect(api.moveNode).not.toHaveBeenCalled();
    expect(api.copyNode).not.toHaveBeenCalled();
  });

  it("refuses a folder dropped on itself, with a reason", async () => {
    await ready();
    drag("Docs", rows("Docs"));
    expect((rows("Docs") as HTMLElement).dataset.dropState).toBe("refused");
    release();
    expect(await screen.findByText("A folder cannot go into itself.")).toBeTruthy();
    expect(api.moveNode).not.toHaveBeenCalled();
  });

  it("takes the move permission from the toolbar's own action: items the caller cannot move are refused unless copied", async () => {
    api.childrenPage.mockResolvedValue({ items: [dir("d1", "Docs", A), node("n1", "alpha.txt", { ...A, role: "viewer" })], next_cursor: undefined });
    await ready();
    drag("alpha.txt", rows("Docs"));
    expect((rows("Docs") as HTMLElement).dataset.dropState).toBe("refused");
    expect(screen.getByTestId("drag-ghost").textContent).toContain("Hold Ctrl to copy them");
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Control", ctrlKey: true, bubbles: true }));
    });
    expect((rows("Docs") as HTMLElement).dataset.dropState).toBe("ok");
    send(document.body, "pointercancel", 0, 0);
  });

  it("does nothing when released over no folder, and Escape cancels a drag", async () => {
    await ready();
    drag("alpha.txt", null);
    release();
    expect(api.moveNode).not.toHaveBeenCalled();
    drag("alpha.txt", rows("Docs"));
    act(() => {
      window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
    });
    expect(screen.queryByTestId("drag-ghost")).toBeNull();
    expect((rows("Docs") as HTMLElement).dataset.dropState).toBeUndefined();
    release();
    expect(api.moveNode).not.toHaveBeenCalled();
  });

  it("a press that hardly moves is a click: it starts no drag", async () => {
    await ready();
    const row = rows("alpha.txt") as Element;
    send(row, "pointerdown", 10, 10);
    send(row, "pointermove", 12, 11);
    expect(screen.queryByTestId("drag-ghost")).toBeNull();
    release();
  });

  it("a finger does not start a drag", async () => {
    await ready();
    const row = rows("alpha.txt") as Element;
    send(row, "pointerdown", 10, 10, { pointerType: "touch" });
    send(row, "pointermove", 80, 80, { pointerType: "touch" });
    expect(screen.queryByTestId("drag-ghost")).toBeNull();
  });

  it("a pen does not start a drag either: gestures are for the mouse, and a pen uses Move to and the menu", async () => {
    await ready();
    const row = rows("alpha.txt") as Element;
    send(row, "pointerdown", 10, 10, { pointerType: "pen" });
    send(row, "pointermove", 80, 80, { pointerType: "pen" });
    expect(screen.queryByTestId("drag-ghost")).toBeNull();
    send(document.body, "pointerup", 80, 80, { pointerType: "pen" });
    expect(api.moveNode).not.toHaveBeenCalled();
  });

  it("Escape is taken only while a drag is under way: a press that has not moved leaves it to the page", async () => {
    await ready();
    const heard = vi.fn();
    document.body.addEventListener("keydown", heard);
    try {
      const row = rows("alpha.txt") as Element;
      send(row, "pointerdown", 10, 10);
      act(() => {
        document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
      });
      expect(heard).toHaveBeenCalledTimes(1);
      send(document.body, "pointerup", 10, 10);
      // Under way, the same key ends the drag and goes no further.
      drag("alpha.txt", rows("Docs"));
      act(() => {
        document.body.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
      });
      expect(heard).toHaveBeenCalledTimes(1);
      expect(screen.queryByTestId("drag-ghost")).toBeNull();
    } finally {
      document.body.removeEventListener("keydown", heard);
    }
  });

  it("an item that someone else moved into the place meanwhile is not sent, and no conflict is asked", async () => {
    api.statOrNull.mockImplementation(async (id: string) => node(id, `${id}.txt`, { ...A, parent_id: "d1", etag: `fresh-${id}` }));
    await ready();
    drag("alpha.txt", rows("Docs"));
    release();
    await waitFor(() => expect(api.statOrNull).toHaveBeenCalled());
    await waitFor(() => expect(api.childrenPage.mock.calls.length).toBeGreaterThan(1));
    expect(api.moveNode).not.toHaveBeenCalled();
    expect(document.querySelector('[role="dialog"]')).toBeNull();
    expect(screen.queryByText(/already exists|Replace/)).toBeNull();
  });

  it("the click that ends a drag does not select or open anything", async () => {
    await ready();
    drag("alpha.txt", null);
    release();
    // The browser's click that follows the release lands on the row under the pointer; it must not select it.
    const gamma = rows("gamma.txt") as Element;
    fireEvent.click(gamma);
    expect(gamma.getAttribute("aria-selected")).toBe("false");
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true");
  });

  it("files dragged in from the desktop still upload", async () => {
    await ready();
    const file = new File(["x"], "desk.txt", { type: "text/plain" });
    const dt = { types: ["Files"], files: [file], items: [], dropEffect: "none" };
    const section = document.querySelector("section.browser") as HTMLElement;
    fireEvent.dragEnter(section, { dataTransfer: dt });
    fireEvent.dragOver(section, { dataTransfer: dt });
    fireEvent.drop(section, { dataTransfer: dt });
    await waitFor(() => expect(uploadsMock.add).toHaveBeenCalled());
  });

  // ---- the rubber band ----
  function boxes(headBottom = 0) {
    vi.spyOn(Element.prototype, "getBoundingClientRect").mockImplementation(function (this: Element) {
      const id = (this as HTMLElement).dataset?.id;
      if (this.tagName === "THEAD") return new DOMRect(0, 0, 600, headBottom);
      const order = ["d1", "d2", "n1", "n2", "n3"];
      if (id && order.includes(id)) {
        const top = 100 + order.indexOf(id) * 40;
        return new DOMRect(0, top, 600, 40);
      }
      if (this.classList?.contains("browser")) return new DOMRect(0, 0, 700, 800);
      return new DOMRect(0, 0, 0, 0);
    });
  }
  const section = () => document.querySelector("section.browser") as HTMLElement;

  it("draws a band on empty space and selects the rows it touches", async () => {
    boxes();
    await ready();
    send(section(), "pointerdown", 650, 90);
    send(section(), "pointermove", 300, 135);
    send(section(), "pointermove", 300, 215);
    expect(screen.getByTestId("rubber-band")).toBeTruthy();
    // 100-140 d1, 140-180 d2, 180-220 n1; the band spans 90 to 215.
    await waitFor(() => expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true"));
    expect(rows("Docs")?.getAttribute("aria-selected")).toBe("true");
    expect(rows("beta.txt")?.getAttribute("aria-selected")).toBe("false");
    send(document.body, "pointerup", 650, 215);
    expect(screen.queryByTestId("rubber-band")).toBeNull();
    expect(screen.getByText("3 selected")).toBeTruthy();
  });

  it("stops the band at the pinned header: it is not drawn over it, and a row hidden under it is not taken", async () => {
    boxes(150);
    await ready();
    // Pressed on empty space beside the row n1 (180 to 220), dragged up to 120, which is under the header that ends at 150.
    send(section(), "pointerdown", 650, 190);
    send(section(), "pointermove", 300, 160);
    send(section(), "pointermove", 300, 120);
    // d1 (100 to 140) lies wholly under the header; d2 (140 to 180) shows below it, and n1 is where the band began.
    await waitFor(() => expect(rows("Pics")?.getAttribute("aria-selected")).toBe("true"));
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true");
    expect(rows("Docs")?.getAttribute("aria-selected")).toBe("false");
    const band = screen.getByTestId("rubber-band");
    expect(band.style.insetBlockStart).toBe("150px");
    expect(band.style.blockSize).toBe("40px");
    send(document.body, "pointerup", 300, 120);
  });

  it("works whichever way the band is drawn, and shrinks the selection when it shrinks", async () => {
    boxes();
    await ready();
    send(section(), "pointerdown", 650, 300);
    send(section(), "pointermove", 300, 250);
    send(section(), "pointermove", 300, 190);
    await waitFor(() => expect(screen.getByText("3 selected")).toBeTruthy());
    send(section(), "pointermove", 300, 250);
    await waitFor(() => expect(screen.getByText("2 selected")).toBeTruthy());
    send(document.body, "pointerup", 650, 250);
  });

  it("adds to the selection with Ctrl, and replaces it without", async () => {
    boxes();
    await ready();
    fireEvent.click(rows("gamma.txt") as Element);
    send(section(), "pointerdown", 650, 90, { ctrlKey: true });
    send(section(), "pointermove", 300, 130, { ctrlKey: true });
    send(section(), "pointermove", 300, 150, { ctrlKey: true });
    await waitFor(() => expect(screen.getByText("3 selected")).toBeTruthy());
    expect(rows("gamma.txt")?.getAttribute("aria-selected")).toBe("true");
    send(document.body, "pointerup", 650, 150, { ctrlKey: true });
    // Without Ctrl the band replaces what was selected.
    send(section(), "pointerdown", 650, 90);
    send(section(), "pointermove", 300, 130);
    send(section(), "pointermove", 300, 150);
    await waitFor(() => expect(screen.getByText("2 selected")).toBeTruthy());
    expect(rows("gamma.txt")?.getAttribute("aria-selected")).toBe("false");
    send(document.body, "pointerup", 650, 150);
  });

  it("does not start on a button, a checkbox, a row, or a finger, and not on the scroll bar's side of the window", async () => {
    boxes();
    await ready();
    const toolbarButton = screen.getByTestId("view-toggle");
    send(toolbarButton, "pointerdown", 650, 90);
    send(toolbarButton, "pointermove", 300, 215);
    expect(screen.queryByTestId("rubber-band")).toBeNull();
    send(document.body, "pointerup", 650, 215);
    const box = screen.getAllByRole("checkbox")[1];
    send(box, "pointerdown", 650, 90);
    send(box, "pointermove", 300, 215);
    expect(screen.queryByTestId("rubber-band")).toBeNull();
    send(document.body, "pointerup", 650, 215);
    send(section(), "pointerdown", 650, 90, { pointerType: "touch" });
    send(section(), "pointermove", 300, 215, { pointerType: "touch" });
    expect(screen.queryByTestId("rubber-band")).toBeNull();
    // A press on a row is a drag of that row, not a band.
    const row = rows("beta.txt") as Element;
    send(row, "pointerdown", 20, 220);
    send(row, "pointermove", 20, 300);
    expect(screen.queryByTestId("rubber-band")).toBeNull();
    send(document.body, "pointercancel", 0, 0);
  });

  it("a plain click on empty space clears the selection, and Ctrl+click does not", async () => {
    await ready();
    fireEvent.click(rows("alpha.txt") as Element);
    send(section(), "pointerdown", 650, 700, { ctrlKey: true });
    send(document.body, "pointerup", 650, 700, { ctrlKey: true });
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true");
    send(section(), "pointerdown", 650, 700);
    send(document.body, "pointerup", 650, 700);
    await waitFor(() => expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("false"));
  });

  it("a press that only dismisses an open menu starts no band", async () => {
    boxes();
    await ready();
    fireEvent.contextMenu(rows("alpha.txt") as Element, { clientX: 20, clientY: 220 });
    await screen.findByRole("menu");
    send(section(), "pointerdown", 650, 90);
    send(section(), "pointermove", 300, 215);
    expect(screen.queryByTestId("rubber-band")).toBeNull();
    send(document.body, "pointercancel", 0, 0);
  });
});

// My files (decision 53): the storage notice shows in the person's own folder only, below the toolbar, and a person with
// no personal folder gets the not-found page, not a blank one.
describe("My files: the storage notice and a person with no folder", () => {
  const OWN = "/files/fld_mineaaaa";
  const MINE = "fld_mineaaaa";
  const near = (used: number) => ({ used_bytes: used, reserved_bytes: 0, quota_bytes: 1000, available_bytes: 1000 - used });

  beforeEach(() => api.getMe.mockResolvedValue({ personal_folder_id: MINE }));
  afterEach(() => myStorage.set(null));

  it("shows the notice in the person's own folder at 80%, below the toolbar", async () => {
    myStorage.set(near(900));
    renderAt(OWN);
    const notice = await screen.findByText(translate("en", "home.storageNotice", { percent: "90" }));
    const toolbar = await screen.findByRole("toolbar");
    // The toolbar comes first in the page: the notice never moves it.
    expect(toolbar.compareDocumentPosition(notice) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  });

  it("does not show the notice below 80%", async () => {
    myStorage.set(near(500));
    renderAt(OWN);
    await screen.findByRole("toolbar");
    expect(screen.queryByRole("meter")).toBeNull();
    expect(screen.queryByText(translate("en", "home.storageNotice", { percent: "50" }))).toBeNull();
  });

  it("does not show the notice in a team space", async () => {
    myStorage.set(near(900));
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(screen.queryByText(translate("en", "home.storageNotice", { percent: "90" }))).toBeNull();
  });

  it("a person with no personal folder sees the not-found page, not a blank one", async () => {
    api.getMe.mockResolvedValue({ personal_folder_id: null });
    renderAt("/files");
    expect(await screen.findByText(translate("en", "app.notFound.title"))).toBeTruthy();
    expect(screen.queryByRole("toolbar")).toBeNull();
  });

  describe("the way from /files to the person's own folder", () => {
    const before = window.location.pathname;
    afterEach(() => window.history.replaceState(null, "", before));

    // renderWaitingForMe opens /files with the read of the person still going on, as it is right after signing in.
    function renderWaitingForMe() {
      window.history.replaceState(null, "", "/files");
      let answer: (me: { personal_folder_id: string }) => void = () => undefined;
      api.getMe.mockReturnValue(new Promise((resolve) => { answer = resolve; }));
      const { qc } = renderAt("/files");
      return { qc, answer };
    }

    it("goes to the folder once the person is read", async () => {
      const { answer } = renderWaitingForMe();
      answer({ personal_folder_id: MINE });
      await waitFor(() => expect(window.location.pathname).toBe(OWN));
    });

    it("goes on to the folder when My files is pressed again during the move", async () => {
      window.history.replaceState(null, "", "/files");
      api.getMe.mockResolvedValue({ personal_folder_id: MINE });
      const { router } = renderAt("/files");
      await waitFor(() => expect(window.location.pathname).toBe(OWN));
      // The press lands before the page has changed: the address is /files again, and the place is still the redirect.
      window.history.replaceState(null, "", "/files");
      await act(async () => { await router.navigate("/files", { replace: true }); });
      await waitFor(() => expect(window.location.pathname).toBe(OWN));
    });

    it("leaves alone a person who already went elsewhere", async () => {
      const { qc, answer } = renderWaitingForMe();
      // The person clicks Shared with me before the read ends: the address has moved, the page has not yet.
      window.history.pushState(null, "", "/shared");
      answer({ personal_folder_id: MINE });
      await waitFor(() => expect(qc.isFetching()).toBe(0));
      await act(async () => { await new Promise((resolve) => setTimeout(resolve, 0)); });
      expect(window.location.pathname).toBe("/shared");
    });
  });
});

describe("Play media", () => {
  const video = (id: string, name: string) => node(id, name, { mime_type: "video/mp4" });
  const audio = (id: string, name: string) => node(id, name, { mime_type: "audio/ogg" });
  const folderRow = (id: string, name: string) => node(id, name, { kind: "dir", mime_type: "" });
  // The listing of the place itself, and of the folder pressed, are told apart by the directory asked for.
  const listing = (here: unknown[], inside: (cursor?: string) => unknown) =>
    api.childrenPage.mockImplementation(async (_folder: string, dir: string | null, cursor?: string) =>
      dir === "d1" ? inside(cursor) : { items: here, next_cursor: undefined },
    );
  const pressFolder = async () => {
    await screen.findByRole("row", { name: "Music" });
    fireEvent.contextMenu(rows("Music") as Element, { clientX: 40, clientY: 50 });
    fireEvent.click(await screen.findByRole("menuitem", { name: "Play media" }));
  };
  type Seed = { start: Node; folder: { loaded: Node[] }; selection?: Node[] };
  const seed = () => started.mock.calls[0][0] as Seed;

  it("plays the folder on screen from its first audio or video file", async () => {
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), video("v1", "one.mp4"), audio("a1", "two.ogg")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "one.mp4" });
    fireEvent.click(within(screen.getByRole("toolbar", { name: "Folder actions" })).getByRole("button", { name: "Play media" }));
    await waitFor(() => expect(started).toHaveBeenCalledTimes(1));
    expect(seed().start.id).toBe("v1");
    expect(seed().folder.loaded.map((n) => n.id)).toEqual(["v1", "a1"]);
    expect(seed().selection).toBeUndefined();
  });

  it("offers no button in a folder with no audio or video, and none over a selection", async () => {
    const first = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(screen.queryByRole("button", { name: "Play media" })).toBeNull();
    first.unmount();
    api.childrenPage.mockResolvedValue({ items: [video("v1", "one.mp4")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("button", { name: "Play media" });
    fireEvent.click(rows("one.mp4") as Element);
    await waitFor(() => expect(screen.queryByRole("button", { name: "Play media" })).toBeNull());
  });

  it("is in the menu of the empty space when the folder has audio or video", async () => {
    api.childrenPage.mockResolvedValue({ items: [video("v1", "one.mp4")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "one.mp4" });
    fireEvent.contextMenu(document.querySelector(".browser") as Element, { clientX: 40, clientY: 50 });
    fireEvent.click(await screen.findByRole("menuitem", { name: "Play media" }));
    await waitFor(() => expect(started).toHaveBeenCalledTimes(1));
    expect(seed().start.id).toBe("v1");
  });

  it("plays a folder from its right-click menu, reading that folder", async () => {
    listing([folderRow("d1", "Music")], () => ({ items: [node("t1", "cover.txt"), audio("a1", "one.ogg"), video("v1", "two.mp4")], next_cursor: undefined }));
    renderAt(FOLDER);
    await pressFolder();
    await waitFor(() => expect(started).toHaveBeenCalledTimes(1));
    expect(seed().start.id).toBe("a1");
    expect(seed().folder.loaded.map((n) => n.id)).toEqual(["a1", "v1"]);
    expect(api.childrenPage.mock.calls.some((c) => c[1] === "d1")).toBe(true);
  });

  it("reads on past pages with no audio or video, and asks each page with the listing's sort", async () => {
    listing([folderRow("d1", "Music")], (cursor) =>
      cursor === undefined ? { items: [node("t1", "a.txt")], next_cursor: "c2" } : { items: [audio("a1", "late.ogg")], next_cursor: undefined },
    );
    renderAt(FOLDER);
    await pressFolder();
    await waitFor(() => expect(started).toHaveBeenCalledTimes(1));
    expect(seed().start.id).toBe("a1");
    const reads = api.childrenPage.mock.calls.filter((c) => c[1] === "d1");
    expect(reads.length).toBe(2);
    expect(reads[1][3]).toEqual(reads[0][3]);
  });

  it("says so when the folder has no audio or video, and plays nothing", async () => {
    listing([folderRow("d1", "Music")], () => ({ items: [node("t1", "a.txt")], next_cursor: undefined }));
    renderAt(FOLDER);
    await pressFolder();
    expect(await screen.findByText("No audio or video in this folder.")).toBeTruthy();
    expect(started).not.toHaveBeenCalled();
  });

  it("says so when the folder cannot be read, on the first page and on a later one", async () => {
    listing([folderRow("d1", "Music")], () => {
      throw new ApiError(500, "internal", {}, 0);
    });
    const first = renderAt(FOLDER);
    await pressFolder();
    await waitFor(() => expect(document.querySelector(".ant-message-error")).not.toBeNull());
    expect(started).not.toHaveBeenCalled();
    first.unmount();
    document.querySelector(".ant-message-error")?.remove();
    listing([folderRow("d1", "Music")], (cursor) => {
      if (cursor === undefined) return { items: [node("t1", "a.txt")], next_cursor: "c2" };
      throw new ApiError(500, "internal", {}, 0);
    });
    renderAt(FOLDER);
    await pressFolder();
    await waitFor(() => expect(document.querySelector(".ant-message-error")).not.toBeNull());
    expect(started).not.toHaveBeenCalled();
    expect(screen.queryByText("No audio or video in this folder.")).toBeNull();
  });

  it("drops the answer when the person has moved on", async () => {
    let answer: (v: unknown) => void = () => {};
    listing([folderRow("d1", "Music")], () => new Promise((res) => (answer = res)));
    const { router } = renderAt(FOLDER);
    await pressFolder();
    await waitFor(() => expect(api.childrenPage.mock.calls.some((c) => c[1] === "d1")).toBe(true));
    // Inside act, so the move has reached the page before the answer comes.
    await act(async () => {
      await router.navigate("/files/fld_bbbbbbbb");
    });
    await act(async () => {
      answer({ items: [audio("a1", "one.ogg")], next_cursor: undefined });
      await new Promise((res) => setTimeout(res, 50));
    });
    expect(started).not.toHaveBeenCalled();
  });

  it("shows the space's no-download rule on the files of later pages too", async () => {
    place.noDownload = true;
    api.childrenPage.mockImplementation(async (_folder: string, _dir: string | null, cursor?: string) =>
      cursor === undefined ? { items: [video("v1", "one.mp4")], next_cursor: "c2" } : { items: [audio("a2", "two.ogg")], next_cursor: undefined },
    );
    renderAt(FOLDER);
    fireEvent.click(await screen.findByRole("button", { name: "Play media" }));
    await waitFor(() => expect(started).toHaveBeenCalledTimes(1));
    const pager = seed().folder as unknown as { more: () => Promise<unknown>; loaded: Node[] };
    await pager.more();
    expect(pager.loaded.map((n) => [n.id, n.no_download])).toEqual([
      ["v1", true],
      ["a2", true],
    ]);
  });

  it("a press on the toolbar's button wins over a folder still being read", async () => {
    let answer: (v: unknown) => void = () => {};
    listing([folderRow("d1", "Music"), video("v1", "one.mp4")], () => new Promise((res) => (answer = res)));
    renderAt(FOLDER);
    await pressFolder();
    await waitFor(() => expect(api.childrenPage.mock.calls.some((c) => c[1] === "d1")).toBe(true));
    // The menu has selected the folder; clearing it brings the toolbar's button back.
    fireEvent.click(await screen.findByTestId("selection-clear"));
    fireEvent.click(await screen.findByRole("button", { name: "Play media" }));
    await waitFor(() => expect(started).toHaveBeenCalledTimes(1));
    await act(async () => {
      answer({ items: [audio("a1", "late.ogg")], next_cursor: undefined });
      await new Promise((res) => setTimeout(res, 50));
    });
    expect(started).toHaveBeenCalledTimes(1);
    expect(seed().start.id).toBe("v1");
  });

  it("is not offered for a selected file", async () => {
    api.childrenPage.mockResolvedValue({ items: [video("v1", "one.mp4"), folderRow("d1", "Music")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "one.mp4" });
    fireEvent.contextMenu(rows("one.mp4") as Element, { clientX: 40, clientY: 50 });
    await menuEntry("Details", "Information");
    expect(screen.queryByRole("menuitem", { name: "Play media" })).toBeNull();
  });
});

// The toolbar's New is the sidebar's where the sidebar is shown (decision 66), and the toolbar keeps its own where it is a drawer.
describe("the toolbar's New (decision 66)", () => {
  const original = window.matchMedia;
  afterEach(() => {
    window.matchMedia = original;
  });

  it("is not on the toolbar where the sidebar holds it", async () => {
    window.matchMedia = (query: string) => ({ ...original(query), matches: query.includes("min-width") }) as MediaQueryList;
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(within(screen.getByRole("toolbar")).queryByRole("button", { name: "New" })).toBeNull();
  });

  it("is on the toolbar in the drawer layout, a phone's", async () => {
    window.matchMedia = (query: string) => ({ ...original(query), matches: false }) as MediaQueryList;
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(within(screen.getByRole("toolbar")).getByRole("button", { name: "New" })).toBeTruthy();
  });
});

// The row end and the grouped menu (decision 70): the icons of a row are the menu's own actions, with the same permissions, and
// the menu groups its entries as Drive does.
// Review fixes 5, 8, 9, 12 and 13 (decision 70): a submenu scrolls like the root menu; a menu entry's name is its text alone, with the
// shortcut named by aria-keyshortcuts; Open is in the first group for a file that Enter opens; Star and Unstar, from the hover icon and
// from the menu, call the star functions.
describe("review fixes: submenus, shortcut names, Open for Enter, and the star calls", () => {
  it("a submenu has the root menu's height limit and scrolls (review fix 5)", async () => {
    place.role = "editor";
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.contextMenu(rows("alpha.txt") as Element, { clientX: 50, clientY: 60 });
    await menuEntry("Cut", "Organize");
    const popup = document.querySelector(".ant-dropdown-menu-submenu-popup") as HTMLElement | null;
    expect(popup).not.toBeNull();
    expect(popup?.style.overflowY).toBe("auto");
    expect(popup?.style.maxBlockSize).toBe("calc(100dvh - 16px)");
  });

  it("a menu entry's name is its text alone, its shortcut is hidden from a screen reader, and aria-keyshortcuts names it (review fix 9)", async () => {
    place.role = "editor";
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.contextMenu(rows("alpha.txt") as Element, { clientX: 50, clientY: 60 });
    const cut = await menuEntry("Cut", "Organize");
    expect(cut.getAttribute("aria-keyshortcuts")).toBe("Control+X");
    expect(cut.querySelector(".menu-hint")?.getAttribute("aria-hidden")).toBe("true");
  });

  it("a file that Enter opens, and that has no preview, has Open in the first group with the Enter hint (review fix 12)", async () => {
    place.role = "editor";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "archive.zip", { mime_type: "application/zip" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "archive.zip" });
    fireEvent.contextMenu(rows("archive.zip") as Element, { clientX: 50, clientY: 60 });
    const open = await menuEntry("Open");
    expect(open.querySelector(".menu-hint")?.getAttribute("data-hint")).toBe("Enter");
  });

  it("the hover icon Star calls starNode, and the menu's Unstar on a starred file calls unstarNode (review fix 13)", async () => {
    place.role = "editor";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "beta.txt", { starred: false }), node("n2", "gamma.txt", { starred: true })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "beta.txt" });
    fireEvent.click(within(rows("beta.txt") as HTMLElement).getByRole("button", { name: "Star" }));
    await waitFor(() => expect(starNode).toHaveBeenCalledWith("n1"));
    fireEvent.contextMenu(rows("gamma.txt") as Element, { clientX: 50, clientY: 60 });
    fireEvent.click(await menuEntry("Unstar", "Organize"));
    await waitFor(() => expect(unstarNode).toHaveBeenCalledWith("n2"));
  });
});

// Review fix 1 (decision 70): Enter or Space on a row's ⋮ opens the item's menu with the focus in it; the arrows move in the menu and
// not the selection; Escape gives the focus back to the ⋮.
describe("review fix 1: the keyboard opens a row's ⋮ menu", () => {
  it("Enter on a row's ⋮ opens its menu with the focus on the first item, arrows move in the menu and not the selection, and Escape gives the focus back to the ⋮", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const more = within(rows("alpha.txt") as HTMLElement).getByRole("button", { name: "More actions for alpha.txt" });
    more.focus();
    // Enter on a button is a click with no pointer: its detail is 0.
    fireEvent.click(more, { detail: 0 });
    const menu = await screen.findByRole("menu");
    await waitFor(() => expect(menu.contains(document.activeElement)).toBe(true));
    expect((document.activeElement as HTMLElement).getAttribute("role")).toBe("menuitem");
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(document.activeElement as Element, { key: "ArrowDown", keyCode: 40 });
    await waitFor(() => expect(menu.contains(document.activeElement)).toBe(true));
    expect(rows("beta.txt")?.getAttribute("aria-selected")).toBe("false");
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("true");
    fireEvent.keyDown(document.activeElement as Element, { key: "Escape", keyCode: 27 });
    await waitFor(() => expect(document.activeElement).toBe(more));
  });

  it("a mouse click on a row's ⋮ does not move the focus into the menu", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const more = within(rows("alpha.txt") as HTMLElement).getByRole("button", { name: "More actions for alpha.txt" });
    fireEvent.click(more, { detail: 1 });
    await screen.findByRole("menu");
    await new Promise((r) => setTimeout(r, 50));
    expect(screen.getByRole("menu").contains(document.activeElement)).toBe(false);
  });
});

describe("the row end and the grouped menu (decision 70)", () => {
  const folderRow = (id: string, name: string) => node(id, name, { kind: "dir", mime_type: "" });

  it("an editor's row has the Rename icon, and a viewer's row has none", async () => {
    place.role = "editor";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "editor" })], next_cursor: undefined });
    renderAt(FOLDER);
    expect(within(await screen.findByRole("row", { name: "alpha.txt" })).getByRole("button", { name: "Rename" })).toBeTruthy();
    cleanup();
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "viewer" })], next_cursor: undefined });
    renderAt(FOLDER);
    expect(within(await screen.findByRole("row", { name: "alpha.txt" })).queryByRole("button", { name: "Rename" })).toBeNull();
  });

  it("the right-click menu of a file lists its first group, then the submenus, and Move to trash last", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.contextMenu(rows("alpha.txt") as Element, { clientX: 50, clientY: 60 });
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByRole("menuitem", { name: "Rename" })).toBeTruthy();
    const titles = Array.from(menu.querySelectorAll(".ant-dropdown-menu-submenu-title")).map((x) => x.textContent?.trim());
    expect(titles).toEqual(["Share", "Organize", "Information"]);
    expect(within(menu).queryByRole("menuitem", { name: "Move to…" })).toBeNull();
    expect(within(menu).getByRole("menuitem", { name: "Move to trash" })).toBeTruthy();
  });

  it("an entry in Organize shows its keyboard shortcut at the right, as Ctrl on a Windows or Linux browser", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.contextMenu(rows("alpha.txt") as Element, { clientX: 50, clientY: 60 });
    await menuEntry("Cut", "Organize");
    const cut = screen.getByRole("menuitem", { name: "Cut" });
    expect(cut.querySelector(".menu-hint")?.getAttribute("data-hint")).toBe("Ctrl+X");
  });

  it("a starred file's menu offers Unstar in Organize", async () => {
    place.role = "editor";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { starred: true })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.contextMenu(rows("alpha.txt") as Element, { clientX: 50, clientY: 60 });
    expect(await menuEntry("Unstar", "Organize")).toBeTruthy();
  });

  it("an unstarred file's menu offers Star in Organize, and its row shows no star mark", async () => {
    place.role = "editor";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "beta.txt", { starred: false })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "beta.txt" });
    expect(rows("beta.txt")?.textContent).not.toContain("Starred");
    fireEvent.contextMenu(rows("beta.txt") as Element, { clientX: 50, clientY: 60 });
    expect(await menuEntry("Star", "Organize")).toBeTruthy();
  });

  it("a folder's menu opens it first", async () => {
    api.childrenPage.mockResolvedValue({ items: [folderRow("d1", "Music")], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "Music" });
    fireEvent.contextMenu(rows("Music") as Element, { clientX: 50, clientY: 60 });
    expect(await screen.findByRole("menuitem", { name: "Open" })).toBeTruthy();
  });
});

// Slice fu3b (decision 77, item 2): the previous listing stays on screen, marked busy, until the next folder is read. A place's
// first read is the only one that shows nothing, and a failed read still shows its error.
describe("the list while the next folder is read (fu3b)", () => {
  const SUB = `${FOLDER}/nod_bbbbbbbb`;
  const busyFrame = () => document.querySelector(".files-list")?.closest("[aria-busy]") ?? null;

  it("keeps the previous rows, marked busy and inert, until the next folder is read, then swaps", async () => {
    let answer!: (page: { items: Node[]; next_cursor: undefined }) => void;
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? new Promise((resolve) => (answer = resolve)) : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", undefined, expect.anything(), expect.anything()));
    // The old rows are still there: no frame without a listing. Assistive technology reads the area as busy, and its rows are inert.
    expect(rows("alpha.txt")).not.toBeNull();
    expect(busyFrame()?.getAttribute("aria-busy")).toBe("true");
    expect(busyFrame()?.closest("[inert]")).not.toBeNull();
    // The rows are present but not actionable: Ctrl+A selects none of them (fu3b).
    fireEvent.keyDown(document.body, { key: "a", ctrlKey: true });
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("false");
    // The change is announced to assistive technology while the rows are kept (fu3b).
    expect(screen.getByText("Loading the folder").closest('[role="status"]')).not.toBeNull();
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
    expect(rows("alpha.txt")).toBeNull();
    expect(busyFrame()).toBeNull();
  });

  it("keeps the page while the next place is looked up: no loading notice replaces the listing (fu3b)", async () => {
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? { items: [node("n3", "gamma.txt")], next_cursor: undefined } : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    const real = vi.mocked(locate).getMockImplementation()!;
    let release!: () => void;
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    // The next place's lookup (the first one after this) waits until the test releases it.
    vi.mocked(locate).mockImplementationOnce(
      (folder: string, dirID: string | null, list: FolderAccess[]) =>
        new Promise((resolve) => {
          release = () => resolve(real(folder, dirID, list));
        }),
    );
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(vi.mocked(locate)).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", expect.anything()));
    // The load is announced while the place is looked up, and the rows stay (fu3b).
    expect(screen.getByText("Loading the folder").closest('[role="status"]')).not.toBeNull();
    expect(rows("alpha.txt")).not.toBeNull();
    expect(busyFrame()?.closest("[inert]")).not.toBeNull();
    await act(async () => {
      release();
    });
    await screen.findByRole("row", { name: "gamma.txt" });
    expect(busyFrame()).toBeNull();
  });

  it("does not start an upload into the folder shown before, while the next place is read (fu3b)", async () => {
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? { items: [node("n3", "gamma.txt")], next_cursor: undefined } : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    const real = vi.mocked(locate).getMockImplementation()!;
    let release!: () => void;
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    vi.mocked(locate).mockImplementationOnce(
      (folder: string, dirID: string | null, list: FolderAccess[]) =>
        new Promise((resolve) => {
          release = () => resolve(real(folder, dirID, list));
        }),
    );
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(vi.mocked(locate)).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", expect.anything()));
    // The page still holds the folder it showed before, so a file dropped now would land there: it is refused.
    const file = new File(["x"], "late.txt", { type: "text/plain" });
    const dt = { types: ["Files"], files: [file], items: [], dropEffect: "none" };
    const section = document.querySelector("section.browser") as HTMLElement;
    fireEvent.dragOver(section, { dataTransfer: dt });
    fireEvent.drop(section, { dataTransfer: dt });
    // The drop reads the files before it queues them, so the page is given time to queue one if it would.
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(uploadsMock.add).not.toHaveBeenCalled();
    await act(async () => {
      release();
    });
    await screen.findByRole("row", { name: "gamma.txt" });
    // The same drop, once the place is settled, is queued: the refusal above was the wait, not a dead drop.
    fireEvent.drop(section, { dataTransfer: dt });
    await waitFor(() => expect(uploadsMock.add).toHaveBeenCalled());
  });

  // Review fixes of fu3b (decision 77): while the rows on screen are the folder left behind, nothing acts on them.
  it("Ctrl+A and Ctrl+X during the load select and cut nothing of the folder left, and a paste after it moves nothing of that folder (fu3b)", async () => {
    let answer!: (page: { items: Node[]; next_cursor: undefined }) => void;
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? new Promise((resolve) => (answer = resolve)) : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    api.moveNode.mockResolvedValue(node("n1", "alpha.txt"));
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", undefined, expect.anything(), expect.anything()));
    // The change clears the selection made in the folder left. Ctrl+A selects none of the rows shown.
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("false");
    fireEvent.keyDown(document.body, { key: "a", ctrlKey: true });
    expect(rows("alpha.txt")?.getAttribute("aria-selected")).toBe("false");
    // A row of the folder left is picked while its rows are shown (jsdom does not apply inert to a click): Ctrl+X cuts nothing.
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    expect((rows("alpha.txt") as HTMLElement).style.opacity).not.toBe("0.5");
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(api.moveNode).not.toHaveBeenCalled();
  });

  it("Ctrl+V during the load moves nothing, and the same paste after it does (fu3b)", async () => {
    let answer!: (page: { items: Node[]; next_cursor: undefined }) => void;
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? new Promise((resolve) => (answer = resolve)) : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    api.moveNode.mockResolvedValue(node("n1", "alpha.txt"));
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", undefined, expect.anything(), expect.anything()));
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(api.moveNode).not.toHaveBeenCalled();
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(1));
  });

  it("Ctrl+V during the load says the folder is still opening, once, and moves nothing (fu4, item 1)", async () => {
    let answer!: (page: { items: Node[]; next_cursor: undefined }) => void;
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? new Promise((resolve) => (answer = resolve)) : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    api.moveNode.mockResolvedValue(node("n1", "alpha.txt"));
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", undefined, expect.anything(), expect.anything()));
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(busyNotices()).toBe(1));
    // One notice for the busy period, not one per key.
    expect(busyNotices()).toBe(1);
    expect(api.moveNode).not.toHaveBeenCalled();
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  // fu4 review: the notice is read by assistive technology, and only a key that would have done something says it.
  const BUSY_TEXT = "The folder is still opening. Try again in a moment.";
  const busyNotices = () => [...document.querySelectorAll(".ant-message-notice")].filter((n) => n.textContent?.includes(BUSY_TEXT)).length;
  const liveHasNotice = () => [...document.querySelectorAll('[aria-live="polite"]')].some((n) => n.textContent?.includes(BUSY_TEXT));
  // busyFolder opens the folder whose listing is held, and returns once the page is busy.
  // With clip, alpha.txt is cut first, so something is on the clipboard when the page is busy.
  async function busyFolder({ clip = false } = {}) {
    let answer!: (page: { items: Node[]; next_cursor: undefined }) => void;
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? new Promise((resolve) => (answer = resolve)) : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    api.moveNode.mockResolvedValue(node("n1", "alpha.txt"));
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    if (clip) {
      fireEvent.click(rows("alpha.txt") as Element);
      fireEvent.keyDown(document.body, { key: "x", ctrlKey: true });
    }
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", undefined, expect.anything(), expect.anything()));
    return { answer };
  }

  it("the notice is also held in a polite live region, which screen readers announce (fu4)", async () => {
    const { answer } = await busyFolder({ clip: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await waitFor(() => expect(liveHasNotice()).toBe(true));
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  it("Ctrl+C with nothing picked says nothing while busy (fu4)", async () => {
    const { answer } = await busyFolder();
    fireEvent.keyDown(document.body, { key: "c", ctrlKey: true });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(busyNotices()).toBe(0);
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  it("Delete and F2 with nothing picked say nothing while busy (fu4)", async () => {
    const { answer } = await busyFolder();
    fireEvent.keyDown(document.body, { key: "Delete" });
    fireEvent.keyDown(document.body, { key: "F2" });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(busyNotices()).toBe(0);
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  it("Ctrl+V with nothing on the clipboard says nothing while busy (fu4)", async () => {
    const { answer } = await busyFolder();
    fireEvent.keyDown(document.body, { key: "v", ctrlKey: true });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    expect(busyNotices()).toBe(0);
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  it("a key typed on a button outside the listing says nothing while busy (fu4)", async () => {
    const { answer } = await busyFolder();
    const outside = document.createElement("button");
    document.body.appendChild(outside);
    fireEvent.keyDown(outside, { key: "Enter" });
    await act(async () => {
      await new Promise((r) => setTimeout(r, 50));
    });
    outside.remove();
    expect(busyNotices()).toBe(0);
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  it("the sidebar's New is read-only while the next folder's listing is read, and while its place is looked up (fu3b)", async () => {
    let answer!: (page: { items: Node[]; next_cursor: undefined }) => void;
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? new Promise((resolve) => (answer = resolve)) : { items: [node("n1", "alpha.txt")], next_cursor: undefined },
    );
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(get(newPlace)?.canUpload).toBe(true);
    await act(async () => {
      await router.navigate(SUB);
    });
    // The place resolves; the listing does not yet: the page still holds the folder's rows, and the New menu acts on nothing.
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", undefined, expect.anything(), expect.anything()));
    expect(get(newPlace)?.canUpload).toBe(false);
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
    expect(get(newPlace)?.canUpload).toBe(true);
  });

  it("focus in the folder left moves to the first row of the next folder once it loads (fu3b)", async () => {
    let answer!: (page: { items: Node[]; next_cursor: undefined }) => void;
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? new Promise((resolve) => (answer = resolve)) : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    (rows("alpha.txt") as HTMLElement).focus();
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", undefined, expect.anything(), expect.anything()));
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
    expect(document.activeElement).toBe(rows("gamma.txt"));
  });

  // The file picker is offered by the same rule as upload: while the rows of the folder left are shown, there is no picker.
  it("the file picker is not offered while the next folder is read (fu3b)", async () => {
    let answer!: (page: { items: Node[]; next_cursor: undefined }) => void;
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) =>
      dir === "nod_bbbbbbbb" ? new Promise((resolve) => (answer = resolve)) : { items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined },
    );
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(document.querySelector('[role="toolbar"] input[type="file"]')).not.toBeNull();
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalledWith("fld_aaaaaaaa", "nod_bbbbbbbb", undefined, expect.anything(), expect.anything()));
    expect(document.querySelector('[role="toolbar"] input[type="file"]')).toBeNull();
    expect(uploadsMock.add).not.toHaveBeenCalled();
    await act(async () => {
      answer({ items: [node("n3", "gamma.txt")], next_cursor: undefined });
    });
    await screen.findByRole("row", { name: "gamma.txt" });
  });

  it("shows the error, and not the previous rows, when the next folder's first read fails", async () => {
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) => {
      if (dir === "nod_bbbbbbbb") throw new ApiError(500, "internal", {}, 0);
      return { items: [node("n1", "alpha.txt")], next_cursor: undefined };
    });
    const { router } = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await act(async () => {
      await router.navigate(SUB);
    });
    await waitFor(() => expect(document.querySelector('[role="alert"]')).not.toBeNull(), { timeout: 8000 });
    expect(rows("alpha.txt")).toBeNull();
    expect(busyFrame()).toBeNull();
  }, 15000);

  // Item 4: the page's New folder is the shared dialog; a folder made there is read again and shown, and the dialog closes.
  it("a folder made from the page is read again, shown, and its dialog closes (fu3b)", async () => {
    api.mkdir.mockResolvedValue({ id: "nod_new", name: "Plans", kind: "dir" });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await chooseNew("New folder");
    const dialog = await screen.findByRole("dialog");
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n9", "Plans", { kind: "dir" })], next_cursor: undefined });
    fireEvent.change(within(dialog).getByLabelText("Folder name"), { target: { value: "Plans" } });
    fireEvent.click(within(dialog).getByRole("button", { name: "Create folder" }));
    await screen.findByRole("row", { name: "Plans" });
    expect(api.mkdir).toHaveBeenCalledWith("fld_aaaaaaaa", "/Plans");
    expect(screen.queryByRole("dialog")).toBeNull();
  });
});

// Decision 85 (fu5): an empty folder a person can add to shows how to add files, with the page's own Upload files.
describe("an empty folder says how to add files (decision 85)", () => {
  const dt = (files: File[]) => ({ types: ["Files"], files, items: [], dropEffect: "none" });
  const stubPointer = (coarse: boolean) =>
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query === "(pointer: coarse)" ? coarse : query === "(min-width: 1000px)" || query === "(min-width: 360px)",
      media: query,
      addEventListener() {},
      removeEventListener() {},
    }));
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("an empty folder a person can add to shows the heading, the mouse instruction and the Upload files button", async () => {
    stubPointer(false);
    api.childrenPage.mockResolvedValue({ items: [], next_cursor: undefined });
    renderAt(FOLDER);
    const box = await screen.findByTestId("empty-folder");
    expect(within(box).getByRole("heading", { name: translate("en", "files.empty") })).toBeTruthy();
    expect(within(box).getByText(translate("en", "files.emptyMouse"))).toBeTruthy();
    expect(within(box).queryByText(translate("en", "files.emptyTouch"))).toBeNull();
    expect(within(box).getByRole("button", { name: "Upload files" })).toBeTruthy();
    // antd's Empty description carries the heading's words, then the hint (decision 111).
    expect(box.querySelector(".ant-empty .ant-empty-description")?.textContent).toBe(
      translate("en", "files.empty") + translate("en", "files.emptyMouse"),
    );
    // The drop target keeps its visible affordance: a dashed box, styled by its class (decision 111).
    expect(box.classList.contains("empty-folder-drop")).toBe(true);
  });

  it("a folder the person can only read shows antd's Empty with its words, and no upload (decision 111)", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByText(translate("en", "files.empty"));
    expect(document.querySelector('[role="status"] .ant-empty .ant-empty-description')?.textContent).toBe(translate("en", "files.empty"));
    expect(screen.queryByRole("button", { name: /upload files/i })).toBeNull();
  });

  it("on a finger the instruction says to tap Upload, and the mouse instruction is not shown", async () => {
    stubPointer(true);
    api.childrenPage.mockResolvedValue({ items: [], next_cursor: undefined });
    renderAt(FOLDER);
    const box = await screen.findByTestId("empty-folder");
    expect(within(box).getByText(translate("en", "files.emptyTouch"))).toBeTruthy();
    expect(within(box).queryByText(translate("en", "files.emptyMouse"))).toBeNull();
  });

  it("the Upload files button opens the same picker and queue as the New menu's Upload files", async () => {
    stubPointer(false);
    api.childrenPage.mockResolvedValue({ items: [], next_cursor: undefined });
    renderAt(FOLDER);
    const box = await screen.findByTestId("empty-folder");
    const clicked: HTMLElement[] = [];
    const original = HTMLInputElement.prototype.click;
    HTMLInputElement.prototype.click = function (this: HTMLInputElement) {
      if (this.type === "file") clicked.push(this);
      return original.call(this);
    };
    try {
      fireEvent.click(within(box).getByRole("button", { name: "Upload files" }));
      expect(clicked).toHaveLength(1);
      expect(clicked[0]).toBeInstanceOf(HTMLInputElement);
      expect((clicked[0] as HTMLInputElement).multiple).toBe(true);
      // The New menu's own entry opens the picker on the same input.
      fireEvent.click(screen.getByRole("button", { name: "New" }));
      fireEvent.click(await screen.findByRole("menuitem", { name: "Upload files" }));
      expect(clicked).toHaveLength(2);
      expect(clicked[1]).toBe(clicked[0]);
    } finally {
      HTMLInputElement.prototype.click = original;
    }
  });

  it("a folder a person can only read shows the plain text and no button", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByText(translate("en", "files.empty"));
    expect(screen.queryByTestId("empty-folder")).toBeNull();
    expect(screen.queryByRole("button", { name: "Upload files" })).toBeNull();
  });

  it("while the folder is loading there is no box, and the box is not shown for a folder still being read", async () => {
    api.childrenPage.mockReturnValue(new Promise(() => undefined));
    renderAt(FOLDER);
    await waitFor(() => expect(document.querySelector(".ant-skeleton")).not.toBeNull());
    expect(screen.queryByTestId("empty-folder")).toBeNull();
    expect(screen.queryByText(translate("en", "files.empty"))).toBeNull();
  });

  it("while the next folder is read, the empty folder shown before gets no box (decision 77's busy place)", async () => {
    stubPointer(false);
    api.childrenPage.mockImplementation(async (_f: string, dir: string | null) => {
      if (dir === null) return { items: [], next_cursor: undefined };
      return new Promise(() => undefined);
    });
    const { router } = renderAt(FOLDER);
    await screen.findByTestId("empty-folder");
    await act(async () => {
      await router.navigate(`${FOLDER}/nod_bbbbbbbb`);
    });
    // The place is busy: the text the page had before stays, and no box is offered until the next folder is read.
    await screen.findByText(translate("en", "files.empty"));
    expect(screen.queryByTestId("empty-folder")).toBeNull();
    expect(screen.queryByRole("button", { name: "Upload files" })).toBeNull();
  });

  it("a drop on the box queues the files, as a drop on the page does", async () => {
    stubPointer(false);
    api.childrenPage.mockResolvedValue({ items: [], next_cursor: undefined });
    renderAt(FOLDER);
    const box = await screen.findByTestId("empty-folder");
    const file = new File(["x"], "desk.txt", { type: "text/plain" });
    fireEvent.dragEnter(box, { dataTransfer: dt([file]) });
    expect(screen.getByText(translate("en", "files.drop.here"))).toBeTruthy();
    fireEvent.drop(box, { dataTransfer: dt([file]) });
    await waitFor(() => expect(uploadsMock.add).toHaveBeenCalled());
  });
});

// wf1: the New menu's Document, Spreadsheet and Presentation entries (decision 91) are wired to documentKinds with the place and
// the impersonation. Each case also checks a plain entry of the same menu, so an empty menu cannot pass for a hidden document.
describe("decision 91: the document entries follow the place and the impersonation (wf1)", () => {
  const DOCUMENTS = ["Document", "Spreadsheet", "Presentation"];
  const openNew = async () => {
    fireEvent.click(await screen.findByRole("button", { name: "New" }));
    await screen.findByRole("menuitem", { name: "New folder" });
  };
  beforeEach(() => {
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true }]);
    officeEdit.set(true);
    // A folder has a root node, which the place needs to take a new file (documentKinds: nodeID).
    place.rootNodeID = "nod_root";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt")], next_cursor: undefined });
  });
  afterEach(() => {
    officeEdit.set(false);
    place.rootNodeID = null;
    folders.set([]);
  });

  it("offers the three documents in a folder the person edits, while office editing is on", async () => {
    place.role = "editor";
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await openNew();
    // The three documents are submenus of the New menu (Document, Spreadsheet, Presentation), so their titles are the entries here.
    for (const name of DOCUMENTS) expect(screen.getByText(name)).toBeTruthy();
  });

  it("hides the three documents while office editing is off, even in a folder the person edits", async () => {
    officeEdit.set(false);
    place.role = "editor";
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await openNew();
    expect(screen.getByRole("menuitem", { name: "Upload files" })).toBeTruthy();
    for (const name of DOCUMENTS) expect(screen.queryByText(name)).toBeNull();
  });

  it("hides the three documents in a folder the person may add to but not edit, and keeps the other entries", async () => {
    place.role = "contributor";
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await openNew();
    expect(screen.getByRole("menuitem", { name: "Upload files" })).toBeTruthy();
    for (const name of DOCUMENTS) expect(screen.queryByText(name)).toBeNull();
  });

  it("hides the three documents when an installation admin is signed in as the person, and keeps the other entries", async () => {
    place.role = "editor";
    session.set({ ...signedAs("u1"), impersonation: { endsAt: "2099-01-01T00:00:00Z" } });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    await openNew();
    expect(screen.getByRole("menuitem", { name: "Upload files" })).toBeTruthy();
    for (const name of DOCUMENTS) expect(screen.queryByText(name)).toBeNull();
  });
});

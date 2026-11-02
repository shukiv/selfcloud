// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { App as AntdApp } from "antd";
import { createQueryClient } from "../lib/query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/apiError";
import type { Node, Role } from "../lib/files/types";
import { session, type Session } from "../lib/session";
import { writable } from "../lib/store";
import { folders } from "../lib/files/folders";
import { locate } from "../lib/files/location";
import { clipboard, setClip } from "../lib/files/clipboard";
import { get } from "../lib/store";
import { fileErrorText } from "../lib/files/fileErrors";
import { translate } from "../lib/i18n";
import { myStorage } from "../lib/storage/api";

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
  serverLimits: vi.fn(),
}));
const place = vi.hoisted(() => ({ role: "editor" as Role, noDownload: false }));
const uploadsMock = vi.hoisted(() => ({ add: vi.fn(), setChunkSize: vi.fn() }));
const uploadList = vi.hoisted(() => {
  return { store: null as unknown as { set: (v: unknown[]) => void } };
});

vi.mock("../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../lib/files/api")>()), ...api }));
vi.mock("../lib/files/location", async (orig) => ({
  ...(await orig<typeof import("../lib/files/location")>()),
  locate: vi.fn(async (folder: string, dirID: string | null) => ({
    folder,
    folderName: "Team A",
    folderRole: place.role,
    dir: dirID ? { id: dirID, name: "Reports", kind: "dir", role: place.role } : null,
    dirRole: place.role,
    trail: [],
    rootReachable: true,
    reach: [],
    spaceNoDownload: place.noDownload,
    path: dirID ? "/Reports" : "/",
  })),
}));
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
// chooseMore opens the toolbar's More menu and picks the action with this name from it.
async function chooseMore(name: string) {
  fireEvent.click(screen.getByRole("button", { name: "More" }));
  fireEvent.click(await screen.findByRole("menuitem", { name }));
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
  session.set(signedAs("u1"));
  api.getMe.mockResolvedValue({ personal_folder_id: "fld_me" });
  api.serverLimits.mockResolvedValue({ max_chunk_size: 1048576, max_file_size: 1 });
  api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt"), node("n2", "beta.txt")], next_cursor: undefined });
  api.statOrNull.mockImplementation(async (id: string) => (id === "n1" ? node("n1", "alpha.txt") : id === "n2" ? node("n2", "beta.txt") : null));
  uploadList.store.set([]);
});

afterEach(() => {
  session.set(null);
  document.title = "";
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
    expect(api.childrenPage).toHaveBeenLastCalledWith("fld_aaaaaaaa", null, undefined, { key: "name", dir: "asc" });
    api.childrenPage.mockClear();
    fireEvent.click(screen.getByRole("button", { name: /size/i }));
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalled());
    expect(api.childrenPage).toHaveBeenLastCalledWith("fld_aaaaaaaa", null, undefined, { key: "size", dir: "asc" });
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
    expect(await screen.findByRole("button", { name: "Share" })).toBeTruthy();
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
    expect(await screen.findByRole("button", { name: "Share" })).toBeTruthy();
    fireEvent.click(await screen.findByRole("row", { name: "beta.txt" }), { ctrlKey: true });
    await waitFor(() => expect((screen.getByRole("button", { name: "Share" }) as HTMLButtonElement).disabled).toBe(true));
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
    fireEvent.click(await screen.findByRole("button", { name: "Share" }));
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
    fireEvent.click(await screen.findByRole("button", { name: "Share" }));
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
    fireEvent.click(await screen.findByRole("button", { name: "Share" }));
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
    fireEvent.click(await screen.findByRole("button", { name: "Share" }));
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
    // Wait for the new place to be shown: the page draws no dialog at all while it loads.
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(screen.queryByRole("button", { name: "Move to trash" })).toBeNull();
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
    expect(await screen.findByRole("menuitem", { name: "Copy to…" })).toBeTruthy();
    expect(screen.queryByRole("menuitem", { name: "Delete" })).toBeNull();
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
    await screen.findByRole("menuitem", { name: "Delete" });
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
      fireEvent.mouseEnter(within(panel).getByRole("button", { name: "More" }));
      expect(await screen.findByRole("tooltip", { name: "More" })).toBeTruthy();
    } finally {
      window.matchMedia = original;
    }
  });

  // Rows keep the scroll margin of the sticky header and toolbar, so the focused row is never left under them.
  it("a row scrolls into view below the sticky header and toolbar", async () => {
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    // The shell header (56), the toolbar row (48), the pinned column header (48) and 4 of air.
    expect((rows("alpha.txt") as HTMLElement).style.scrollMarginTop).toBe("156px");
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
      expect(await screen.findByRole("menuitem", { name: "Delete" })).toBeTruthy();
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
    const holder = document.querySelector<HTMLElement>(".ant-table-sticky-holder");
    expect(holder).not.toBeNull();
    // The shell's header (56) and the toolbar's row (48 with a mouse).
    expect(holder?.style.top).toBe("104px");
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
    expect(panel.style.height).toBe("40px");
    expect(panel.getAttribute("style")).toContain("padding-block: 4px");
    expect(bar.style.minHeight).toBe(rowHeight);
    expect(rowHeight).toBe("48px");
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
    expect((within(panel).getByRole("button", { name: "Download" }) as HTMLElement).style.minHeight).toBe("44px");
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

// Slice w2: the folder tree beside the listing.
describe("slice w2: the folder tree in the page", () => {
  const wideWindow = (wide: boolean) =>
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: wide && /min-width/.test(query),
      media: query,
      addEventListener() {},
      removeEventListener() {},
      addListener() {},
      removeListener() {},
    }));
  beforeEach(() => {
    localStorage.clear();
    folders.set([{ id: "fld_aaaaaaaa", name: "Team A", kind: "team", access: "write", role: "editor", members_only: false, sharing_managers_only: false, no_download_for_viewers: false, allow_links: true }]);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    folders.set([]);
  });

  it("is beside the listing in a wide window, and absent at phone width", async () => {
    wideWindow(true);
    const wide = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(await screen.findByTestId("folder-tree")).toBeTruthy();
    expect(await screen.findByRole("tree", { name: "Folders" })).toBeTruthy();
    wide.unmount();
    wideWindow(false);
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(screen.queryByTestId("folder-tree")).toBeNull();
    expect(screen.queryByTestId("tree-toggle")).toBeNull();
  });

  it("can be hidden and shown, and the choice and the opened folders are kept for the person, not for the browser", async () => {
    wideWindow(true);
    const first = renderAt(FOLDER);
    await screen.findByTestId("folder-tree");
    fireEvent.click(screen.getByTestId("tree-toggle"));
    expect(screen.queryByTestId("folder-tree")).toBeNull();
    expect(JSON.parse(localStorage.getItem("selfcloud.tree.u1") ?? "{}").open).toBe(false);
    first.unmount();
    // The same person coming back finds it hidden; another person finds their own (open) tree.
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    expect(screen.queryByTestId("folder-tree")).toBeNull();
    act(() => session.set(signedAs("u2")));
    expect(await screen.findByTestId("folder-tree")).toBeTruthy();
    // Nothing of the first person's choice leaks: the second person's own state, if any was written, is open.
    expect(JSON.parse(localStorage.getItem("selfcloud.tree.u2") ?? '{"open":true}').open).toBe(true);
  });

  it("is mounted again for the next person, and one person's opened folders are never written under the next person's key", async () => {
    wideWindow(true);
    localStorage.setItem("selfcloud.tree.u1", JSON.stringify({ open: true, expanded: ["fld_aaaaaaaa", "nod_secret_one"] }));
    renderAt(FOLDER);
    const before = await screen.findByTestId("folder-tree");
    const nav = before.querySelector("nav");
    const writes: Array<[string, string]> = [];
    const set = Storage.prototype.setItem;
    const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(function (this: Storage, k: string, v: string) {
      writes.push([k, v]);
      set.call(this, k, v);
    });
    try {
      act(() => session.set(signedAs("u2")));
      await waitFor(() => expect(screen.getByTestId("folder-tree").querySelector("nav")).not.toBe(nav));
      fireEvent.click(screen.getByTestId("tree-toggle"));
      fireEvent.click(screen.getByTestId("tree-toggle"));
    } finally {
      spy.mockRestore();
    }
    for (const [k, v] of writes) if (k === "selfcloud.tree.u2") expect(v).not.toContain("nod_secret_one");
    expect(JSON.parse(localStorage.getItem("selfcloud.tree.u1") ?? "{}").expanded).toContain("nod_secret_one");
  });

  it("is not part of the section, so a desktop file dropped on it uploads nothing", async () => {
    wideWindow(true);
    renderAt(FOLDER);
    const tree = await screen.findByTestId("folder-tree");
    expect(tree.closest("section.browser")).toBeNull();
    const dt = { types: ["Files"], files: [new File(["x"], "d.txt")], items: [], dropEffect: "none" };
    fireEvent.drop(tree, { dataTransfer: dt });
    expect(uploadsMock.add).not.toHaveBeenCalled();
  });

  it("lists the folder it shows and the folders in it, and lights a folder that takes a dragged item", async () => {
    wideWindow(true);
    api.childrenPage.mockImplementation(async (_f: string, d: string | null) =>
      d === null ? { items: [node("d1", "Docs", { kind: "dir", mime_type: undefined, folder_id: "fld_aaaaaaaa" }), node("n1", "alpha.txt", { folder_id: "fld_aaaaaaaa" })], next_cursor: undefined } : { items: [], next_cursor: undefined },
    );
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    const inTree = await within(await screen.findByTestId("folder-tree")).findByText("Docs");
    const target = inTree.closest(".tree-row") as HTMLElement;
    // Press the item, move, and hover the tree's folder.
    const row = rows("alpha.txt") as Element;
    document.elementFromPoint = () => target;
    act(() => {
      row.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, button: 0, buttons: 1, pointerType: "mouse", isPrimary: true, clientX: 5, clientY: 5 }));
      row.dispatchEvent(new PointerEvent("pointermove", { bubbles: true, button: 0, buttons: 1, pointerType: "mouse", isPrimary: true, clientX: 50, clientY: 50 }));
    });
    expect(target.dataset.dropState).toBe("ok");
    act(() => {
      document.body.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, button: 0, buttons: 0, pointerType: "mouse", isPrimary: true, clientX: 50, clientY: 50 }));
    });
    await waitFor(() => expect(api.moveNode).toHaveBeenCalledTimes(1));
    expect(api.moveNode.mock.calls[0][1]).toMatchObject({ folder: "fld_aaaaaaaa", parent: "d1" });
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

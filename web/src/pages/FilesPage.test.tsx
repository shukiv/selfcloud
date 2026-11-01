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
    expect((rows("alpha.txt") as HTMLElement).style.scrollMarginTop).toBe("112px");
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

// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { createQueryClient } from "../lib/query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../lib/apiError";
import type { Node, Role } from "../lib/files/types";
import { session, type Session } from "../lib/session";
import { writable } from "../lib/store";
import { folders } from "../lib/files/folders";
import { locate } from "../lib/files/location";

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
function renderAt(path: string, client?: QueryClient) {
  // The app's own client: the same stale window and retry rules, so a test sees what a person sees.
  const qc = client ?? createQueryClient();
  const router = createMemoryRouter([{ path: "*", element: <FilesPage /> }], { initialEntries: [path] });
  const view = render(
    <QueryClientProvider client={qc}>
      <RouterProvider router={router} />
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
  api.statOrNull.mockResolvedValue(null);
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
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Move to…" }));
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
    fireEvent.click(rows("alpha.txt") as Element);
    fireEvent.click(screen.getByRole("button", { name: "Rename" }));
    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
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
    fireEvent.click(screen.getByRole("button", { name: "New folder" }));
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
    await screen.findByRole("button", { name: "Details" });
    expect(await screen.findByRole("button", { name: "Share" })).toBeTruthy();
  });

  it("offers Share in the selection bar to every caller for one picked item, as Svelte does (the dialog limits what each role sees)", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "viewer" })], next_cursor: undefined });
    const first = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    expect(await screen.findByRole("button", { name: "Share" })).toBeTruthy();
    first.unmount();

    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "editor", editors_can_share: false })], next_cursor: undefined });
    const second = renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    expect(await screen.findByRole("button", { name: "Share" })).toBeTruthy();
    second.unmount();
  });

  it("offers no Share bar button when the picks are not exactly one item", async () => {
    place.role = "manager";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "manager" }), node("n2", "beta.txt", { role: "manager" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    expect(await screen.findByRole("button", { name: "Share" })).toBeTruthy();
    fireEvent.click(await screen.findByRole("row", { name: "beta.txt" }), { ctrlKey: true });
    await waitFor(() => expect(screen.queryByRole("button", { name: "Share" })).toBeNull());
  });

  it("Details shows Who has access to every caller, and its own Share only where Share is offered", async () => {
    place.role = "viewer";
    api.childrenPage.mockResolvedValue({ items: [node("n1", "alpha.txt", { role: "viewer" })], next_cursor: undefined });
    renderAt(FOLDER);
    await screen.findByRole("row", { name: "alpha.txt" });
    selectOnly("alpha.txt");
    fireEvent.click(await screen.findByRole("button", { name: "Details" }));
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

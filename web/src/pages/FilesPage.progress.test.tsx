// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, cleanup, render, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { App as AntdApp } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { writable, get } from "../lib/store";
import { folders } from "../lib/files/folders";
import { newPlace, type NewPlace } from "../lib/files/newPlace";
import type { FolderAccess, Node } from "../lib/files/types";
import type { UploadItem } from "../lib/files/uploads";

// The listing is replaced by a stub that counts its renders. FilesPage renders the listing on each of its own renders, so the
// count is FilesPage's render count: every upload progress event used to redraw the whole page and its listing).
const counts = vi.hoisted(() => ({ listing: 0 }));
const uploads = vi.hoisted(() => ({ store: null as unknown as ReturnType<typeof writable<UploadItem[]>>, add: vi.fn() }));
const api = vi.hoisted(() => ({
  getMe: vi.fn(),
  childrenPage: vi.fn(),
  statOrNull: vi.fn(),
  readLimits: vi.fn(),
}));

vi.mock("../components/files/Listing", async (orig) => {
  const React = await import("react");
  return {
    ...(await orig<typeof import("../components/files/Listing")>()),
    default: () => {
      counts.listing++;
      return React.createElement("div", { "data-testid": "listing-stub" });
    },
  };
});
vi.mock("../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../lib/files/api")>()), ...api }));
vi.mock("../lib/files/location", async (orig) => ({
  ...(await orig<typeof import("../lib/files/location")>()),
  locate: vi.fn(async (folder: string) => ({
    folder,
    folderName: "Team A",
    folderRole: "editor",
    dir: null,
    dirRole: "editor",
    trail: [],
    rootReachable: true,
    rootNodeID: null,
    reach: [],
    spaceNoDownload: false,
    path: "/",
  })),
}));
vi.mock("../lib/files/folders", async () => {
  const { userWritable } = await import("../lib/store");
  return { folders: userWritable([]), hiddenFolders: userWritable([]), loadFolders: async () => [], loadHiddenFolders: async () => [] };
});
vi.mock("../lib/files/uploads", async () => {
  const { writable } = await import("../lib/store");
  uploads.store = writable<UploadItem[]>([]);
  return { uploads: uploads.store, manager: { add: uploads.add, setChunkSize: vi.fn() }, conflictAsk: writable<string | null>(null) };
});
vi.mock("../lib/stars", () => ({ starNode: vi.fn(), unstarNode: vi.fn() }));
vi.mock("../lib/storage/api", async (orig) => ({
  ...(await orig<typeof import("../lib/storage/api")>()),
  refreshMyStorage: vi.fn(async () => undefined),
}));

import FilesPage from "./FilesPage";

const FOLDER = "/files/fld_aaaaaaaa";
const row = { id: "nod_aaaaaaaa", folder_id: "fld_aaaaaaaa", parent_id: null, name: "alpha.txt", kind: "file", size: 10, mime_type: "text/plain", created_at: "2026-10-01T00:00:00Z", modified_at: "2026-10-01T00:00:00Z", role: "editor", no_download: false, editors_can_share: false } as Node;
const space = { id: "fld_aaaaaaaa", name: "Team A", role: "editor", root_node_id: null, no_download_for_viewers: false } as unknown as FolderAccess;

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: `Name ${id}`, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: "t1",
    publicLinks: false,
  } as unknown as Session;
}

function file(id: number, status: UploadItem["status"]): UploadItem {
  return {
    id,
    name: `big-${id}.bin`,
    size: 40 * 1024 * 1024,
    mime: "application/octet-stream",
    lastModified: 0,
    offset: 0,
    speed: 0,
    status,
    target: { folder: "fld_aaaaaaaa" },
    onConflict: "fail",
    url: null,
    nodeID: null,
    error: null,
    suggested: "",
    head: "",
  } as unknown as UploadItem;
}

function renderAt(path: string) {
  const qc = createQueryClient();
  const router = createMemoryRouter([{ path: "*", element: <FilesPage /> }], { initialEntries: [path] });
  render(
    <QueryClientProvider client={qc}>
      <AntdApp>
        <RouterProvider router={router} />
      </AntdApp>
    </QueryClientProvider>,
  );
  return { router };
}

beforeEach(() => {
  counts.listing = 0;
  session.set(signedAs("u1"));
  folders.set([space]);
  api.getMe.mockResolvedValue({ personal_folder_id: "fld_me" });
  api.readLimits.mockResolvedValue({ max_chunk_size: 1048576, max_file_size: 1 });
  api.statOrNull.mockResolvedValue(null);
  api.childrenPage.mockResolvedValue({ items: [row] as Node[], next_cursor: undefined });
  uploads.store.set([]);
});

afterEach(() => {
  cleanup();
  session.set(null);
  folders.set([]);
  newPlace.set(null);
  vi.clearAllMocks();
});

describe("the files page under an upload", () => {
  it("a progress event does not re-render the page or its listing", async () => {
    renderAt(FOLDER);
    await waitFor(() => expect(counts.listing).toBeGreaterThan(0));
    const before = counts.listing;
    for (let i = 1; i <= 20; i++) {
      act(() => {
        uploads.store.set([file(1, "uploading")].map((u) => ({ ...u, offset: i * 1024 * 1024 })));
      });
    }
    expect(counts.listing - before).toBe(0);
  });

  it("a finished upload still reads the listing again", async () => {
    renderAt(FOLDER);
    await waitFor(() => expect(counts.listing).toBeGreaterThan(0));
    await waitFor(() => expect(api.childrenPage).toHaveBeenCalled());
    const reads = api.childrenPage.mock.calls.length;
    // A part still moving reads nothing: only the finish reads the listing again.
    act(() => {
      uploads.store.set([file(1, "uploading")]);
    });
    await new Promise((r) => setTimeout(r, 50));
    expect(api.childrenPage.mock.calls.length).toBe(reads);
    act(() => {
      uploads.store.set([file(1, "done")]);
    });
    await waitFor(() => expect(api.childrenPage.mock.calls.length).toBeGreaterThan(reads));
  });
});

describe("the page does not re-render for the place it writes", () => {
  it("a write of the same place to newPlace does not re-render the listing", async () => {
    renderAt(FOLDER);
    await waitFor(() => expect(counts.listing).toBeGreaterThan(0));
    await waitFor(() => expect(get(newPlace)).not.toBeNull());
    const before = counts.listing;
    const shown = get(newPlace) as NewPlace;
    act(() => {
      newPlace.set({ ...shown });
    });
    expect(counts.listing - before).toBe(0);
  });
});

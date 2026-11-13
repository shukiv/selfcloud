// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { LIMITS_STALE_MS, seedLimits } from "../lib/files/api";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { App as AntdApp } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { folders } from "../lib/files/folders";
import type { FolderAccess } from "../lib/files/types";
import FilesPage from "./FilesPage";

// The API is the real one (lib/files/api, lib/api, location.ts); only the network is replaced. Each test counts the
// requests a folder open sends, so a repeated read shows up as a second request for the same address (perf-1 3.2).

vi.mock("../lib/files/folders", async () => {
  const { userWritable } = await import("../lib/store");
  return { folders: userWritable([]), hiddenFolders: userWritable([]), loadFolders: async () => [], loadHiddenFolders: async () => [] };
});
vi.mock("../lib/storage/api", async (orig) => ({
  ...(await orig<typeof import("../lib/storage/api")>()),
  refreshMyStorage: vi.fn(async () => undefined),
}));
vi.mock("../lib/stars", () => ({ starNode: vi.fn(), unstarNode: vi.fn() }));

const FOLDER_ROOT = "fld_aaaaaaaa";
const space: FolderAccess = {
  id: FOLDER_ROOT,
  name: "Team A",
  role: "editor",
  root_node_id: null,
  no_download_for_viewers: false,
} as unknown as FolderAccess;

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: `Name ${id}`, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: "t1",
    publicLinks: false,
  } as unknown as Session;
}

// reply answers the requests the folder page makes. Every node named here is a directory of Team A; nod_top is its top.
function reply(url: URL): unknown {
  const p = url.pathname;
  if (p === "/api/v1/me") return { personal_folder_id: "fld_me", storage: null };
  if (p === "/api/v1/info") return { limits: { max_chunk_size: 1048576, max_file_size: 1 }, features: [] };
  if (p === "/api/v1/nodes/nod_dirdirdi") {
    return { id: "nod_dirdirdi", folder_id: FOLDER_ROOT, parent_id: "nod_topptop", name: "Reports", kind: "dir", role: "editor", size: 0, created_at: "2026-10-01T00:00:00Z", modified_at: "2026-10-01T00:00:00Z", no_download: false, editors_can_share: false };
  }
  if (p === "/api/v1/nodes/nod_topptop") {
    return { id: "nod_topptop", folder_id: FOLDER_ROOT, parent_id: null, name: "Top", kind: "dir", role: "editor", size: 0, created_at: "2026-10-01T00:00:00Z", modified_at: "2026-10-01T00:00:00Z", no_download: false, editors_can_share: false };
  }
  if (p.endsWith("/children")) return { items: [row], next_cursor: null };
  return { items: [], next_cursor: null };
}

// row is one file in the top folder, so the listing draws its rows and its column headers.
const row = {
  id: "nod_aaaaaaaa",
  folder_id: FOLDER_ROOT,
  parent_id: null,
  name: "alpha.txt",
  kind: "file",
  size: 10,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "editor",
  no_download: false,
  editors_can_share: false,
};

let calls: string[] = [];

function requestsTo(prefix: string): number {
  return calls.filter((u) => u === prefix || u.startsWith(prefix + "?") || u.startsWith(prefix + "&")).length;
}

beforeEach(() => {
  calls = [];
  session.set(signedAs("u1"));
  folders.set([space]);
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://127.0.0.1");
      calls.push(url.pathname + url.search);
      return new Response(JSON.stringify(reply(url)), { status: 200, headers: { "content-type": "application/json" } });
    }),
  );
});

afterEach(() => {
  cleanup();
  session.set(null);
  folders.set([]);
  vi.unstubAllGlobals();
});

function renderAt(path: string, client?: QueryClient) {
  const qc = client ?? createQueryClient();
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

describe("a folder open reads what it needs once (perf-1 3.2)", () => {
  it("reads the root listing once for the open, not once for a probe and once for the page", async () => {
    renderAt(`/files/${FOLDER_ROOT}`);
    await waitFor(() => expect(requestsTo(`/api/v1/folders/${FOLDER_ROOT}/children`)).toBeGreaterThan(0));
    await waitFor(() => expect(screen.queryByRole("status", { name: /loading/i })).toBeNull());
    expect(requestsTo(`/api/v1/folders/${FOLDER_ROOT}/children`)).toBe(1);
  });

  it("reads /me once across two folder opens", async () => {
    const { router } = renderAt(`/files/${FOLDER_ROOT}`);
    await waitFor(() => expect(requestsTo(`/api/v1/folders/${FOLDER_ROOT}/children`)).toBeGreaterThan(0));
    await act(async () => {
      await router.navigate(`/files/${FOLDER_ROOT}/nod_dirdirdi`);
    });
    await waitFor(() => expect(requestsTo(`/api/v1/nodes/nod_dirdirdi`)).toBeGreaterThan(0));
    await waitFor(() => expect(requestsTo(`/api/v1/nodes/nod_dirdirdi/children`)).toBeGreaterThan(0));
    expect(requestsTo("/api/v1/me")).toBe(1);
  });

  // The boot read of /api/v1/info seeds the upload limits. A fresh value is used on mount with no read; an older one is read again
  // on mount, because the Settings page can change the limits without a restart (decision 127).
  it("uses the boot's upload limits on mount while they are fresh, with no read of /info", async () => {
    const qc = createQueryClient();
    seedLimits(qc, { max_chunk_size: 1048576, max_file_size: 1 }, 0);
    renderAt(`/files/${FOLDER_ROOT}`, qc);
    await waitFor(() => expect(requestsTo(`/api/v1/folders/${FOLDER_ROOT}/children`)).toBeGreaterThan(0));
    expect(requestsTo("/api/v1/info")).toBe(0);
  });

  it("reads /info on mount when the boot's upload limits are older than five minutes", async () => {
    const qc = createQueryClient();
    seedLimits(qc, { max_chunk_size: 1048576, max_file_size: 1 }, LIMITS_STALE_MS + 1000);
    renderAt(`/files/${FOLDER_ROOT}`, qc);
    await waitFor(() => expect(requestsTo("/api/v1/info")).toBe(1));
  });

  // The sort direction is part of the listing's key (childrenKey). Flipping it must read the listing again, in the new order.
  it("reads the listing again in the new order when the sort direction flips", async () => {
    renderAt(`/files/${FOLDER_ROOT}`);
    await screen.findByRole("row", { name: "alpha.txt" });
    fireEvent.click(screen.getByRole("button", { name: /size/i }));
    await waitFor(() => expect(calls.some((u) => u.includes("/children") && u.includes("sort=size"))).toBe(true));
    fireEvent.click(screen.getByRole("button", { name: /size/i }));
    await waitFor(() => expect(calls.some((u) => u.includes("/children") && u.includes("order=desc"))).toBe(true));
  });

  it("starts the directory's listing before its stat has answered", async () => {
    const gate: { open?: () => void } = {};
    const held = new Promise<void>((r) => {
      gate.open = r;
    });
    const real = vi.mocked(globalThis.fetch);
    real.mockImplementation(async (input: RequestInfo | URL) => {
      const url = new URL(String(input), "http://127.0.0.1");
      calls.push(url.pathname + url.search);
      if (url.pathname === "/api/v1/nodes/nod_dirdirdi") await held;
      return new Response(JSON.stringify(reply(url)), { status: 200, headers: { "content-type": "application/json" } });
    });
    renderAt(`/files/${FOLDER_ROOT}/nod_dirdirdi`);
    // The stat of the directory is held. The listing of the directory needs only its id, which the address gives.
    await waitFor(() => expect(requestsTo(`/api/v1/nodes/nod_dirdirdi/children`)).toBeGreaterThan(0));
    expect(requestsTo(`/api/v1/nodes/nod_dirdirdi`)).toBe(1);
    gate.open?.();
  });
});

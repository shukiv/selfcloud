// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../lib/query";
import type { Node } from "../lib/files/types";
import { session, type Session } from "../lib/session";
import NodeLinkPage from "./NodeLinkPage";

const api = vi.hoisted(() => ({ statOrNull: vi.fn(), statNode: vi.fn(), contentPath: vi.fn(), listVersions: vi.fn() }));
vi.mock("../lib/files/api", async (orig) => ({ ...(await orig<typeof import("../lib/files/api")>()), ...api }));

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: `Name ${id}`, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: "t1",
    publicLinks: false,
  };
}

// A file whose folder listing cannot be reached (its parent is not readable) opens on its own.
const lone: Node = {
  id: "nod_aaaaaaaa",
  folder_id: "fld_aaaaaaaa",
  parent_id: "nod_dddddddd",
  name: "alone.txt",
  kind: "file",
  size: 10,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "viewer",
  no_download: false,
  editors_can_share: false,
};

describe("NodeLinkPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    session.set(signedAs("u1"));
    api.statNode.mockRejectedValue(new Error("not reachable"));
    api.contentPath.mockReturnValue("/x");
    api.listVersions.mockResolvedValue({ items: [] });
  });

  it("does not keep the preview of one link on the next link while that one loads", async () => {
    // The second link never answers in this test, so the page is still waiting on it.
    api.statOrNull.mockImplementation(async (id: string) => (id === "nod_aaaaaaaa" ? lone : new Promise(() => {})));
    const router = createMemoryRouter(
      [{ path: "/n/:nod", element: <NodeLinkPage /> }, { path: "*", element: <p>elsewhere</p> }],
      { initialEntries: ["/n/nod_aaaaaaaa"] },
    );
    const view = render(
      <QueryClientProvider client={createQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(document.querySelector(".ant-modal")).not.toBeNull());
    await act(async () => {
      await router.navigate("/n/nod_bbbbbbbb");
    });
    expect(document.querySelector(".ant-modal")).toBeNull();
    view.unmount();
  });
});

describe("the link page leaves the address alone once the person has moved on", () => {
  const dirNode = (id: string, name: string, parent: string | null): Node => ({ ...lone, id, name, kind: "dir", parent_id: parent, mime_type: undefined });
  it("does not send the address back to the folder when the read finishes after a move", async () => {
    window.history.replaceState(null, "", "/shared");
    api.statOrNull.mockResolvedValue(dirNode("nod_aaaaaaaa", "Plans", "nod_parent0"));
    api.statNode.mockResolvedValue(dirNode("nod_parent0", "Team", null));
    const router = createMemoryRouter(
      [{ path: "/n/:nod", element: <NodeLinkPage /> }, { path: "*", element: <p>elsewhere</p> }],
      { initialEntries: ["/n/nod_aaaaaaaa"] },
    );
    render(
      <QueryClientProvider client={createQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(api.statOrNull).toHaveBeenCalled());
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(window.location.pathname).toBe("/shared");
    window.history.replaceState(null, "", "/");
  });
});


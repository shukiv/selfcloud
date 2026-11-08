// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider, createMemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../lib/query";
import type { Node } from "../lib/files/types";
import { session, type Session } from "../lib/session";
import { officeEdit } from "../lib/files/editor";
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


describe("a link a script opened in a new tab (tab=1) goes straight to the editor when office editing is on (oe1)", () => {
  const DOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document";
  const office: Node = { ...lone, id: "nod_0ff1ce01", name: "letter.docx", mime_type: DOCX, role: "editor" };
  const mountAt = (path: string) => {
    // Outside the app the page moves with the History API (lib/router), so the address bar is what the test reads.
    window.history.replaceState(null, "", path);
    const router = createMemoryRouter(
      [
        { path: "/n/:nod", element: <NodeLinkPage /> },
        { path: "/edit/:nod", element: <p>editor page</p> },
        { path: "*", element: <p>elsewhere</p> },
      ],
      { initialEntries: [path] },
    );
    render(
      <QueryClientProvider client={createQueryClient()}>
        <RouterProvider router={router} />
      </QueryClientProvider>,
    );
  };

  beforeEach(() => {
    officeEdit.set(true);
  });
  afterEach(() => {
    officeEdit.set(false);
    window.history.replaceState(null, "", "/");
  });

  it("opens the editor, with tab=1 kept, when office editing is on", async () => {
    api.statOrNull.mockResolvedValue(office);
    api.statNode.mockResolvedValue({ ...lone, id: "nod_parent0", parent_id: null, kind: "dir", mime_type: undefined });
    mountAt("/n/nod_0ff1ce01?tab=1");
    await waitFor(() => expect(window.location.pathname).toBe("/edit/nod_0ff1ce01"));
    expect(window.location.search).toBe("?mode=edit&tab=1");
  });

  it("keeps the folder view for the same link without tab=1", async () => {
    api.statOrNull.mockResolvedValue(office);
    api.statNode.mockResolvedValue({ ...lone, id: "nod_parent0", parent_id: null, kind: "dir", mime_type: undefined });
    mountAt("/n/nod_0ff1ce01");
    await waitFor(() => expect(window.location.pathname).toBe("/files/fld_aaaaaaaa"));
  });

  it("keeps the folder view when office editing is off", async () => {
    officeEdit.set(false);
    api.statOrNull.mockResolvedValue(office);
    api.statNode.mockResolvedValue({ ...lone, id: "nod_parent0", parent_id: null, kind: "dir", mime_type: undefined });
    mountAt("/n/nod_0ff1ce01?tab=1");
    await waitFor(() => expect(window.location.pathname).toBe("/files/fld_aaaaaaaa"));
  });
});

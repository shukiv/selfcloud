// SPDX-License-Identifier: AGPL-3.0-or-later
import { act, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { ApiError } from "../../lib/apiError";
import type { Node, Role } from "../../lib/files/types";

const api = vi.hoisted(() => ({
  listShareLinks: vi.fn(),
  createDropLink: vi.fn(),
  updateDropLink: vi.fn(),
  deleteShareLink: vi.fn(),
  resetShareLink: vi.fn(),
}));
vi.mock("../../lib/sharing/api", async (orig) => ({ ...(await orig<typeof import("../../lib/sharing/api")>()), ...api }));

import RequestFiles from "./RequestFiles";

const DROP_LINK = {
  id: "shl_drop",
  node_id: "nod_board",
  audience: "drop",
  role: "viewer",
  expires_at: "2027-02-01T00:00:00Z",
  created_at: "",
  inherited_from: null,
  password_set: false,
  paused: false,
};

function nodeOf(role: Role): Node {
  return {
    id: "nod_board",
    folder_id: "fld_team",
    parent_id: null,
    name: "Board",
    kind: "dir",
    size: 0,
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role,
    no_download: false,
    editors_can_share: false,
  };
}

function renderFiles() {
  const client = createQueryClient();
  const view = render(
    <QueryClientProvider client={client}>
      <RequestFiles node={nodeOf("manager")} cap="manager" onchanged={vi.fn()} />
    </QueryClientProvider>,
  );
  return { ...view, client };
}

describe("request files: the links that cannot be read", () => {
  beforeEach(() => {
    api.listShareLinks.mockReset();
  });
  afterEach(() => vi.clearAllMocks());

  it("a failed first load shows the failure and no switch or form (guard: the isError branch, !data)", async () => {
    api.listShareLinks.mockRejectedValue(new ApiError(403, "forbidden", {}, 0));
    renderFiles();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(document.getElementById("drop-expires")).toBeNull();
  });

  it("a failed refetch of a list already shown keeps the switch and the form (guard: the !data test)", async () => {
    api.listShareLinks.mockResolvedValueOnce({ items: [DROP_LINK] });
    const { client } = renderFiles();
    await waitFor(() => expect(screen.getByRole("switch")).toBeTruthy());
    api.listShareLinks.mockRejectedValue(new ApiError(403, "forbidden", {}, 0));
    await act(async () => {
      await client.invalidateQueries();
    });
    await waitFor(() => expect(api.listShareLinks.mock.calls.length).toBeGreaterThan(1));
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(screen.queryByRole("alert")).toBeNull();
    expect(screen.getByRole("switch")).toBeTruthy();
  });
});

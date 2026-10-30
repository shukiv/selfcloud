// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../../lib/apiError";
import type { Node, Version } from "../../lib/files/types";

const api = vi.hoisted(() => ({
  listVersions: vi.fn(),
  statNode: vi.fn(),
  restoreVersion: vi.fn(),
  updateVersion: vi.fn(),
  deleteVersion: vi.fn(),
}));
vi.mock("../../lib/files/api", async (orig) => ({
  ...(await orig<typeof import("../../lib/files/api")>()),
  ...api,
}));

import Versions from "./Versions";

const node: Node = {
  id: "n1",
  folder_id: "f1",
  parent_id: null,
  name: "plan.txt",
  kind: "file",
  size: 10,
  mime_type: "text/plain",
  etag: "e-current",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "editor",
  no_download: false,
  editors_can_share: false,
};

function version(id: string, over: Partial<Version> = {}): Version {
  return {
    id,
    current: false,
    size: 10,
    etag: `etag-${id}`,
    created_at: "2026-10-01T00:00:00Z",
    replaced_at: "2026-10-02T00:00:00Z",
    label: null,
    keep_forever: false,
    ...over,
  };
}

const CURRENT = version("cur", { current: true, replaced_at: undefined });
const OLD = version("v1", { label: "draft" });

function renderVersions(over: Partial<{ node: Node; onChanged: () => void }> = {}) {
  const onClose = vi.fn();
  const onChanged = over.onChanged ?? vi.fn();
  render(<Versions node={over.node ?? node} onClose={onClose} onChanged={onChanged} />);
  return { onClose, onChanged };
}

describe("Versions", () => {
  beforeEach(() => {
    for (const fn of Object.values(api)) fn.mockReset();
    api.listVersions.mockResolvedValue({ items: [CURRENT, OLD] });
    api.updateVersion.mockResolvedValue(OLD);
    api.statNode.mockResolvedValue({ ...node, etag: "fresh" });
    api.restoreVersion.mockResolvedValue(node);
    api.deleteVersion.mockResolvedValue(undefined);
  });

  it("lists the current version and the earlier ones", async () => {
    renderVersions();
    const items = await screen.findAllByTestId("version");
    expect(items.length).toBe(2);
    expect(within(items[0]).getByText("Current version")).toBeTruthy();
    expect(within(items[1]).getByRole("button", { name: "Restore this version" })).toBeTruthy();
  });

  it("restores an earlier version with the fresh ETag and tells the page it changed", async () => {
    const { onChanged } = renderVersions();
    const items = await screen.findAllByTestId("version");
    fireEvent.click(within(items[1]).getByRole("button", { name: "Restore this version" }));
    await waitFor(() => expect(api.restoreVersion).toHaveBeenCalledWith("n1", "v1", "fresh"));
    expect(onChanged).toHaveBeenCalledTimes(1);
    expect(await screen.findByText(/Restored\./)).toBeTruthy();
  });

  it("saves a label, and sends null for a blank one", async () => {
    renderVersions();
    const items = await screen.findAllByTestId("version");
    const input = within(items[1]).getByRole("textbox", { name: "Label" });
    fireEvent.change(input, { target: { value: "   " } });
    fireEvent.click(within(items[1]).getByRole("button", { name: "Save label" }));
    await waitFor(() => expect(api.updateVersion).toHaveBeenCalledWith("n1", "v1", { label: null }));
  });

  it("shows the keep-cap text with the number from the server", async () => {
    api.updateVersion.mockRejectedValue(new ApiError(409, "conflict", { max_kept: 3 }, 0));
    renderVersions();
    const items = await screen.findAllByTestId("version");
    fireEvent.click(within(items[1]).getByRole("checkbox", { name: "Keep forever" }));
    expect(await screen.findByText("This file already keeps 3 versions forever. Unkeep one first.")).toBeTruthy();
    expect(api.updateVersion).toHaveBeenCalledWith("n1", "v1", { keep_forever: true });
  });

  it("shows the generic text, not server text, for other failures", async () => {
    api.deleteVersion.mockRejectedValue(new ApiError(500, "internal", { message: "SQL: boom" }, 0));
    renderVersions();
    const items = await screen.findAllByTestId("version");
    fireEvent.click(within(items[1]).getByRole("button", { name: "Delete" }));
    await waitFor(() => expect(api.deleteVersion).toHaveBeenCalledWith("n1", "v1"));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toBe("The server had a problem. Try again in a moment.");
    expect(screen.queryByText(/SQL/)).toBeNull();
  });
});

describe("Versions close control", () => {
  it("has one close control, the drawer's X", async () => {
    renderVersions();
    await screen.findByRole("dialog");
    expect(screen.getAllByRole("button", { name: /close/i })).toHaveLength(1);
  });
});

describe("Versions by role", () => {
  beforeEach(() => {
    for (const fn of Object.values(api)) fn.mockReset();
    api.listVersions.mockResolvedValue({ items: [CURRENT, OLD] });
  });

  it("offers a viewer no restore, and no download where the space forbids it", async () => {
    renderVersions({ node: { ...node, role: "viewer", no_download: true } });
    const items = await screen.findAllByTestId("version");
    expect(within(items[1]).queryByRole("button", { name: "Restore this version" })).toBeNull();
    expect(within(items[1]).queryByRole("button", { name: /download/i })).toBeNull();
  });
});

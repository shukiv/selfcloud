// SPDX-License-Identifier: AGPL-3.0-or-later

// Trash on the one table (tl4, decision 137): columns, the original location from the server's place (no per-parent lookup), the
// Restore and Delete forever icons named after their items, the bulk actions for the selected rows, and Empty trash as an icon.
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Role, TrashItem } from "../lib/files/types";
import { session, type Session } from "../lib/session";
import { desktopViewport } from "../test/viewport";

const api = vi.hoisted(() => ({
  getMe: vi.fn(),
  listTrash: vi.fn(),
  restoreNode: vi.fn(),
  purgeTrashed: vi.fn(),
  emptyTrashStep: vi.fn(),
  statOrNull: vi.fn(),
}));
vi.mock("../lib/files/api", () => api);
vi.mock("../lib/storage/api", () => ({ refreshMyStorage: async () => undefined }));
vi.mock("../lib/files/folders", async () => {
  const { writable } = await import("../lib/store");
  return {
    folders: writable([{ id: "f1", name: "Finance", kind: "team", root_node_id: "root_f1", access: "write", role: "manager" }]),
    loadFolders: async () => [],
  };
});

import TrashPage from "./TrashPage";

function item(id: string, name: string, role: Role, over: Partial<TrashItem> = {}): TrashItem {
  return {
    id,
    folder_id: "f1",
    parent_id: "dir_b",
    name,
    kind: "file",
    size: 10,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role,
    no_download: false,
    editors_can_share: false,
    trashed_at: "2026-10-20T00:00:00Z",
    trashed_by: "u1",
    original_parent_id: "dir_b",
    purge_at: new Date(Date.now() + 10 * 86_400_000).toISOString(),
    place: "Budget",
    ...over,
  } as TrashItem;
}

function signIn() {
  const s: Session = {
    user: { id: "u1", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en" } as Session["user"],
    account: null,
    csrf: "csrf",
    publicLinks: false,
  };
  session.set(s);
}

function renderTrash(items: TrashItem[]) {
  api.listTrash.mockResolvedValue({ items, next_cursor: undefined });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <TrashPage />
    </QueryClientProvider>,
  );
}

const table = () => screen.findByRole("region", { name: "Trash" });

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
  signIn();
  api.getMe.mockResolvedValue({ personal_folder_id: "f_mine" });
  api.statOrNull.mockResolvedValue(null);
  api.restoreNode.mockImplementation(async (id: string) => ({ name: id === "t1" ? "Old logo.png" : "Draft.xlsx" }));
});

afterEach(() => {
  undoView();
  session.set(null);
  vi.clearAllMocks();
});

describe("Trash is one table (tl4, decision 137)", () => {
  it("draws the columns Name, Original location, Deleted on and Size", async () => {
    renderTrash([item("t1", "Old logo.png", "editor")]);
    const region = await table();
    const heads = within(region).getAllByRole("columnheader").map((h) => h.textContent ?? "");
    expect(heads).toEqual(expect.arrayContaining(["Name", "Original location", "Deleted on", "Size"]));
  });

  it("shows where each item was from the server's place, and asks no folder for it", async () => {
    renderTrash([item("t1", "Old logo.png", "editor"), item("t2", "Draft.xlsx", "editor", { place: "Finance", original_parent_id: "dir_f" })]);
    const region = await table();
    expect(within(region).getByRole("row", { name: "Old logo.png" }).textContent).toContain("Budget");
    expect(within(region).getByRole("row", { name: "Draft.xlsx" }).textContent).toContain("Finance");
    expect(api.statOrNull).not.toHaveBeenCalled();
  });

  it("gives an item with no place in reach nothing, rather than a guessed place", async () => {
    renderTrash([item("t1", "Old logo.png", "editor", { place: undefined, original_parent_id: "dir_gone" })]);
    const region = await table();
    expect(within(region).getByRole("row", { name: "Old logo.png" }).textContent).not.toContain("Was in");
  });

  it("names Restore after its item, with an icon, for a caller who can edit it", async () => {
    renderTrash([item("t1", "Old logo.png", "editor")]);
    const region = await table();
    const restore = within(region).getByRole("button", { name: "Restore: Old logo.png" });
    expect(restore.textContent).toBe("");
    expect(within(region).queryByRole("button", { name: "Delete forever: Old logo.png" })).toBeNull();
  });

  it("offers Delete forever only to a manager of the item", async () => {
    renderTrash([item("t1", "Old logo.png", "manager")]);
    const region = await table();
    expect(within(region).getByRole("button", { name: "Delete forever: Old logo.png" })).toBeTruthy();
  });

  it("makes Empty trash an icon with a name and no words, for a manager", async () => {
    renderTrash([item("t1", "Old logo.png", "manager")]);
    await table();
    const empty = screen.getByRole("button", { name: "Empty trash" });
    expect(empty.textContent).toBe("");
  });

  it("restores the selected rows in one bulk action", async () => {
    renderTrash([item("t1", "Old logo.png", "editor"), item("t2", "Draft.xlsx", "editor")]);
    const region = await table();
    fireEvent.click(within(region).getByRole("checkbox", { name: "Select Old logo.png" }));
    const bulk = await waitFor(() => within(region).getByRole("button", { name: "Restore" }));
    fireEvent.click(bulk);
    await waitFor(() => expect(api.restoreNode).toHaveBeenCalledWith("t1"));
    expect(api.restoreNode).not.toHaveBeenCalledWith("t2");
  });

  it("keeps the table on screen when the trash is empty, and says so inside the body", async () => {
    renderTrash([]);
    const region = await table();
    expect(within(region).getByRole("columnheader", { name: "Deleted on" })).toBeTruthy();
    expect(region.querySelector(".empty")?.textContent).toContain("The trash is empty");
  });

  it("marks the Deleted on column as the order the rows are in, newest first", async () => {
    renderTrash([item("t1", "Old logo.png", "editor")]);
    const region = await table();
    expect(within(region).getByRole("columnheader", { name: "Deleted on" }).getAttribute("aria-sort")).toBe("descending");
  });

  it("adds the next page's items on Load more", async () => {
    api.listTrash.mockResolvedValueOnce({ items: [item("t1", "Old logo.png", "editor")], next_cursor: "c2" });
    api.listTrash.mockResolvedValueOnce({ items: [item("t2", "Draft.xlsx", "editor")], next_cursor: undefined });
    renderTrash([]);
    await table();
    fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
    const region = await table();
    await waitFor(() => expect(within(region).getByRole("row", { name: "Draft.xlsx" })).toBeTruthy());
    expect(within(region).getByRole("row", { name: "Old logo.png" })).toBeTruthy();
  });

  it("keeps the rows on screen when the next page fails, and says why above them", async () => {
    api.listTrash.mockResolvedValueOnce({ items: [item("t1", "Old logo.png", "editor")], next_cursor: "c2" });
    api.listTrash.mockRejectedValueOnce(new Error("offline"));
    renderTrash([]);
    await table();
    fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy());
    const region = screen.getByRole("region", { name: "Trash" });
    expect(within(region).getByRole("row", { name: "Old logo.png" })).toBeTruthy();
  });

  it("deletes for good only the selected items the person manages", async () => {
    api.purgeTrashed.mockResolvedValue(undefined);
    renderTrash([item("t1", "Old logo.png", "manager"), item("t2", "Draft.xlsx", "editor")]);
    const region = await table();
    fireEvent.click(within(region).getByRole("checkbox", { name: "Select Old logo.png" }));
    fireEvent.click(within(region).getByRole("checkbox", { name: "Select Draft.xlsx" }));
    fireEvent.click(await waitFor(() => within(region).getByRole("button", { name: "Delete forever" })));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete forever" }));
    await waitFor(() => expect(api.purgeTrashed).toHaveBeenCalledWith("t1"));
    expect(api.purgeTrashed).not.toHaveBeenCalledWith("t2");
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Role, TrashItem } from "../lib/files/types";
import { session, type Session } from "../lib/session";

const api = vi.hoisted(() => ({
  getMe: vi.fn(),
  listTrash: vi.fn(),
  restoreNode: vi.fn(),
  purgeTrashed: vi.fn(),
  emptyTrashStep: vi.fn(),
  statOrNull: vi.fn(),
}));
vi.mock("../lib/files/api", () => api);
vi.mock("../lib/files/folders", async () => {
  const { writable } = await import("../lib/store");
  return { folders: writable([]), loadFolders: async () => [] };
});

import TrashPage from "./TrashPage";

function item(id: string, name: string, role: Role): TrashItem {
  return {
    id,
    folder_id: "f1",
    parent_id: "p1",
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
    original_parent_id: "p1",
    purge_at: new Date(Date.now() + 10 * 86_400_000).toISOString(),
  };
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

// renderTrash gives each test its own query client, so no answer is shared between tests.
function renderTrash(items: TrashItem[]) {
  api.listTrash.mockResolvedValue({ items, next_cursor: undefined });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <TrashPage />
    </QueryClientProvider>,
  );
}

// row waits for the listing to arrive, then returns the item's list entry.
const row = async (name: string) => (await screen.findByText(name)).closest("li") as HTMLElement;

describe("TrashPage marks a shortcut and a coloured folder in words", () => {
  beforeEach(() => {
    signIn();
    api.getMe.mockResolvedValue({ personal_folder_id: "f1" });
    api.statOrNull.mockResolvedValue({ id: "p1", parent_id: null, name: "Plans" });
  });
  afterEach(() => session.set(null));

  it("says Shortcut and Unavailable on a shortcut, and names a folder's colour", async () => {
    renderTrash([
      { ...item("s1", "Gone link", "editor"), kind: "shortcut", unavailable: true },
      { ...item("d1", "Reports", "editor"), kind: "dir", color: "teal" },
    ]);
    expect(within(await row_("Gone link")).getByText("Unavailable")).toBeTruthy();
    expect(within(await row_("Reports")).getByText("Colour: Teal")).toBeTruthy();
  });
});

describe("TrashPage", () => {
  beforeEach(() => {
    signIn();
    api.getMe.mockResolvedValue({ personal_folder_id: "f1" });
    api.statOrNull.mockResolvedValue({ id: "p1", parent_id: null, name: "Plans" });
    api.restoreNode.mockReset();
    api.purgeTrashed.mockReset();
    api.emptyTrashStep.mockReset();
  });
  afterEach(() => {
    session.set(null);
  });

  it("restores an item and shows the restored notice with its name", async () => {
    api.restoreNode.mockResolvedValue({ id: "a", name: "plan.txt" });
    renderTrash([item("a", "plan.txt", "editor")]);
    fireEvent.click(await within(await row("plan.txt")).findByRole("button", { name: "Restore" }));
    await waitFor(() => expect(api.restoreNode).toHaveBeenCalledWith("a"));
    expect(await screen.findByText("Restored plan.txt.")).toBeTruthy();
  });

  it("delete forever opens a confirmation first, and only the confirm button purges", async () => {
    api.purgeTrashed.mockResolvedValue(undefined);
    renderTrash([item("b", "old.txt", "manager")]);
    fireEvent.click(await within(await row("old.txt")).findByRole("button", { name: "Delete forever" }));
    const dialog = await screen.findByRole("dialog");
    expect(api.purgeTrashed).not.toHaveBeenCalled();
    const ok = within(dialog).getByRole("button", { name: "Delete forever" });
    expect(ok.className).toContain("ant-btn-dangerous");
    fireEvent.click(ok);
    await waitFor(() => expect(api.purgeTrashed).toHaveBeenCalledWith("b"));
  });

  it("empty trash loops emptyTrashStep while the server says more remain", async () => {
    api.emptyTrashStep
      .mockResolvedValueOnce({ purged: 1000, more: true })
      .mockResolvedValueOnce({ purged: 5, more: false });
    renderTrash([item("c", "big.bin", "manager")]);
    fireEvent.click(await screen.findByRole("button", { name: "Empty trash" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Empty trash" }));
    await waitFor(() => expect(api.emptyTrashStep).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("The trash was emptied.")).toBeTruthy();
  });

  it("an editor gets Restore but not Delete forever, and no Empty trash", async () => {
    renderTrash([item("d", "shared.txt", "editor")]);
    const r = await within(await row("shared.txt")).findByRole("button", { name: "Restore" });
    expect(r).toBeTruthy();
    expect(within(await row("shared.txt")).queryByRole("button", { name: "Delete forever" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Empty trash" })).toBeNull();
  });
});

describe("TrashPage reads and refreshes", () => {
  beforeEach(() => {
    signIn();
    api.getMe.mockResolvedValue({ personal_folder_id: "f1" });
    api.statOrNull.mockImplementation(async (id: string) => ({ id, parent_id: null, name: id === "p1" ? "Plans" : "Archive" }));
    api.restoreNode.mockReset();
    api.purgeTrashed.mockReset();
  });
  afterEach(() => {
    session.set(null);
  });

  it("looks up each place once, and only the places that arrive with a new page", async () => {
    const first = { ...item("t1", "a.txt", "editor"), original_parent_id: "p1" };
    const second = { ...item("t2", "b.txt", "editor"), original_parent_id: "p2" };
    api.listTrash.mockReset();
    api.listTrash.mockResolvedValueOnce({ items: [first], next_cursor: "c2" }).mockResolvedValueOnce({ items: [second], next_cursor: undefined });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(
      <QueryClientProvider client={client}>
        <TrashPage />
      </QueryClientProvider>,
    );
    await screen.findByText("a.txt");
    await waitFor(() => expect(api.statOrNull).toHaveBeenCalledWith("p1"));
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    await screen.findByText("b.txt");
    await waitFor(() => expect(api.statOrNull).toHaveBeenCalledWith("p2"));
    const calls = api.statOrNull.mock.calls.map((c) => c[0] as string);
    expect(calls.filter((id) => id === "p1").length).toBe(1);
    expect(calls.filter((id) => id === "p2").length).toBe(1);
  });

  it("restore refreshes the folder's listings and places, not only the trash", async () => {
    api.listTrash.mockReset();
    api.listTrash.mockResolvedValue({ items: [item("t1", "a.txt", "editor")], next_cursor: undefined });
    api.restoreNode.mockResolvedValue({ id: "t1", name: "a.txt" });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const spy = vi.spyOn(client, "invalidateQueries");
    render(
      <QueryClientProvider client={client}>
        <TrashPage />
      </QueryClientProvider>,
    );
    await screen.findByText("a.txt");
    const row = (await screen.findByText("a.txt")).closest("li") as HTMLElement;
    fireEvent.click(within(row).getByRole("button", { name: "Restore" }));
    await waitFor(() => expect(api.restoreNode).toHaveBeenCalled());
    const keys = spy.mock.calls.map((c) => (c[0] as { queryKey: unknown[] }).queryKey.join("/"));
    expect(keys.some((k) => k.endsWith("/children"))).toBe(true);
    expect(keys.some((k) => k.endsWith("/place"))).toBe(true);
  });

  it("reads the trash again on each visit, even inside the stale window", async () => {
    api.listTrash.mockReset();
    api.listTrash.mockResolvedValue({ items: [item("t1", "a.txt", "editor")], next_cursor: undefined });
    const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 30_000 } } });
    const first = render(
      <QueryClientProvider client={client}>
        <TrashPage />
      </QueryClientProvider>,
    );
    await screen.findByText("a.txt");
    first.unmount();
    api.listTrash.mockClear();
    render(
      <QueryClientProvider client={client}>
        <TrashPage />
      </QueryClientProvider>,
    );
    await screen.findByText("a.txt");
    await waitFor(() => expect(api.listTrash).toHaveBeenCalled());
  });
});

describe("TrashPage shows people by name, never a raw ID", () => {
  it("leaves another person's raw ID off the trash line", async () => {
    signIn();
    api.getMe.mockResolvedValue({ personal_folder_id: "f1" });
    renderTrash([{ ...item("t1", "a.txt", "editor"), trashed_by: "usr_bbbbbbbb" }]);
    await screen.findByText("a.txt");
    expect(document.body.textContent).not.toContain("usr_bbbbbbbb");
    session.set(null);
  });
});

describe("TrashPage restore by role", () => {
  beforeEach(() => {
    signIn();
    api.getMe.mockResolvedValue({ personal_folder_id: "f1" });
    api.statOrNull.mockResolvedValue({ id: "p1", parent_id: null, name: "Plans" });
  });
  afterEach(() => session.set(null));

  it("offers a viewer no Restore on an item", async () => {
    renderTrash([item("t1", "a.txt", "viewer")]);
    const row = await row_("a.txt");
    expect(within(row).queryByRole("button", { name: "Restore" })).toBeNull();
  });
});

// row_ waits for an item by name and returns its list entry.
async function row_(name: string): Promise<HTMLElement> {
  return (await screen.findByText(name)).closest("li") as HTMLElement;
}

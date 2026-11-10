// SPDX-License-Identifier: AGPL-3.0-or-later

// Recent on the one table (tl4, decision 137): columns, the Opened time column, the Remove icon, Clear as an icon with a name and
// no words, no selection, and the empty state inside the table.
import { fireEvent, render, screen, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { folders } from "../lib/files/folders";
import type { FolderAccess } from "../lib/files/types";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { desktopViewport } from "../test/viewport";
import RecentPage from "./RecentPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const recent = (id: string, name: string, over: Record<string, unknown> = {}) => ({
  id,
  folder_id: "fld_a",
  parent_id: "root_a",
  name,
  kind: "file",
  size: 10,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "editor",
  owner_id: "usr_a",
  no_download: false,
  editors_can_share: false,
  viewed_at: "2026-10-05T09:00:00Z",
  edited_at: null,
  last_at: "2026-10-05T09:00:00Z",
  ...over,
});

const SPACE = { id: "fld_a", name: "Finance", kind: "team", root_node_id: "root_a" } as unknown as FolderAccess;

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
  session.set(signedAs("olivia"));
  folders.set([SPACE]);
});

afterEach(() => {
  undoView();
  session.set(null);
  folders.set([]);
});

const table = () => screen.findByRole("region", { name: "Recent" });

describe("Recent is one table (tl4, decision 137)", () => {
  it("draws the columns Name, Location and Opened, with Opened as the time column in order (aria-sort descending)", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/recent") ? { status: 200, body: { items: [recent("nod_r1", "Budget.xlsx", { parent_id: "dir_f", place: "Finance" })] } } : fail(404, "not_found")));
    render(<RecentPage />, { wrapper: ({ children }) => <QueryClientProvider client={createQueryClient()}>{children}</QueryClientProvider> });
    const region = await table();
    const heads = within(region).getAllByRole("columnheader").map((h) => h.textContent ?? "");
    expect(heads).toEqual(expect.arrayContaining(["Name", "Location", "Opened"]));
    expect(screen.getByRole("columnheader", { name: "Opened" }).getAttribute("aria-sort")).toBe("descending");
  });

  it("names the Remove icon after its item, and offers no selection", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/recent") ? { status: 200, body: { items: [recent("nod_r1", "Budget.xlsx")] } } : fail(404, "not_found")));
    render(<RecentPage />, { wrapper: ({ children }) => <QueryClientProvider client={createQueryClient()}>{children}</QueryClientProvider> });
    const region = await table();
    expect(within(region).getByRole("button", { name: "Remove Budget.xlsx from Recent" })).toBeTruthy();
    expect(within(region).queryByRole("checkbox")).toBeNull();
  });

  it("makes Clear an icon with a name and no words, which still asks first", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/recent") ? { status: 200, body: { items: [recent("nod_r1", "Budget.xlsx")] } } : fail(404, "not_found")));
    render(<RecentPage />, { wrapper: ({ children }) => <QueryClientProvider client={createQueryClient()}>{children}</QueryClientProvider> });
    await table();
    const clear = screen.getByRole("button", { name: "Clear all" });
    expect(clear.textContent).toBe("");
    fireEvent.click(clear);
    expect(await screen.findByText("Clear Recent?")).toBeTruthy();
  });

  it("keeps the table on screen when Recent is empty, and says so inside the body", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/recent") ? { status: 200, body: { items: [] } } : fail(404, "not_found")));
    render(<RecentPage />, { wrapper: ({ children }) => <QueryClientProvider client={createQueryClient()}>{children}</QueryClientProvider> });
    const region = await table();
    expect(within(region).getByRole("columnheader", { name: "Opened" })).toBeTruthy();
    expect(region.querySelector(".empty")?.textContent).toContain("Nothing here yet");
  });
});

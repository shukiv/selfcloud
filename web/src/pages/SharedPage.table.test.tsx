// SPDX-License-Identifier: AGPL-3.0-or-later

// Shared with me on the one table (tl4, decision 137): columns, Shared by from the owner's name, the role, Shared on from shared_at
// as the time column, and the empty state inside the table.
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { folders } from "../lib/files/folders";
import type { FolderAccess } from "../lib/files/types";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { desktopViewport } from "../test/viewport";
import SharedPage from "./SharedPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const shared = (id: string, name: string, over: Record<string, unknown> = {}) => ({
  id,
  folder_id: "fld_t",
  parent_id: "root_t",
  name,
  kind: "file",
  size: 10,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-02T00:00:00Z",
  role: "viewer",
  owner_id: "usr_omer",
  owner_name: "Omer Katz",
  no_download: false,
  editors_can_share: false,
  shared_at: "2026-11-06T10:00:00Z",
  ...over,
});

const SPACE = { id: "fld_t", name: "Team", kind: "team", root_node_id: "root_t" } as unknown as FolderAccess;

function renderPage() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <SharedPage />
    </QueryClientProvider>,
  );
}

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

const table = () => screen.findByRole("region", { name: "Shared with me" });

describe("Shared with me is one table (tl4, decision 137)", () => {
  it("draws the columns Name, Shared by, Your role and Shared on", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/shared") ? { status: 200, body: { items: [shared("nod_h1", "Supplier contract.pdf")] } } : fail(404, "not_found")));
    renderPage();
    const region = await table();
    // The secondary columns show once the breakpoint is known, which is after the table's first draw.
    await screen.findByRole("columnheader", { name: "Shared on" });
    const heads = within(region).getAllByRole("columnheader").map((h) => h.textContent ?? "");
    expect(heads).toEqual(expect.arrayContaining(["Name", "Shared by", "Your role", "Shared on"]));
  });

  it("shows the owner's name and the role, and dates the row from shared_at (not modified_at)", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/shared") ? { status: 200, body: { items: [shared("nod_h1", "Supplier contract.pdf")] } } : fail(404, "not_found")));
    renderPage();
    const region = await table();
    const row = within(region).getByRole("row", { name: "Supplier contract.pdf" });
    expect(row.textContent).toContain("Omer Katz");
    expect(row.textContent).toContain("Can view");
    expect(row.textContent).toContain("Nov 6, 2026");
    expect(row.textContent).not.toContain("Nov 2, 2026");
  });

  it("marks Shared on as the time column in order (aria-sort descending)", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/shared") ? { status: 200, body: { items: [shared("nod_h1", "Supplier contract.pdf")] } } : fail(404, "not_found")));
    renderPage();
    await table();
    expect(screen.getByRole("columnheader", { name: "Shared on" }).getAttribute("aria-sort")).toBe("descending");
  });

  it("opens an item from its name, which is a link to the item", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/shared") ? { status: 200, body: { items: [shared("nod_h1", "Supplier contract.pdf")] } } : fail(404, "not_found")));
    renderPage();
    const region = await table();
    expect(within(region).getByRole("link", { name: "Supplier contract.pdf" }).getAttribute("href")).toBe("/n/nod_h1");
  });

  it("keeps the table on screen when nothing is shared, and says so inside the body", async () => {
    stubFetch((c: Call) => (c.url.startsWith("/api/v1/shared") ? { status: 200, body: { items: [] } } : fail(404, "not_found")));
    renderPage();
    const region = await table();
    expect(within(region).getByRole("columnheader", { name: "Shared on" })).toBeTruthy();
    expect(region.querySelector(".empty")?.textContent).toContain("Nothing is shared with you yet");
  });

  it("adds the next page's items on Load more", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("cursor=c2")) return { status: 200, body: { items: [shared("nod_h2", "Budget.xlsx")] } };
      if (c.url.startsWith("/api/v1/shared")) return { status: 200, body: { items: [shared("nod_h1", "Supplier contract.pdf")], next_cursor: "c2" } };
      return fail(404, "not_found");
    });
    renderPage();
    const region = await table();
    fireEvent.click(await screen.findByRole("button", { name: "Load more" }));
    await waitFor(() => expect(within(region).getByRole("row", { name: "Budget.xlsx" })).toBeTruthy());
    expect(within(region).getByRole("row", { name: "Supplier contract.pdf" })).toBeTruthy();
  });
});

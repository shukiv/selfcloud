// SPDX-License-Identifier: AGPL-3.0-or-later

// Starred on the one table (tl4, decision 137): columns, the time column's aria-sort, the Unstar icon, the location, the empty
// state inside the table, the bulk Unstar and the name link.
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { folders } from "../lib/files/folders";
import type { FolderAccess } from "../lib/files/types";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { desktopViewport } from "../test/viewport";
import StarredPage from "./StarredPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

// starred is a starred item as the server lists it. place is set when the parent is one the caller reaches, and left out for a top-level item.
const starred = (id: string, name: string, over: Record<string, unknown> = {}) => ({
  id,
  folder_id: "fld_a",
  parent_id: "root_a",
  name,
  kind: "file",
  size: 2048,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "viewer",
  owner_id: "usr_a",
  no_download: false,
  editors_can_share: false,
  starred_at: "2026-10-03T00:00:00Z",
  ...over,
});

const SPACE = { id: "fld_a", name: "Finance", kind: "team", root_node_id: "root_a" } as unknown as FolderAccess;

function renderPage() {
  return render(
    <QueryClientProvider client={createQueryClient()}>
      <StarredPage />
    </QueryClientProvider>,
  );
}

const STARRED = "/api/v1/starred";

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

// table waits for the page's table, which is drawn once its strings and its first page have arrived.
const table = async () => screen.findByRole("region", { name: "Starred" });

describe("Starred is one table (tl4, decision 137)", () => {
  it("draws the columns Name, Location, Starred on and Size", async () => {
    stubFetch((c: Call) => (c.url.startsWith(STARRED) ? { status: 200, body: { items: [starred("nod_s1", "Budget.pdf", { parent_id: "dir_b", place: "Budget" })] } } : fail(404, "not_found")));
    renderPage();
    const region = await table();
    // The secondary columns show once the breakpoint is known, which is after the table's first draw.
    await screen.findByRole("columnheader", { name: "Location" });
    const heads = within(region).getAllByRole("columnheader").map((h) => h.textContent ?? "");
    expect(heads).toEqual(expect.arrayContaining(["Name", "Location", "Starred on", "Size"]));
  });

  it("marks Starred on as the time column the rows are in order of (aria-sort descending), with no sort button", async () => {
    stubFetch((c: Call) => (c.url.startsWith(STARRED) ? { status: 200, body: { items: [starred("nod_s1", "Budget.pdf")] } } : fail(404, "not_found")));
    renderPage();
    await table();
    const when = screen.getByRole("columnheader", { name: "Starred on" });
    expect(when.getAttribute("aria-sort")).toBe("descending");
    expect(within(when).queryByRole("button")).toBeNull();
  });

  it("gives each row an icon named after its item, and shows where the item is", async () => {
    stubFetch((c: Call) =>
      c.url.startsWith(STARRED)
        ? {
            status: 200,
            body: {
              items: [starred("nod_s1", "Budget.pdf", { parent_id: "dir_b", place: "Budget" }), starred("nod_s2", "Plan.doc", { parent_id: "root_a" })],
            },
          }
        : fail(404, "not_found"),
    );
    renderPage();
    const region = await table();
    // The table is drawn before its rows arrive, so the test waits for the first icon.
    await within(region).findByRole("button", { name: "Remove star: Budget.pdf" });
    const row1 = within(region).getByRole("row", { name: "Budget.pdf" });
    expect(row1.textContent).toContain("Budget");
    // A top-level item shows its space: its parent is the space's root, which names nothing.
    const row2 = within(region).getByRole("row", { name: "Plan.doc" });
    expect(row2.textContent).toContain("Finance");
  });

  it("keeps the table on screen when there are no stars, and says so inside the body", async () => {
    stubFetch((c: Call) => (c.url.startsWith(STARRED) ? { status: 200, body: { items: [] } } : fail(404, "not_found")));
    renderPage();
    const region = await table();
    await within(region).findByRole("columnheader", { name: "Starred on" });
    const empty = region.querySelector(".empty");
    expect(empty?.textContent).toContain("Nothing is starred yet");
    expect(region.querySelector("tbody tr.ant-table-placeholder")).toBeTruthy();
  });

  it("opens an item from its name, which is a link to the item", async () => {
    stubFetch((c: Call) => (c.url.startsWith(STARRED) ? { status: 200, body: { items: [starred("nod_s1", "Budget.pdf")] } } : fail(404, "not_found")));
    renderPage();
    const region = await table();
    expect(within(region).getByRole("link", { name: "Budget.pdf" }).getAttribute("href")).toBe("/n/nod_s1");
  });

  it("removes the stars of the selected rows in one bulk action, and only those rows", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url.startsWith(STARRED)) return { status: 200, body: { items: [starred("nod_s1", "Budget.pdf"), starred("nod_s2", "Plan.doc"), starred("nod_s3", "Notes.txt")] } };
      if (c.method === "DELETE" && c.url.endsWith("/star")) return { status: 204 };
      return fail(404, "not_found");
    });
    renderPage();
    const region = await table();
    fireEvent.click(within(region).getByRole("checkbox", { name: "Select Budget.pdf" }));
    fireEvent.click(within(region).getByRole("checkbox", { name: "Select Plan.doc" }));
    const bar = await waitFor(() => {
      const b = within(region).getByRole("button", { name: "Remove star" });
      return b;
    });
    fireEvent.click(bar);
    await waitFor(() => expect(within(region).queryByRole("row", { name: "Budget.pdf" })).toBeNull());
    expect(within(region).queryByRole("row", { name: "Plan.doc" })).toBeNull();
    expect(within(region).getByRole("row", { name: "Notes.txt" })).toBeTruthy();
    const deletes = calls.filter((c) => c.method === "DELETE").map((c) => c.url);
    expect(deletes.sort()).toEqual(["/api/v1/nodes/nod_s1/star", "/api/v1/nodes/nod_s2/star"]);
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

// Search results on the one table (tl4, decision 137): columns Name (with the snippet as a second line), Location, Modified and Size;
// relevance order, so no time column is marked as sorted; no selection; an item opens from its name; the menu is the ⋮ button.
import { render, screen, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../lib/session";
import { folders } from "../lib/files/folders";
import type { FolderAccess } from "../lib/files/types";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { desktopViewport } from "../test/viewport";
import SearchPage from "./SearchPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const hit = (id: string, name: string, extra: Record<string, unknown> = {}) => ({
  id,
  folder_id: "fld_a",
  parent_id: "root_a",
  name,
  kind: "file",
  size: 2048,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-02T00:00:00Z",
  role: "viewer",
  owner_id: "usr_a",
  no_download: false,
  editors_can_share: false,
  ...extra,
});

const SPACE = { id: "fld_a", name: "Finance", kind: "team", root_node_id: "root_a" } as unknown as FolderAccess;

function renderAt(url: string) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <Routes>
        <Route path="/search" element={<SearchPage />} />
      </Routes>
    </MemoryRouter>,
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

const FIELDS = { items: [] };

function answer(items: unknown[]) {
  return stubFetch((c: Call) => {
    if (c.url.startsWith("/api/v1/label-fields")) return { status: 200, body: FIELDS };
    if (c.url.startsWith("/api/v1/search")) return { status: 200, body: { items, next_cursor: undefined } };
    return fail(404, "not_found");
  });
}

// results waits for the one row the test expects, since the table is drawn with its empty body for the moment before a search's
// answer arrives, and then returns the table.
const results = async (row = "Budget 2027.xlsx") => {
  await screen.findByRole("row", { name: row });
  return screen.getByRole("region", { name: "Search results" });
};

describe("Search results are one table (tl4, decision 137)", () => {
  it("draws the columns Name, Location, Modified and Size", async () => {
    answer([hit("nod_b", "Budget 2027.xlsx", { place: "Finance", parent_id: "dir_f" })]);
    renderAt("/search?q=budget");
    const region = await results();
    const heads = within(region).getAllByRole("columnheader").map((h) => h.textContent ?? "");
    expect(heads).toEqual(expect.arrayContaining(["Name", "Location", "Modified", "Size"]));
  });

  it("shows the snippet as a muted second line under the name, as text with its matches marked", async () => {
    const text = "the budget for the year";
    answer([hit("nod_b", "Budget 2027.xlsx", { snippet: { text, ranges: [[4, 10]] } })]);
    renderAt("/search?q=budget");
    const region = await results();
    const row = within(region).getByRole("row", { name: "Budget 2027.xlsx" });
    expect(row.textContent).toContain(text);
    expect(row.querySelector("mark")?.textContent).toBe("budget");
  });

  it("marks no column as sorted: the results come in relevance order", async () => {
    answer([hit("nod_b", "Budget 2027.xlsx")]);
    renderAt("/search?q=budget");
    const region = await results();
    for (const h of within(region).getAllByRole("columnheader")) expect(h.getAttribute("aria-sort")).toBeNull();
  });

  it("offers no selection", async () => {
    answer([hit("nod_b", "Budget 2027.xlsx")]);
    renderAt("/search?q=budget");
    const region = await results();
    expect(within(region).queryByRole("checkbox")).toBeNull();
  });

  it("opens an item from its name, and puts its actions behind a ⋮ button named for the row", async () => {
    answer([hit("nod_b", "Budget 2027.xlsx")]);
    renderAt("/search?q=budget");
    const region = await results();
    expect(within(region).getByRole("link", { name: "Budget 2027.xlsx" }).getAttribute("href")).toBe("/n/nod_b");
    expect(within(region).getByRole("button", { name: "More actions for Budget 2027.xlsx" }).getAttribute("aria-haspopup")).toBe("menu");
  });
});

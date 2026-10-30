// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient, queryKey } from "../lib/query";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import SharedPage from "./SharedPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "member", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const item = (id: string, name: string, owner: string) => ({
  id,
  folder_id: "fld_a",
  parent_id: null,
  name,
  kind: "file",
  size: 1,
  mime_type: "text/plain",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-02T00:00:00Z",
  role: "viewer",
  owner_id: owner,
  no_download: false,
  editors_can_share: false,
});

function renderPage() {
  const qc = createQueryClient();
  const view = render(
    <QueryClientProvider client={qc}>
      <SharedPage />
    </QueryClientProvider>,
  );
  return { qc, ...view };
}

beforeEach(() => {
  session.set(signedAs("olivia"));
});

afterEach(() => {
  session.set(null);
});

describe("shared with me", () => {
  it("lists the items in the order the server gives, and names the owner only when it is the caller", async () => {
    stubFetch((c: Call) =>
      c.url.startsWith("/api/v1/shared")
        ? { status: 200, body: { items: [item("nod_1", "Mine.txt", "olivia"), item("nod_2", "Theirs.txt", "mateo")], next_cursor: null } }
        : fail(404, "not_found"),
    );
    renderPage();
    const list = await screen.findByRole("list", { name: "Shared with me" });
    const rows = within(list).getAllByRole("listitem");
    expect(rows.map((r) => r.textContent)).toEqual([expect.stringContaining("Mine.txt"), expect.stringContaining("Theirs.txt")]);
    expect(within(rows[0]).getByText("You")).toBeTruthy();
    expect(within(rows[1]).queryByText("You")).toBeNull();
  });

  it("shows the empty state when nothing is shared", async () => {
    stubFetch(() => ({ status: 200, body: { items: [], next_cursor: null } }));
    renderPage();
    expect(await screen.findByText("Nothing is shared with you yet. Ask an owner of your organisation to share a folder.")).toBeTruthy();
  });

  it("a signed-out page reads nothing (guard: enabled on the session)", async () => {
    session.set(null);
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [], next_cursor: null } }));
    renderPage();
    await act(async () => {
      await new Promise((r) => setTimeout(r, 20));
    });
    expect(calls).toHaveLength(0);
  });

  it("keeps the list on screen when a refresh fails, and shows the failure above it (guard: error only without data)", async () => {
    let fresh = true;
    stubFetch(() => {
      if (fresh) return { status: 200, body: { items: [item("nod_1", "Kept.txt", "mateo")], next_cursor: null } };
      return fail(500, "internal");
    });
    const { qc } = renderPage();
    await screen.findByText(/Kept\.txt/);
    fresh = false;
    await act(async () => {
      await qc.invalidateQueries({ queryKey: queryKey("shared") }).catch(() => undefined);
    });
    await waitFor(() => expect(screen.getByRole("alert")).toBeTruthy(), { timeout: 4000 });
    expect(screen.getByText(/Kept\.txt/)).toBeTruthy();
  });
});

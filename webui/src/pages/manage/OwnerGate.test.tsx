// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import OverviewPage from "./OverviewPage";
import PeoplePage from "./PeoplePage";
import GroupsPage from "./GroupsPage";
import SpacesPage from "./SpacesPage";
import StoragePage from "./StoragePage";
import LinksPage from "./LinksPage";
import AuditPage from "./AuditPage";

// The owner pages are for an account owner. Each page is rendered for each other kind of person, and for nobody: the
// not-found view shows, and no request leaves the page (App.tsx makes the same check for the address).
const PAGES: Array<[string, () => JSX.Element]> = [
  ["overview", OverviewPage],
  ["people", PeoplePage],
  ["groups", GroupsPage],
  ["spaces", SpacesPage],
  ["storage", StoragePage],
  ["links", LinksPage],
  ["audit", AuditPage],
];

function as(role: string, account: { id: string; name: string } | null): Session {
  return {
    user: { id: "usr_1", email: "x@example.com", display_name: "X", role, locale: "en", status: "active" },
    account,
    csrf: "csrf-1",
    publicLinks: false,
  } as unknown as Session;
}

afterEach(() => session.set(null));

describe.each(PAGES)("the owner page %s", (_name, Page) => {
  it.each([
    ["a member", as("member", { id: "acc_1", name: "Acme" })],
    ["an installation admin", as("admin", null)],
    ["nobody (no session)", null],
  ])("shows the not-found view to %s and sends no request (guard: OwnerOnly around the page)", async (_who, who) => {
    const { calls } = stubFetch((c: Call) => (c.url.startsWith("/api/") ? fail(404, "not_found") : fail(404, "not_found")));
    session.set(who);
    const qc = createQueryClient();
    render(
      <QueryClientProvider client={qc}>
        <Page />
      </QueryClientProvider>,
    );
    expect(await screen.findByText("Page not found")).toBeTruthy();
    await new Promise((r) => setTimeout(r, 30));
    expect(calls).toEqual([]);
  });

  it("an owner gets the page and its first request (guard: the role check is not inverted)", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url.includes("/usage")) return { status: 200, body: { quota_bytes: null, used_bytes: 0, reserved_bytes: 0, available_bytes: null, files: 0, folders: 0, users_active: 1, users_pending: 0 } };
      return { status: 200, body: { items: [], next_cursor: undefined, names: undefined, quota_bytes: null, users: [], folders: [], account: { used_bytes: 0, reserved_bytes: 0, quota_bytes: null, available_bytes: null, user_quota_bytes: 0 }, quota: null, anyone_links: 0, public_links: false } };
    });
    session.set(as("owner", { id: "acc_1", name: "Acme" }));
    const qc = createQueryClient();
    render(
      <QueryClientProvider client={qc}>
        <Page />
      </QueryClientProvider>,
    );
    await waitFor(() => expect(calls.length).toBeGreaterThan(0));
    expect(screen.queryByText("Page not found")).toBeNull();
  });
});

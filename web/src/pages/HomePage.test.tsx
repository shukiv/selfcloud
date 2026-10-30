// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient, queryKey } from "../lib/query";
import { session, type Session } from "../lib/session";
import { myStorage } from "../lib/storage/api";
import { translate } from "../lib/i18n";
import { fail, stubFetch, type Answer, type Call } from "../test/fetchStub";
import HomePage from "./HomePage";

function signedAs(id: string, role: "owner" | "member" | "admin" = "member"): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role, locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

function node(id: string, name: string, extra: Record<string, unknown> = {}) {
  return {
    id,
    folder_id: "fld_a",
    parent_id: null,
    name,
    kind: "file",
    size: 10,
    mime_type: "text/plain",
    created_at: "2026-10-01T00:00:00Z",
    modified_at: "2026-10-01T00:00:00Z",
    role: "owner",
    owner_id: "usr_a",
    no_download: false,
    editors_can_share: false,
    ...extra,
  };
}

const recentItem = (id: string, name: string) =>
  node(id, name, { viewed_at: "2026-10-02T00:00:00Z", edited_at: null, last_at: "2026-10-02T00:00:00Z" });
const starredItem = (id: string, name: string) => node(id, name, { starred_at: "2026-10-03T00:00:00Z" });
const sharedItem = (id: string, name: string) => node(id, name, { shared_at: "2026-10-04T00:00:00Z" });

function renderPage(qc = createQueryClient()) {
  return render(
    <QueryClientProvider client={qc}>
      <HomePage />
    </QueryClientProvider>,
  );
}

// the three lists and the requests and storage reads; each answer comes from the given table, by path.
function answers(table: Record<string, () => Answer | Promise<Answer>>) {
  return (c: Call): Answer | Promise<Answer> => {
    const path = c.url.split("?")[0];
    const hit = table[path];
    if (hit) return hit();
    if (path === "/api/v1/me") return { status: 200, body: { user: {}, storage: null } };
    return fail(404, "not_found");
  };
}

beforeEach(() => {
  session.set(signedAs("olivia", "member"));
});

afterEach(() => {
  session.set(null);
  myStorage.set(null);
});

describe("Home", () => {
  it("shows the recent, starred and shared items, each opened through its address", async () => {
    stubFetch(
      answers({
        "/api/v1/recent": () => ({ status: 200, body: { items: [recentItem("nod_r1", "Plan.txt")] } }),
        "/api/v1/starred": () => ({ status: 200, body: { items: [starredItem("nod_s1", "Budget.pdf")] } }),
        "/api/v1/shared": () => ({ status: 200, body: { items: [sharedItem("nod_h1", "Shared.doc")] } }),
        "/api/v1/access-requests": () => ({ status: 200, body: { items: [] } }),
      }),
    );
    renderPage();
    expect((await screen.findByRole("link", { name: "Plan.txt" })).getAttribute("href")).toBe("/n/nod_r1");
    expect(screen.getByRole("link", { name: "Budget.pdf" }).getAttribute("href")).toBe("/n/nod_s1");
    expect(screen.getByRole("link", { name: "Shared.doc" }).getAttribute("href")).toBe("/n/nod_h1");
  });

  it("an installation admin sends no request and shows no section (role refusal, guard: the admin check before the queries)", async () => {
    session.set(signedAs("ada", "admin"));
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderPage();
    await new Promise((r) => setTimeout(r, 20));
    expect(calls.length).toBe(0);
    expect(screen.queryByRole("heading", { name: translate("en", "home.recent") })).toBeNull();
    expect(screen.getByRole("heading", { name: translate("en", "nav.home") })).toBeTruthy();
  });

  it("a failed first load shows its error, and never the empty state (guard: empty only when the read succeeded)", async () => {
    stubFetch(
      answers({
        "/api/v1/recent": () => fail(500, "internal"),
        "/api/v1/starred": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/shared": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/access-requests": () => ({ status: 200, body: { items: [] } }),
      }),
    );
    renderPage();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toBe("");
    // The starred list is empty and says so; the recent section, which failed, does not.
    await waitFor(() => expect(screen.getByText(translate("en", "starred.empty"))).toBeTruthy());
    expect(screen.queryByText(translate("en", "recent.empty"))).toBeNull();
  });

  it("while the first read is pending, no section shows its empty state (guard: empty only after a successful read)", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    stubFetch(
      answers({
        "/api/v1/recent": async () => {
          await gate;
          return { status: 200, body: { items: [] } };
        },
        "/api/v1/starred": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/shared": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/access-requests": () => ({ status: 200, body: { items: [] } }),
      }),
    );
    renderPage();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByText(translate("en", "recent.empty"))).toBeNull();
    release();
    expect(await screen.findByText(translate("en", "recent.empty"))).toBeTruthy();
  });

  it("a refetch that fails keeps the list shown, with the error beside it (guard: data is kept on a failed refetch)", async () => {
    let fail_next = false;
    stubFetch(
      answers({
        "/api/v1/recent": () => (fail_next ? fail(500, "internal") : { status: 200, body: { items: [recentItem("nod_r1", "Plan.txt")] } }),
        "/api/v1/starred": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/shared": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/access-requests": () => ({ status: 200, body: { items: [] } }),
      }),
    );
    const qc = createQueryClient();
    renderPage(qc);
    await screen.findByRole("link", { name: "Plan.txt" });
    fail_next = true;
    await qc.refetchQueries({ queryKey: queryKey("home", "recent") });
    const alert = await screen.findByRole("alert");
    expect(alert.className).toContain("ant-alert-error");
    expect(alert.className).not.toContain("ant-alert-warning");
    expect(screen.getByRole("link", { name: "Plan.txt" })).toBeTruthy();
  });

  it("the storage notice shows from 80% of a limit, with a link to Storage (guard: the 80% threshold)", async () => {
    stubFetch(
      answers({
        "/api/v1/recent": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/starred": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/shared": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/access-requests": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/me": () => ({ status: 200, body: { user: {}, storage: { used_bytes: 900, reserved_bytes: 0, quota_bytes: 1000 } } }),
      }),
    );
    renderPage();
    expect(await screen.findByText(translate("en", "home.storageNotice", { percent: "90" }))).toBeTruthy();
    expect(screen.getByRole("link", { name: translate("en", "home.storageLink") }).getAttribute("href")).toBe("/storage");
    expect(screen.getByRole("meter").getAttribute("aria-valuemax")).toBe("1000");
  });

  it("no storage notice below 80% of the limit", async () => {
    stubFetch(
      answers({
        "/api/v1/recent": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/starred": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/shared": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/access-requests": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/me": () => ({ status: 200, body: { user: {}, storage: { used_bytes: 500, reserved_bytes: 0, quota_bytes: 1000 } } }),
      }),
    );
    renderPage();
    await waitFor(() => expect(myStorage).toBeTruthy());
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole("meter")).toBeNull();
  });

  it("a session change shows the new person's items, never the last person's (guard: the query key starts with the session)", async () => {
    let who = "ann";
    stubFetch(
      answers({
        "/api/v1/recent": () => ({ status: 200, body: { items: [recentItem(`nod_${who}`, `${who}-file.txt`)] } }),
        "/api/v1/starred": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/shared": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/access-requests": () => ({ status: 200, body: { items: [] } }),
      }),
    );
    renderPage();
    await screen.findByRole("link", { name: "ann-file.txt" });
    who = "bob";
    session.set(signedAs("bob", "member"));
    await screen.findByRole("link", { name: "bob-file.txt" });
    expect(screen.queryByRole("link", { name: "ann-file.txt" })).toBeNull();
  });

  it("an answer that arrives after the session changed never shows (a late answer)", async () => {
    let release!: () => void;
    const gate = new Promise<void>((r) => (release = r));
    let reads = 0;
    stubFetch(
      answers({
        "/api/v1/recent": async () => {
          // The first read is held until the session has changed; the next person's read answers at once.
          reads++;
          if (reads === 1) await gate;
          return { status: 200, body: { items: reads === 1 ? [recentItem("nod_late", "late.txt")] : [] } };
        },
        "/api/v1/starred": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/shared": () => ({ status: 200, body: { items: [] } }),
        "/api/v1/access-requests": () => ({ status: 200, body: { items: [] } }),
      }),
    );
    renderPage();
    session.set(signedAs("bob", "member"));
    release();
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole("link", { name: "late.txt" })).toBeNull();
  });
});


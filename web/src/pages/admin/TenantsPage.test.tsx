// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { account, signedAs, usage, LINK } from "../../test/adminFixtures";
import { expectEmptyTable } from "../../test/emptyTable";
import TenantsPage from "./TenantsPage";

// A client that does not retry, so a refused read shows at once.
function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = render(
    <App>
      <QueryClientProvider client={qc}>
        <TenantsPage />
      </QueryClientProvider>
    </App>,
  );
  return { qc, ...view };
}

const path = (c: Call) => new URL(c.url, "http://x");

// setField types into a dialog's field by its id, the ids the Svelte pages used and the e2e suite names.
function setField(id: string, value: string) {
  fireEvent.change(document.getElementById(id) as HTMLInputElement, { target: { value } });
}

beforeEach(() => {
  session.set(signedAs("admin"));
});

afterEach(() => {
  session.set(null);
  // The tenants list writes its search and filter into the address; the next test must start from the bare list.
  window.history.replaceState(null, "", "/");
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("who sees the tenants list", () => {
  it.each(["owner", "member"] as const)("a %s sees the not-found view and no request is sent", async (role) => {
    session.set(signedAs(role));
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [account()] } }));
    renderPage();
    expect(await screen.findByText("Page not found")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Tenants" })).toBeNull();
    expect(calls).toEqual([]);
  });

  it("an admin reads the list and shows each tenant with its status and its usage", async () => {
    stubFetch((c) => {
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      return fail(404, "not_found");
    });
    renderPage();
    const list = await screen.findByRole("region", { name: "Tenants" });
    expect(within(list).getByText("Acme Ltd")).toBeTruthy();
    expect(within(list).getByText("Active")).toBeTruthy();
    expect(await screen.findByRole("meter")).toBeTruthy();
  });

  // tl1: the list carries each tenant's usage (used_bytes and quota_bytes, decision A1), so the page makes no request per row.
  it("the list makes no usage request for a tenant (the usage comes with the account)", async () => {
    const { calls } = stubFetch((c) => (path(c).pathname === "/api/v1/accounts" ? { status: 200, body: { items: [account()] } } : fail(404, "not_found")));
    renderPage();
    await screen.findByRole("region", { name: "Tenants" });
    await screen.findByRole("meter");
    expect(calls.some((c) => path(c).pathname.endsWith("/usage"))).toBe(false);
  });

  // The list's order is the server's: Name is the default, asc, and a header click asks the server for the other key.
  it("Name is the default sort, and a click on Created asks the server to sort by created", async () => {
    // A desktop: Created is a column with a header button here, and a phone line below md.
    vi.stubGlobal("matchMedia", (query: string) => ({ matches: /min-width: (\d+)px/.test(query) && Number(/min-width: (\d+)px/.exec(query)?.[1]) <= 1200, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }));
    const { calls } = stubFetch((c) => (path(c).pathname === "/api/v1/accounts" ? { status: 200, body: { items: [account()] } } : fail(404, "not_found")));
    renderPage();
    const list = await screen.findByRole("region", { name: "Tenants" });
    expect(calls.some((c) => path(c).searchParams.get("sort") === "name" && path(c).searchParams.get("order") === "asc")).toBe(true);
    expect(within(list).getByRole("columnheader", { name: /Name/ }).getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(within(list).getByRole("button", { name: "Created" }));
    await waitFor(() => expect(calls.some((c) => path(c).searchParams.get("sort") === "created")).toBe(true));
    expect(within(screen.getByRole("region", { name: "Tenants" })).getByRole("columnheader", { name: /Created/ }).getAttribute("aria-sort")).toBe("ascending");
  });
});

describe("a failed read", () => {
  it("a failed first load shows the error and no empty state (guard: firstFailed)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderPage();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByText(/No tenants yet/)).toBeNull();
    expect(screen.queryByText(/No tenant matches/)).toBeNull();
  });

  it("a failed load of the next page keeps the list that is on screen (guard: list kept)", async () => {
    stubFetch((c) => {
      if (path(c).pathname !== "/api/v1/accounts") return { status: 200, body: usage() };
      if (path(c).searchParams.get("cursor") === "next") return fail(500, "internal");
      return { status: 200, body: { items: [account()], next_cursor: "next" } };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.getByText("Acme Ltd")).toBeTruthy();
  });

  // The owner's rule of 2026-11-09 (decision 114): the empty table keeps its headers, and its Empty offers New tenant; a
  // filter that matches nothing says so, with no action.
  it("an empty list keeps its header row, and its Empty in the table offers New tenant, which opens the form", async () => {
    vi.stubGlobal("matchMedia", (query: string) => ({ matches: /min-width: (\d+)px/.test(query) && Number(/min-width: (\d+)px/.exec(query)?.[1]) <= 1200, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }));
    stubFetch((c) => (path(c).pathname === "/api/v1/accounts" ? { status: 200, body: { items: [] } } : { status: 200, body: usage() }));
    renderPage();
    const add = await expectEmptyTable("Tenants", {
      headers: ["Name", "Status", "Storage", "Users", "Created"],
      title: "No tenants yet.",
      description: "A tenant is an organisation with its own users and folders.",
      action: "New tenant",
    });
    fireEvent.click(add as HTMLElement);
    await waitFor(() => expect(document.getElementById("new-tenant-name")).toBeTruthy());
    vi.unstubAllGlobals();
  });

  it("the status filter is sent to the server and shown as pressed", async () => {
    const { calls } = stubFetch((c) => (path(c).pathname === "/api/v1/accounts" ? { status: 200, body: { items: [] } } : { status: 200, body: usage() }));
    renderPage();
    await screen.findByText(/No tenants yet/);
    fireEvent.click(screen.getByRole("button", { name: "Suspended" }));
    await waitFor(() => expect(calls.some((c) => path(c).searchParams.get("status") === "suspended")).toBe(true));
    expect(screen.getByRole("button", { name: "Suspended" }).getAttribute("aria-pressed")).toBe("true");
    // Nothing matches the filter: the table says so, and offers no New tenant in its body.
    const body = (await screen.findByRole("region", { name: "Tenants" })).querySelector("tbody") as HTMLElement;
    await waitFor(() => expect(body.querySelector(".empty-title")?.textContent).not.toBe("No tenants yet."));
    expect(body.querySelector(".empty-desc")).toBeNull();
    expect(within(body).queryByRole("button")).toBeNull();
  });
});

describe("a row's name and its actions", () => {
  // The name is the link to the tenant, so a middle click opens it in a new tab (a real anchor).
  it("the tenant's name is a link to the tenant", async () => {
    stubFetch((c) => (path(c).pathname === "/api/v1/accounts" ? { status: 200, body: { items: [account()] } } : { status: 200, body: usage() }));
    renderPage();
    const name = await screen.findByRole("link", { name: "Acme Ltd" });
    expect(name.getAttribute("href")).toBe("/admin/tenants/acc_1");
  });

  // The ⋮ menu holds Open, Suspend and Delete; there is no inline icon on a tenant row (design-tl section 3.1).
  it("the ⋮ menu holds Open, Suspend and Delete tenant", async () => {
    stubFetch((c) => (path(c).pathname === "/api/v1/accounts" ? { status: 200, body: { items: [account()] } } : { status: 200, body: usage() }));
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    expect(await screen.findByRole("menuitem", { name: "Open" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Suspend" })).toBeTruthy();
    expect(screen.getByRole("menuitem", { name: "Delete tenant…" })).toBeTruthy();
  });

  // Review L2: Open in the ⋮ menu is a real link, so a middle or modified click opens the tenant in a new tab.
  it("Open in the ⋮ menu is a link to the tenant", async () => {
    stubFetch((c) => (path(c).pathname === "/api/v1/accounts" ? { status: 200, body: { items: [account()] } } : { status: 200, body: usage() }));
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    const open = await screen.findByRole("menuitem", { name: "Open" });
    expect(within(open).getByRole("link", { name: "Open" }).getAttribute("href")).toBe("/admin/tenants/acc_1");
  });
});

// Review M3: the Users column of the approved mockup, read from user_count on each account of the list (no request per row).
describe("the tenants table's Users column", () => {
  it("shows each tenant's number of users, from the list itself", async () => {
    vi.stubGlobal("matchMedia", (query: string) => ({ matches: /min-width: (\d+)px/.test(query) && Number(/min-width: (\d+)px/.exec(query)?.[1]) <= 1200, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false }));
    const { calls } = stubFetch((c) =>
      path(c).pathname === "/api/v1/accounts"
        ? { status: 200, body: { items: [account({ user_count: 14 }), account({ id: "acc_2", name: "Beta", user_count: 0 })] } }
        : fail(404, "not_found"),
    );
    renderPage();
    const list = await screen.findByRole("region", { name: "Tenants" });
    const heads = within(list).getAllByRole("columnheader").map((h) => h.textContent);
    expect(heads).toContain("Users");
    const col = heads.indexOf("Users");
    const cell = (name: string) => (list.querySelector(`tr[aria-label="${name}"]`) as HTMLElement).querySelectorAll("td")[col].textContent;
    expect(cell("Acme Ltd")).toBe("14");
    expect(cell("Beta")).toBe("0");
    expect(calls.filter((c) => path(c).pathname !== "/api/v1/accounts")).toEqual([]);
  });
});

describe("a change of person while a change is out", () => {
  it("a suspend that never answers: Cancel closes the dialog and aborts the request (fu4, item 2)", async () => {
    const { calls } = stubFetch((c) => {
      if (c.method === "PATCH") return new Promise<Answer>(() => undefined);
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Suspend" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Suspend" }));
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.find((c) => c.method === "PATCH")?.signal?.aborted).toBe(true);
  });

  it("the answer to a suspend that arrives after the person changed is not shown (guard: sessionChanged in confirmSuspend)", async () => {
    let release!: (a: { status: number; body?: unknown }) => void;
    const gate = new Promise<{ status: number; body?: unknown }>((resolve) => {
      release = resolve;
    });
    stubFetch((c) => {
      if (c.method === "PATCH") return gate;
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Suspend" }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Suspend" }));
    act(() => {
      session.set(signedAs("admin", "admin_2"));
    });
    await act(async () => {
      release({ status: 200, body: account({ status: "suspended" }) });
      await gate;
    });
    await waitFor(() => expect(screen.queryByRole("status", { name: /suspend/i })).toBeNull());
    expect(document.body.textContent).not.toContain("Acme Ltd is suspended");
  });
});

describe("suspend", () => {
  it("asks first, and a double click sends one request (guard: useGuard in confirmSuspend)", async () => {
    let suspendCalls = 0;
    const { calls } = stubFetch((c) => {
      if (c.method === "PATCH") {
        suspendCalls++;
        return { status: 200, body: account({ status: "suspended" }) };
      }
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Suspend" }));
    const dialog = await screen.findByRole("dialog");
    const go = within(dialog).getByRole("button", { name: "Suspend" });
    fireEvent.click(go);
    fireEvent.click(go);
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("suspended"));
    expect(suspendCalls).toBe(1);
    expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(1);
  });

  it("a refused suspend keeps the dialog and shows the code's text, never the server's (guard: failureText)", async () => {
    stubFetch((c) => {
      if (c.method === "PATCH") return fail(403, "forbidden", { text: "SERVER-SECRET-TEXT" });
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Suspend" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Suspend" }));
    await waitFor(() => expect(within(screen.getByRole("dialog")).getByRole("alert").textContent).toContain("not allowed"));
    expect(document.body.textContent).not.toContain("SERVER-SECRET-TEXT");
  });
});

describe("the owner link of a new tenant", () => {
  // The link needs tenant access (decision 41), and the admin's password for the prompt that asks for it (decision 84).
  beforeEach(() => {
    session.set({ ...signedAs("admin"), tenantAccess: true, mailConfigured: true } as Session);
  });

  it("is shown once, is kept nowhere but the dialog, and is gone when it closes (guard: ShowOnce state only)", async () => {
    const { qc } = (() => {
      stubFetch((c) => {
        if (c.method === "POST" && path(c).pathname === "/api/v1/accounts") {
          return { status: 201, body: { ...account({ id: "acc_9", name: "New Co" }), owner: { id: "usr_9", email: "o@example.com", display_name: "", locale: "en", role: "owner", status: "pending", verified_at: null, created_at: "", updated_at: "", verification: { method: "link", url: LINK } } } };
        }
        if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [] } };
        return { status: 200, body: usage() };
      });
      return renderPage();
    })();
    await screen.findByText(/No tenants yet/);
    fireEvent.click(screen.getAllByRole("button", { name: "New tenant" })[0]);
    await screen.findByRole("dialog");
    setField("new-tenant-name", "New Co");
    setField("new-tenant-owner-email", "o@example.com");
    setField("new-tenant-limit", "5");
    fireEvent.click(screen.getByRole("radio", { name: /link/i }));
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
    setField("new-tenant-admin", "admin-pass-1");
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    const shown = (await screen.findByDisplayValue(LINK)) as HTMLInputElement;
    expect(shown.id).toBe("tenant-link-value");
    expect(JSON.stringify(qc.getQueryCache().getAll().map((q) => q.state.data))).not.toContain("SECRET-ONE-TIME-VALUE");
    expect(window.location.href).not.toContain("SECRET-ONE-TIME-VALUE");
    fireEvent.click(screen.getByRole("button", { name: "I have copied it" }));
    await waitFor(() => expect(screen.queryByDisplayValue(LINK)).toBeNull());
    expect(document.body.textContent).not.toContain("SECRET-ONE-TIME-VALUE");
  });

  it("a session change clears the link at once (guard: useOnSessionChange)", async () => {
    stubFetch((c) => {
      if (c.method === "POST") return { status: 201, body: { ...account({ id: "acc_9", name: "New Co" }), owner: { id: "u", email: "o@example.com", display_name: "", locale: "en", role: "owner", status: "pending", verified_at: null, created_at: "", updated_at: "", verification: { method: "link", url: LINK } } } };
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [] } };
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText(/No tenants yet/);
    fireEvent.click(screen.getAllByRole("button", { name: "New tenant" })[0]);
    await screen.findByRole("dialog");
    setField("new-tenant-name", "New Co");
    setField("new-tenant-owner-email", "o@example.com");
    setField("new-tenant-limit", "5");
    fireEvent.click(screen.getByRole("radio", { name: /link/i }));
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
    setField("new-tenant-admin", "admin-pass-1");
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByDisplayValue(LINK);
    act(() => {
      session.set(signedAs("admin", "admin_2"));
    });
    await waitFor(() => expect(screen.queryByDisplayValue(LINK)).toBeNull());
  });
});

describe("a session swap", () => {
  it("the tenants of the person who signed out are not shown to the next one (guard: queryKey carries the session)", async () => {
    let next = false;
    stubFetch((c) => {
      if (path(c).pathname === "/api/v1/accounts") {
        return { status: 200, body: { items: [next ? account({ id: "acc_2", name: "Other Ltd" }) : account()] } };
      }
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    next = true;
    act(() => {
      session.set(signedAs("admin", "admin_2"));
    });
    expect(await screen.findByText("Other Ltd")).toBeTruthy();
    expect(screen.queryByText("Acme Ltd")).toBeNull();
  });
});

describe("delete", () => {
  it("the typed name is required, and a double click deletes once (guard: typedNameMatches; the second click is stopped by useGuard, the started ref is not proven here)", async () => {
    let deletes = 0;
    stubFetch((c) => {
      if (c.method === "DELETE") {
        deletes++;
        return { status: 202, body: { id: "job_1", kind: "account.delete", target: "acc_1", status: "queued", created_at: "", finished_at: null } };
      }
      if (path(c).pathname === "/api/v1/jobs/job_1") return { status: 200, body: { id: "job_1", kind: "account.delete", target: "acc_1", status: "succeeded", created_at: "", finished_at: null } };
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Delete tenant…" }));
    const dialog = await screen.findByRole("dialog");
    const go = within(dialog).getByRole("button", { name: "Delete Acme Ltd permanently" });
    expect((go as HTMLButtonElement).disabled).toBe(true);
    setField("delete-tenant-name", "acme ltd");
    expect((go as HTMLButtonElement).disabled).toBe(true);
    setField("delete-tenant-name", "Acme Ltd");
    fireEvent.click(go);
    fireEvent.click(go);
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("deleted"));
    expect(deletes).toBe(1);
  });

  it("a refused delete keeps the dialog and shows the error by code (guard: failed request keeps dialog)", async () => {
    stubFetch((c) => {
      if (c.method === "DELETE") return fail(500, "internal");
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Delete tenant…" }));
    const dialog = await screen.findByRole("dialog");
    setField("delete-tenant-name", "Acme Ltd");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete Acme Ltd permanently" }));
    await waitFor(() => expect(within(screen.getByRole("dialog")).getByRole("alert").textContent).toContain("The server had a problem"));
    expect(screen.getByRole("dialog")).toBeTruthy();
  });

  it("a deletion that runs cannot be dismissed with Escape, since it cannot be undone (guard: closable false while deleting)", async () => {
    stubFetch((c) => {
      if (c.method === "DELETE") return { status: 202, body: { id: "job_1", kind: "account.delete", target: "acc_1", status: "queued", created_at: "", finished_at: null } };
      if (path(c).pathname === "/api/v1/jobs/job_1") return { status: 200, body: { id: "job_1", kind: "account.delete", target: "acc_1", status: "running", created_at: "", finished_at: null } };
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      return { status: 200, body: usage() };
    });
    renderPage();
    await screen.findByText("Acme Ltd");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Acme Ltd" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Delete tenant…" }));
    const dialog = await screen.findByRole("dialog");
    setField("delete-tenant-name", "Acme Ltd");
    fireEvent.click(within(dialog).getByRole("button", { name: "Delete Acme Ltd permanently" }));
    await waitFor(() => expect(screen.getByRole("dialog").textContent).toContain("Deleting"));
    fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape", code: "Escape" });
    expect(screen.getByRole("dialog").textContent).toContain("Deleting");
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
  });
});

describe("the new tenant form after a change of person", () => {
  // The form holds what the admin typed, so a change of person closes it (guard: useOnSessionChange).
  it("a session change closes the new tenant form with what was typed (guard: the form closes on a session change)", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderPage();
    // The toolbar's New tenant, the first one: an empty table offers its own too (decision 114).
    fireEvent.click((await screen.findAllByRole("button", { name: "New tenant" }))[0]);
    fireEvent.change(document.getElementById("new-tenant-name") as HTMLInputElement, { target: { value: "Typed" } });
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(document.getElementById("new-tenant-name")).toBeNull());
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { account, signedAs, usage, LINK } from "../../test/adminFixtures";
import TenantsPage from "./TenantsPage";

// A client that does not retry, so a refused read shows at once.
function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const view = render(
    <QueryClientProvider client={qc}>
      <TenantsPage />
    </QueryClientProvider>,
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
});

describe("who sees the tenants list", () => {
  it.each(["owner", "member"] as const)("a %s sees the not-found view and no request is sent", async (role) => {
    session.set(signedAs(role));
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [account()] } }));
    renderPage();
    expect(await screen.findByText("Page not found")).toBeTruthy();
    expect(screen.queryByRole("list", { name: "Tenants" })).toBeNull();
    expect(calls).toEqual([]);
  });

  it("an admin reads the list and shows each tenant with its status and its usage", async () => {
    stubFetch((c) => {
      if (path(c).pathname === "/api/v1/accounts") return { status: 200, body: { items: [account()] } };
      if (path(c).pathname === "/api/v1/accounts/acc_1/usage") return { status: 200, body: usage() };
      return fail(404, "not_found");
    });
    renderPage();
    const list = await screen.findByRole("list", { name: "Tenants" });
    expect(within(list).getByText("Acme Ltd")).toBeTruthy();
    expect(within(list).getByText("Active")).toBeTruthy();
    expect(await screen.findByRole("meter")).toBeTruthy();
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

  it("the status filter is sent to the server and shown as pressed", async () => {
    const { calls } = stubFetch((c) => (path(c).pathname === "/api/v1/accounts" ? { status: 200, body: { items: [] } } : { status: 200, body: usage() }));
    renderPage();
    await screen.findByText(/No tenants yet/);
    fireEvent.click(screen.getByRole("button", { name: "Suspended" }));
    await waitFor(() => expect(calls.some((c) => path(c).searchParams.get("status") === "suspended")).toBe(true));
    expect(screen.getByRole("button", { name: "Suspended" }).getAttribute("aria-pressed")).toBe("true");
  });
});

describe("a change of person while a change is out", () => {
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
    fireEvent.click(screen.getByRole("button", { name: "Actions for Acme Ltd" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Actions for Acme Ltd" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Actions for Acme Ltd" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Suspend" }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: "Suspend" }));
    await waitFor(() => expect(within(screen.getByRole("dialog")).getByRole("alert").textContent).toContain("not allowed"));
    expect(document.body.textContent).not.toContain("SERVER-SECRET-TEXT");
  });
});

describe("the owner link of a new tenant", () => {
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
    fireEvent.click(screen.getByRole("button", { name: "New tenant" }));
    await screen.findByRole("dialog");
    setField("new-tenant-name", "New Co");
    setField("new-tenant-owner-email", "o@example.com");
    setField("new-tenant-limit", "5");
    fireEvent.click(screen.getByRole("radio", { name: /link/i }));
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
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
    fireEvent.click(screen.getByRole("button", { name: "New tenant" }));
    await screen.findByRole("dialog");
    setField("new-tenant-name", "New Co");
    setField("new-tenant-owner-email", "o@example.com");
    setField("new-tenant-limit", "5");
    fireEvent.click(screen.getByRole("radio", { name: /link/i }));
    fireEvent.click(screen.getByRole("button", { name: "Create tenant" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Actions for Acme Ltd" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Actions for Acme Ltd" }));
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
    fireEvent.click(screen.getByRole("button", { name: "Actions for Acme Ltd" }));
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
    fireEvent.click(await screen.findByRole("button", { name: "New tenant" }));
    fireEvent.change(document.getElementById("new-tenant-name") as HTMLInputElement, { target: { value: "Typed" } });
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(document.getElementById("new-tenant-name")).toBeNull());
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { account, signedAs, usage } from "../../test/adminFixtures";
import TenantPage from "./TenantPage";

function renderTenant(id = "acc_1") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TenantPage account={id} />
    </QueryClientProvider>,
  );
}

const path = (c: Call) => new URL(c.url, "http://x").pathname;

// The overview tab's reads: usage, team spaces and the storage block. Any other read is an empty list.
function overviewReads(c: Call): { status: number; body: unknown } | null {
  if (path(c).endsWith("/usage")) return { status: 200, body: usage() };
  if (path(c).endsWith("/storage")) return { status: 200, body: { account: { used_bytes: 1, reserved_bytes: 0, user_quota_bytes: 0 }, users: [] } };
  if (path(c).endsWith("/folders")) return { status: 200, body: { items: [] } };
  return null;
}

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => {
  session.set(null);
  window.history.replaceState(null, "", "/");
});

describe("who sees a tenant page", () => {
  it.each(["owner", "member"] as const)("a %s sees the not-found view and no request is sent", async (role) => {
    session.set(signedAs(role));
    const { calls } = stubFetch(() => ({ status: 200, body: account() }));
    renderTenant();
    expect(await screen.findByText("Page not found")).toBeTruthy();
    expect(calls).toEqual([]);
  });
});

describe("a tenant page", () => {
  it("a failed first load shows the error and no tabs (guard: error branch before tenant)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderTenant();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByRole("tablist")).toBeNull();
  });

  it("an unknown tenant says so, with no tabs", async () => {
    stubFetch(() => fail(404, "not_found"));
    renderTenant("acc_missing");
    expect((await screen.findByRole("alert")).textContent).toContain("No such tenant.");
  });

  it("the tab named in the address opens first, and the arrow keys move between tabs", async () => {
    window.history.replaceState(null, "", "/admin/tenants/acc_1?tab=keys");
    stubFetch((c) => {
      if (path(c) === "/api/v1/accounts/acc_1") return { status: 200, body: account() };
      return overviewReads(c) ?? { status: 200, body: { items: [] } };
    });
    renderTenant();
    const keys = await screen.findByRole("tab", { name: "API keys" });
    expect(keys.getAttribute("aria-selected")).toBe("true");
    keys.focus();
    fireEvent.keyDown(keys, { key: "ArrowRight" });
    expect(screen.getByRole("tab", { name: "Audit log" }).getAttribute("aria-selected")).toBe("true");
  });

  it("a double click on suspend sends one request (guard: useGuard in confirmSuspend)", async () => {
    let patches = 0;
    stubFetch((c: Call) => {
      if (c.method === "PATCH") {
        patches++;
        return { status: 200, body: account({ status: "suspended" }) };
      }
      if (path(c) === "/api/v1/accounts/acc_1") return { status: 200, body: account() };
      return overviewReads(c) ?? { status: 200, body: { items: [] } };
    });
    renderTenant();
    fireEvent.click(await screen.findByRole("button", { name: "Suspend" }));
    const dialog = await screen.findByRole("dialog");
    const go = within(dialog).getByRole("button", { name: "Suspend" });
    fireEvent.click(go);
    fireEvent.click(go);
    await waitFor(() => expect(screen.getByRole("status").textContent).toContain("suspended"));
    expect(patches).toBe(1);
  });

  it("a refused rename keeps the form open with the error by code (guard: renameError)", async () => {
    stubFetch((c: Call) => {
      if (c.method === "PATCH") return fail(403, "forbidden", { text: "SERVER-TEXT" });
      if (path(c) === "/api/v1/accounts/acc_1") return { status: 200, body: account() };
      return overviewReads(c) ?? { status: 200, body: { items: [] } };
    });
    renderTenant();
    fireEvent.click(await screen.findByRole("button", { name: "Rename" }));
    const input = document.getElementById("tenant-name") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "Renamed" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("not allowed"));
    expect(document.getElementById("tenant-name")).not.toBeNull();
    expect(document.body.textContent).not.toContain("SERVER-TEXT");
  });

  // A refetch that fails keeps the tenant, but its status may be stale: the error is shown and Suspend, Resume and
  // Delete wait until a read succeeds, because the status decides which of them is right.
  it("a failed refetch keeps the tenant, shows the error, and disables the actions (guard: stale disables the actions)", async () => {
    let broken = false;
    stubFetch((c) => {
      if (path(c) === "/api/v1/accounts/acc_1") return broken ? fail(500, "internal") : { status: 200, body: account() };
      return overviewReads(c) ?? { status: 200, body: {} };
    });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <TenantPage account="acc_1" />
      </QueryClientProvider>,
    );
    await screen.findByText("Acme Ltd");
    broken = true;
    await act(async () => {
      await qc.invalidateQueries();
    });
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.getByText("Acme Ltd")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Suspend" }) as HTMLButtonElement).disabled).toBe(true);
    expect((screen.getByRole("button", { name: "Delete tenant…" }) as HTMLButtonElement).disabled).toBe(true);
  });
});

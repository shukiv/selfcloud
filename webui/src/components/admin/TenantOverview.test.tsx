// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { queryKey } from "../../lib/query";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import TenantOverview from "./TenantOverview";
import { account, signedAs, usage } from "../../test/adminFixtures";

function renderOverview(onChanged = vi.fn()) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TenantOverview account={account()} onChanged={onChanged} />
    </QueryClientProvider>,
  );
  return onChanged;
}

const routes = (c: Call) => {
  if (c.url.endsWith("/usage")) return { status: 200, body: usage() };
  if (c.url.includes("/folders")) return { status: 200, body: { items: [] } };
  if (c.url.endsWith("/storage")) return { status: 200, body: { account: { used_bytes: 1, reserved_bytes: 0, user_quota_bytes: 0 }, users: [], users_next_cursor: null } };
  if (c.url.includes("/share/policy") || c.url.includes("/sharing")) return { status: 200, body: { public_links: false, anyone_links: 0 } };
  return { status: 200, body: {} };
};

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => session.set(null));

describe("tenant overview", () => {
  it("a failed first load shows the error and no figures (guard: failed first load)", async () => {
    stubFetch((c) => (c.url.endsWith("/usage") ? fail(500, "internal") : routes(c)));
    renderOverview();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByText("Storage")).toBeNull();
  });

  it("saves a new limit once and refuses an unreadable one before any request", async () => {
    let patches = 0;
    const { calls } = stubFetch((c) => {
      if (c.method === "PATCH") {
        patches++;
        return { status: 200, body: account({ quota_bytes: 7_000_000_000 }) };
      }
      return routes(c);
    });
    const onChanged = renderOverview();
    await screen.findByText("Storage limit: 5 GB.");
    fireEvent.click(screen.getByRole("button", { name: "Change limit" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Amount" }), { target: { value: "lots" } });
    fireEvent.click(screen.getByRole("button", { name: "Save limit" }));
    expect((await screen.findByRole("alert")).textContent).toContain("Enter a storage limit");
    expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(0);

    fireEvent.change(screen.getByRole("textbox", { name: "Amount" }), { target: { value: "7" } });
    const save = screen.getByRole("button", { name: "Save limit" });
    fireEvent.click(save);
    fireEvent.click(save);
    await waitFor(() => expect(onChanged).toHaveBeenCalledTimes(1));
    expect(patches).toBe(1);
  });

  // A refresh that fails keeps the figures that were read, and says so (not a silent stale page).
  // The tenants list reads each row's usage under the usage key, so a new limit must mark that tenant's usage stale.
  it("a saved limit invalidates the tenant's usage (guard: usage key invalidated on save)", async () => {
    stubFetch((c) => (c.method === "PATCH" ? { status: 200, body: account({ quota_bytes: 7_000_000_000 }) } : routes(c)));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    const spy = vi.spyOn(qc, "invalidateQueries");
    render(
      <QueryClientProvider client={qc}>
        <TenantOverview account={account()} onChanged={vi.fn()} />
      </QueryClientProvider>,
    );
    await screen.findByText("Storage limit: 5 GB.");
    fireEvent.click(screen.getByRole("button", { name: "Change limit" }));
    fireEvent.change(screen.getByRole("textbox", { name: "Amount" }), { target: { value: "7" } });
    fireEvent.click(screen.getByRole("button", { name: "Save limit" }));
    await waitFor(() => expect(spy).toHaveBeenCalledWith({ queryKey: queryKey("usage", "acc_1") }));
  });

  it("a failed refresh keeps the figures on screen and shows the error (guard: error shown on refresh)", async () => {
    let broken = false;
    stubFetch((c) => (c.url.endsWith("/usage") && broken ? fail(500, "internal") : routes(c)));
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    render(
      <QueryClientProvider client={qc}>
        <TenantOverview account={account()} onChanged={vi.fn()} />
      </QueryClientProvider>,
    );
    await screen.findByText("Storage");
    broken = true;
    await act(async () => {
      await qc.invalidateQueries();
    });
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.getByText("Storage")).toBeTruthy();
  });
});

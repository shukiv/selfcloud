// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { stubFetch } from "../../test/fetchStub";
import TenantUsers from "./TenantUsers";
import { person, signedAs } from "../../test/adminFixtures";

const active = person({ id: "usr_active", email: "mateo@example.com", display_name: "Mateo", role: "member", status: "active" });
const pending = person({ id: "usr_pending", email: "dana@example.com", display_name: "Dana", role: "member", status: "pending" });

function renderUsers() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TenantUsers account="acc_1" />
    </QueryClientProvider>,
  );
}

// The admin's session carries the installation's two switches, as /me sends them to an installation admin.
function adminWith(switches: { tenantAccess: boolean; mailConfigured: boolean }) {
  session.set({ ...signedAs("admin"), ...switches });
}

beforeEach(() => {
  stubFetch(() => ({ status: 200, body: { items: [active, pending] } }));
});
afterEach(() => session.set(null));

describe("the reset and sign-in actions follow the installation's switches", () => {
  it("with neither mail nor tenant access, no reset and no sign-in is offered", async () => {
    adminWith({ tenantAccess: false, mailConfigured: false });
    renderUsers();
    await screen.findByText("Mateo");
    expect(screen.queryByRole("button", { name: /Reset password/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /Sign in as/ })).toBeNull();
  });

  it("with mail only, a reset is offered and no sign-in", async () => {
    adminWith({ tenantAccess: false, mailConfigured: true });
    renderUsers();
    await screen.findByText("Mateo");
    expect(screen.getAllByRole("button", { name: /Reset password/ })).toHaveLength(2);
    expect(screen.queryByRole("button", { name: /Sign in as/ })).toBeNull();
  });

  it("with tenant access, sign-in is offered for an active person only", async () => {
    adminWith({ tenantAccess: true, mailConfigured: false });
    renderUsers();
    await screen.findByText("Mateo");
    expect(screen.getAllByRole("button", { name: /Reset password/ })).toHaveLength(2);
    const signIns = screen.getAllByRole("button", { name: /Sign in as/ });
    expect(signIns).toHaveLength(1);
    expect(signIns[0].textContent).toContain("Sign in as");
  });
});

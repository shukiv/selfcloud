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

// The inline icons show on a desktop; below md they are in the ⋮ menu (design-tl section 4), so these tests pin a desktop.
let savedMatchMedia: typeof window.matchMedia;
beforeEach(() => {
  stubFetch(() => ({ status: 200, body: { items: [active, pending] } }));
  savedMatchMedia = window.matchMedia;
  window.matchMedia = ((query: string) => {
    const m = /min-width: (\d+)px/.exec(query);
    return { matches: m ? 1200 >= Number(m[1]) : false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
  }) as unknown as typeof window.matchMedia;
});
afterEach(() => {
  session.set(null);
  window.matchMedia = savedMatchMedia;
});

// A pending owner's invitation: resend by email needs mail, and a new link to an admin's session needs tenant access
// (decision 41). The server refuses the other way, so the page offers only these.
const pendingOwner = person({ id: "usr_owner", email: "owen@example.com", display_name: "Owen", role: "owner", status: "pending" });

describe("a pending owner's invitation actions follow the installation's switches", () => {
  beforeEach(() => {
    stubFetch(() => ({ status: 200, body: { items: [pendingOwner] } }));
  });

  it("with neither mail nor tenant access, neither resend nor a new link is offered", async () => {
    adminWith({ tenantAccess: false, mailConfigured: false });
    renderUsers();
    await screen.findByText("Owen");
    expect(screen.queryByRole("button", { name: /Resend invitation/ })).toBeNull();
    expect(screen.queryByRole("button", { name: /New link/ })).toBeNull();
  });

  it("with mail only, resend by email is offered and no new link", async () => {
    adminWith({ tenantAccess: false, mailConfigured: true });
    renderUsers();
    await screen.findByText("Owen");
    expect(screen.getByRole("button", { name: /Resend invitation/ })).toBeTruthy();
    expect(screen.queryByRole("button", { name: /New link/ })).toBeNull();
  });

  it("with tenant access only, a new link is offered and no resend by email", async () => {
    adminWith({ tenantAccess: true, mailConfigured: false });
    renderUsers();
    await screen.findByText("Owen");
    expect(screen.queryByRole("button", { name: /Resend invitation/ })).toBeNull();
    expect(screen.getByRole("button", { name: /New link/ })).toBeTruthy();
  });
});

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
    expect(signIns[0].getAttribute("aria-label")).toContain("Sign in as");
    expect(signIns[0].textContent).toBe("");
  });
});

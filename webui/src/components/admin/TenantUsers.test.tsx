// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import TenantUsers from "./TenantUsers";
import { LINK, person, signedAs } from "../../test/adminFixtures";

const pendingOwner = person({ id: "usr_owner", email: "owen@example.com", display_name: "Owen", role: "owner", status: "pending" });

function renderUsers() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TenantUsers account="acc_1" />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => session.set(null));

describe("people of a tenant", () => {
  it("a failed first load shows the error and no empty state (guard: firstFailed)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderUsers();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByText("No people yet.")).toBeNull();
  });

  it("a new invitation link is shown once and the link is gone when the dialog closes", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/verification")) return { status: 200, body: { ...pendingOwner, verification: { method: "link", url: LINK } } };
      return { status: 200, body: { items: [pendingOwner] } };
    });
    const qc = renderUsers();
    await screen.findByText("Owen");
    fireEvent.click(screen.getByRole("button", { name: /New link/ }));
    await screen.findByDisplayValue(LINK);
    expect(JSON.stringify(qc.getQueryCache().getAll().map((q) => q.state.data))).not.toContain("SECRET-ONE-TIME-VALUE");
    fireEvent.click(screen.getByRole("button", { name: "I have copied it" }));
    await waitFor(() => expect(screen.queryByDisplayValue(LINK)).toBeNull());
  });

  it("a session change clears the link (guard: useOnSessionChange)", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/verification")) return { status: 200, body: { ...pendingOwner, verification: { method: "link", url: LINK } } };
      return { status: 200, body: { items: [pendingOwner] } };
    });
    renderUsers();
    await screen.findByText("Owen");
    fireEvent.click(screen.getByRole("button", { name: /New link/ }));
    await screen.findByDisplayValue(LINK);
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(screen.queryByDisplayValue(LINK)).toBeNull());
  });

  it("a refused resend shows the code's text and sends nothing more (guard: failureText)", async () => {
    const { calls } = (() => {
      const s = stubFetch((c: Call) => {
        if (c.url.includes("/verification")) return fail(429, "rate_limited", { text: "SERVER-TEXT" });
        return { status: 200, body: { items: [pendingOwner] } };
      });
      return s;
    })();
    renderUsers();
    await screen.findByText("Owen");
    fireEvent.click(screen.getByRole("button", { name: /Resend/ }));
    expect((await screen.findByRole("alert")).textContent).toContain("Too many");
    expect(calls.filter((c) => c.url.includes("/verification"))).toHaveLength(1);
  });
});

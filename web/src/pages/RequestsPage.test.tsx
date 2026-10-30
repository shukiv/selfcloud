// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { pendingRequests } from "../lib/sharing/requestCount";
import { get } from "../lib/store";
import RequestsPage from "./RequestsPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "owner", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const request = (id: string, role: string, user_name?: string) => ({
  id,
  node_id: "nod_item",
  user_id: "usr_asker_1",
  user_name,
  role,
  message: "",
  status: "pending",
  created_at: "2026-10-01T00:00:00Z",
  decided_at: null,
  decided_by: "",
});

const node = (role: string, editors_can_share: boolean) => ({
  id: "nod_item",
  folder_id: "fld_a",
  parent_id: null,
  name: "Plans",
  kind: "dir",
  size: 0,
  mime_type: "",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role,
  owner_id: "usr_owner",
  no_download: false,
  editors_can_share,
});

function renderPage() {
  const qc = createQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <RequestsPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  session.set(signedAs("olivia"));
});

afterEach(() => {
  session.set(null);
  pendingRequests.set(null);
});

describe("access requests", () => {
  it("the count is read again each time the page opens, not kept from the last visit (guard: refreshPendingRequests on mount)", async () => {
    pendingRequests.set(9);
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/access-requests") return { status: 200, body: { items: [request("arq_1", "viewer", "Mateo")] } };
      if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node("editor", true) };
      return fail(404, "not_found");
    });
    renderPage();
    await waitFor(() => expect(get(pendingRequests)).toBe(1));
  });

  it("offers a grant no higher than the caller's own share cap (guard: grantableRoles(shareCap))", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/access-requests") return { status: 200, body: { items: [request("arq_1", "viewer", "Mateo")] } };
      if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node("editor", true) };
      return fail(404, "not_found");
    });
    renderPage();
    const select = await screen.findByRole("combobox");
    fireEvent.mouseDown(select);
    await waitFor(() => expect(document.querySelectorAll(".ant-select-item-option").length).toBeGreaterThan(0));
    const labels = [...document.querySelectorAll(".ant-select-item-option")].map((o) => o.textContent);
    expect(labels).toEqual(["Can view", "Can comment", "Can upload", "Can edit"]);
    expect(labels).not.toContain("Manager");
  });

  it("sends the role only when the approver changes it", async () => {
    const approvals: Array<unknown> = [];
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/access-requests") return { status: 200, body: { items: [request("arq_1", "viewer", "Mateo")] } };
      if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node("manager", false) };
      if (c.url === "/api/v1/access-requests/arq_1/approve") {
        approvals.push(c.body);
        return { status: 200, body: request("arq_1", "viewer", "Mateo") };
      }
      return fail(404, "not_found");
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    await waitFor(() => expect(approvals).toEqual([{}]));
  });

  it("a hidden item is named by no ID, and a person without a name is shown as A person", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/access-requests") return { status: 200, body: { items: [request("arq_1", "viewer")] } };
      return fail(404, "not_found");
    });
    const { container } = renderPage();
    await screen.findByText("An item you can't see");
    expect(screen.getByText("A person")).toBeTruthy();
    expect(container.textContent?.includes("usr_asker_1")).toBe(false);
    expect(container.textContent?.includes("nod_item")).toBe(false);
  });

  it("a refused decision shows its message by code, never the server's text", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/access-requests") return { status: 200, body: { items: [request("arq_1", "viewer", "Mateo")] } };
      if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node("manager", false) };
      if (c.url === "/api/v1/access-requests/arq_1/deny") return fail(403, "forbidden");
      return fail(404, "not_found");
    });
    renderPage();
    const list = await screen.findByRole("list", { name: "Access requests" });
    fireEvent.click(within(list).getByRole("button", { name: "Deny" }));
    expect(await screen.findByText("You can't decide this request.")).toBeTruthy();
  });
});

describe("a decision answered after the person has changed", () => {
  it("shows no notice and refreshes nothing for the next person (guard: the watch taken when the decision is sent)", async () => {
    let release!: () => void;
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    const { calls } = stubFetch(async (c: Call) => {
      if (c.url === "/api/v1/access-requests") return { status: 200, body: { items: [request("arq_1", "viewer", "Mateo")] } };
      if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node("manager", true) };
      if (c.url === "/api/v1/access-requests/arq_1/approve") {
        await held;
        return { status: 200, body: request("arq_1", "viewer", "Mateo") };
      }
      return fail(404, "not_found");
    });
    const qc = createQueryClient();
    render(
      <QueryClientProvider client={qc}>
        <RequestsPage />
      </QueryClientProvider>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "Approve" }));
    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/approve"))).toBe(true));
    // The person changes while the decision is out: the client is cleared, as the app does at a change of person.
    session.set(signedAs("bob"));
    qc.clear();
    release();
    await new Promise((r) => setTimeout(r, 50));
    // The notice names the person decided for; it must not be shown on the next person's page.
    expect(document.body.textContent).not.toContain("Access given");
  });
});


// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { pendingRequests } from "../lib/sharing/requestCount";
import { desktopViewport } from "../test/viewport";
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

// The row icons (Approve, Deny) are drawn inline on a desktop; a phone has them in the ⋮ menu (tl5, decision 138).
let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
  session.set(signedAs("olivia"));
});

afterEach(() => {
  undoView();
  session.set(null);
  pendingRequests.set(null);
});

describe("access requests icon buttons name the person who asks", () => {
  beforeEach(() => session.set(signedAs("olivia")));
  afterEach(() => session.set(null));

  it("each request's Approve and Deny name the asker, so the rows can be told apart", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/access-requests") {
        return { status: 200, body: { items: [request("arq_1", "viewer", "Ann Lee"), request("arq_2", "editor", "Ben Ko")] } };
      }
      return fail(404, "not_found");
    });
    renderPage();
    await screen.findByText("Ann Lee");
    // The breakpoint is known after the first draw, and the icons come with it: wait for them rather than read them at once.
    expect(await screen.findByRole("button", { name: "Approve: Ann Lee" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Deny: Ann Lee" })).toBeTruthy();
    expect(screen.getByRole("button", { name: "Approve: Ben Ko" })).toBeTruthy();
  });
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
    fireEvent.click(await screen.findByRole("button", { name: /^Approve/ }));
    await waitFor(() => expect(approvals).toEqual([{}]));
  });

  it("a double click on Approve sends one approval, and no second one when the first has answered", async () => {
    const calls: Call[] = [];
    stubFetch(async (c: Call) => {
      if (c.url === "/api/v1/access-requests") return { status: 200, body: { items: [request("arq_1", "viewer", "Mateo")] } };
      if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node("manager", false) };
      if (c.url === "/api/v1/access-requests/arq_1/approve") {
        calls.push(c);
        // The answer takes time, as a network does, so the second click comes while the first is on its way.
        await new Promise((r) => setTimeout(r, 30));
        return { status: 200, body: request("arq_1", "viewer", "Mateo") };
      }
      return fail(404, "not_found");
    });
    renderPage();
    const approve = await screen.findByRole("button", { name: /^Approve/ });
    fireEvent.click(approve);
    fireEvent.click(approve);
    await screen.findByText(/Access given to Mateo/);
    expect(calls).toHaveLength(1);
  });

  it("reads the list again after an approval", async () => {
    let lists = 0;
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/access-requests") {
        lists++;
        return { status: 200, body: { items: lists === 1 ? [request("arq_1", "viewer", "Mateo")] : [] } };
      }
      if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node("manager", false) };
      if (c.url === "/api/v1/access-requests/arq_1/approve") return { status: 200, body: request("arq_1", "viewer", "Mateo") };
      return fail(404, "not_found");
    });
    renderPage();
    const approve = await screen.findByRole("button", { name: /^Approve/ });
    const before = lists;
    fireEvent.click(approve);
    await screen.findByText("Nothing to decide");
    expect(lists).toBeGreaterThan(before);
    expect(screen.queryByText("Mateo")).toBeNull();
  });

  it("after a decision the focus goes to the next row's first control, or to the page heading when no row is left", async () => {
    let decided: string[] = [];
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/access-requests") {
        const all = [request("arq_1", "viewer", "Ann Lee"), request("arq_2", "editor", "Ben Ko")];
        return { status: 200, body: { items: all.filter((r) => !decided.includes(r.id)) } };
      }
      if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node("manager", false) };
      if (c.url.startsWith("/api/v1/access-requests/") && c.url.endsWith("/deny")) {
        decided = [...decided, c.url.split("/")[4] as string];
        return { status: 200, body: request("arq_1", "viewer", "Ann Lee") };
      }
      return fail(404, "not_found");
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Deny: Ann Lee" }));
    await waitFor(() => expect(screen.queryByRole("row", { name: "Ann Lee" })).toBeNull());
    const next = screen.getByRole("row", { name: "Ben Ko" });
    await waitFor(() => expect(next.contains(document.activeElement)).toBe(true));
    expect(document.activeElement).not.toBe(next);
    fireEvent.click(screen.getByRole("button", { name: "Deny: Ben Ko" }));
    await screen.findByText("Nothing to decide");
    await waitFor(() => expect(document.activeElement).toBe(screen.getByRole("heading", { name: "Access requests" })));
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
    const list = await screen.findByRole("region", { name: "Access requests" });
    fireEvent.click(within(list).getByRole("button", { name: /^Deny/ }));
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
    fireEvent.click(await screen.findByRole("button", { name: /^Approve/ }));
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


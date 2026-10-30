// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import GroupsPage from "./GroupsPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

const GROUP = { id: "grp_1", external_id: null, name: "Finance", created_at: "2026-10-01T00:00:00Z", updated_at: "2026-10-01T00:00:00Z" };

function renderPage() {
  const qc = createQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <GroupsPage />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

const isGroupDelete = (c: Call) => c.method === "DELETE" && c.url === "/api/v1/accounts/acc_1/groups/grp_1";

describe("groups", () => {
  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c) => (c.url.includes("/groups") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText(/No groups yet/)).toBeNull();
  });

  it("a double click on delete sends one request (guard: the once-at-a-time check)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((r) => {
      release = r;
    });
    const { calls } = stubFetch((c) => {
      if (isGroupDelete(c)) return held;
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Finance/ }));
    const go = await screen.findByRole("button", { name: /^Delete$/ });
    act(() => {
      fireEvent.click(go);
      fireEvent.click(go);
    });
    await waitFor(() => expect(calls.filter(isGroupDelete)).toHaveLength(1));
    release({ status: 204 });
  });

  it("a deletion answered after the person has changed shows nothing (guard: sessionChanged before the await)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((r) => {
      release = r;
    });
    const { calls } = stubFetch((c) => {
      if (isGroupDelete(c)) return held;
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Finance/ }));
    fireEvent.click(await screen.findByRole("button", { name: /^Delete$/ }));
    await waitFor(() => expect(calls.filter(isGroupDelete)).toHaveLength(1));
    // Another owner signs in: the page stays mounted, so a late write would be visible to the next person.
    session.set({ ...signedAs(), user: { ...signedAs().user, id: "usr_bob" } } as Session);
    release({ status: 204 });
    await new Promise((r) => setTimeout(r, 30));
    expect(screen.queryByText("Finance was deleted.")).toBeNull();
    expect(calls.filter(isGroupDelete)).toHaveLength(1);
  });

  it("a refused deletion keeps the dialog open with the error by code, and not the server text (guard: the error stays in the confirm)", async () => {
    stubFetch((c) => {
      if (isGroupDelete(c)) return { status: 403, body: { error: { code: "forbidden", message: "SERVER-TEXT-XYZ" } } };
      if (c.url.includes("/groups")) return { status: 200, body: { items: [GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Finance/ }));
    fireEvent.click(await screen.findByRole("button", { name: /^Delete$/ }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toContain("SERVER-TEXT-XYZ");
    expect(screen.getByRole("button", { name: /Delete$/ })).toBeTruthy();
  });
});

describe("the open group after a rename", () => {
  // The heading shows the name the list has now, not the name the group had when it was opened (guard: the header reads the list).
  it("the members heading takes the new name once the rename is saved", async () => {
    let renamed = false;
    stubFetch((c) => {
      if (c.method !== "GET" && c.url === "/api/v1/accounts/acc_1/groups/grp_1") {
        renamed = true;
        return { status: 200, body: { ...GROUP, name: "Accounts" } };
      }
      if (c.url.includes("/groups/grp_1/members")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/groups")) return { status: 200, body: { items: [renamed ? { ...GROUP, name: "Accounts" } : GROUP] } };
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Members.*Finance/ }));
    await waitFor(() => expect(document.getElementById("members-title")?.textContent).toBe("Finance"));
    fireEvent.click(screen.getByRole("button", { name: /Rename.*Finance/ }));
    fireEvent.change(await screen.findByLabelText("Group name"), { target: { value: "Accounts" } });
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(document.getElementById("members-title")?.textContent).toBe("Accounts"));
  });
});


// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import TenantUsers from "./TenantUsers";
import { LINK, person, signedAs } from "../../test/adminFixtures";
import { expectEmptyTable } from "../../test/emptyTable";

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

// Both switches on, so the pending owner offers resend by email and a new link (decision 41).
// These tests are about a desktop: the inline icons show there, and the Role column is a column.
const desktop = (query: string) => {
  const m = /min-width: (\d+)px/.exec(query);
  return { matches: m ? 1200 >= Number(m[1]) : false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
};
let savedMatchMedia: typeof window.matchMedia;
beforeEach(() => {
  session.set({ ...signedAs("admin"), tenantAccess: true, mailConfigured: true } as Session);
  savedMatchMedia = window.matchMedia;
  window.matchMedia = desktop as unknown as typeof window.matchMedia;
});
afterEach(() => {
  session.set(null);
  window.matchMedia = savedMatchMedia;
});

// tl1: the people are a table. On a phone the inline icons move into the row's ⋮ menu.
describe("people of a tenant as a table", () => {
  it("is a region with Name, Role and Status headers, and no list", async () => {
    stubFetch(() => ({ status: 200, body: { items: [pendingOwner] } }));
    renderUsers();
    const region = await screen.findByRole("region", { name: "People" });
    // The table is drawn before its headers settle on the window's layout: wait for each header rather than read it at once.
    expect(await within(region).findByRole("columnheader", { name: "Role" })).toBeTruthy();
    expect(await within(region).findByRole("columnheader", { name: "Status" })).toBeTruthy();
    expect(document.querySelector("ul[aria-label]")).toBeNull();
  });

  // The mockup's Last sign-in column, Never when the person has not signed in.
  it("has a Last sign-in column with the date, or Never", async () => {
    const active = person({ id: "usr_a", email: "mateo@example.com", display_name: "Mateo", role: "member", status: "active", last_sign_in_at: "2026-11-02T08:00:00Z" });
    stubFetch(() => ({ status: 200, body: { items: [active, pendingOwner] } }));
    renderUsers();
    const region = await screen.findByRole("region", { name: "People" });
    const heads = within(region).getAllByRole("columnheader").map((h) => h.textContent);
    const col = heads.indexOf("Last sign-in");
    expect(col).toBeGreaterThan(0);
    const cell = (name: string) => (region.querySelector(`tr[aria-label="${name}"]`) as HTMLElement).querySelectorAll("td")[col];
    expect(cell("Mateo").querySelector("time")?.getAttribute("dateTime")).toBe("2026-11-02T08:00:00Z");
    expect(cell("Owen").textContent).toBe("Never");
  });

  // The owner's rule of 2026-11-09 (decision 114): the empty table keeps its headers; an admin adds no one here, so no action.
  it("an empty list keeps its header row, and its Empty in the table has no action", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderUsers();
    await expectEmptyTable("People", {
      headers: ["Name", "Role", "Status", "Last sign-in"],
      title: "No people yet",
      description: "The tenant's owner invites its people, or an app adds them through the API.",
    });
  });

  it("on a phone a pending owner's New link is in the row's ⋮ menu", async () => {
    window.matchMedia = ((query: string) => ({ matches: false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false })) as unknown as typeof window.matchMedia;
    stubFetch(() => ({ status: 200, body: { items: [pendingOwner] } }));
    renderUsers();
    await screen.findByText("Owen");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Owen" }));
    expect(await screen.findByRole("menuitem", { name: /New link/ })).toBeTruthy();
  });
});

describe("people of a tenant", () => {
  it("a failed first load shows the error and no empty state (guard: firstFailed)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderUsers();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByText("No people yet")).toBeNull();
  });

  // A new link to an admin's session needs the admin's own password (decision 84). The prompt opens on New link, and the
  // request is sent only once the password is confirmed.
  it("a new link asks for the admin's password first, then sends it", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url.includes("/verification")) return { status: 200, body: { ...pendingOwner, verification: { method: "link", url: LINK } } };
      return { status: 200, body: { items: [pendingOwner] } };
    });
    renderUsers();
    await screen.findByText("Owen");
    fireEvent.click(screen.getByRole("button", { name: /New link/ }));
    expect(calls.some((c) => c.url.includes("/verification"))).toBe(false);
    fireEvent.change(document.getElementById("owner-link-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByDisplayValue(LINK);
    expect(calls.find((c) => c.url.includes("/verification"))?.body).toEqual({ method: "link", admin_password: "admin-pass-1" });
  });

  it("a new invitation link is shown once and the link is gone when the dialog closes", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/verification")) return { status: 200, body: { ...pendingOwner, verification: { method: "link", url: LINK } } };
      return { status: 200, body: { items: [pendingOwner] } };
    });
    const qc = renderUsers();
    await screen.findByText("Owen");
    fireEvent.click(screen.getByRole("button", { name: /New link/ }));
    fireEvent.change(document.getElementById("owner-link-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
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
    fireEvent.change(document.getElementById("owner-link-admin") as HTMLInputElement, { target: { value: "admin-pass-1" } });
    fireEvent.click(screen.getByRole("button", { name: "Confirm" }));
    await screen.findByDisplayValue(LINK);
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(screen.queryByDisplayValue(LINK)).toBeNull());
  });

  // A row with a request on its way keeps its icons where they are, disabled; an icon taken out of the row would
  // move the others and drop the focus. The re-check: a real browser takes the focus from a button that turns disabled, so
  // the icons are aria-disabled and stay focusable, and a second press does nothing (the e2e reads the focus in Chrome).
  it("keeps a busy row's actions in place, aria-disabled, and the pressed one keeps the focus", async () => {
    let release: (a: Answer) => void = () => {};
    const { calls } = stubFetch((c: Call) => {
      if (c.url.includes("/verification")) return new Promise<Answer>((r) => (release = r));
      return { status: 200, body: { items: [pendingOwner] } };
    });
    renderUsers();
    await screen.findByText("Owen");
    const resend = screen.getByRole("button", { name: /Resend invitation/ }) as HTMLButtonElement;
    const newLink = screen.getByRole("button", { name: /New link/ }) as HTMLButtonElement;
    resend.focus();
    fireEvent.click(resend);
    await waitFor(() => expect(resend.getAttribute("aria-disabled")).toBe("true"));
    expect(resend.disabled).toBe(false);
    expect(resend.isConnected).toBe(true);
    expect(newLink.isConnected).toBe(true);
    expect(newLink.getAttribute("aria-disabled")).toBe("true");
    expect(document.activeElement).toBe(resend);
    fireEvent.click(resend);
    expect(calls.filter((c) => c.url.includes("/verification"))).toHaveLength(1);
    await act(async () => release({ status: 200, body: { ...pendingOwner, verification: { method: "email", sent: true } } }));
    await waitFor(() => expect(resend.getAttribute("aria-disabled")).toBeNull());
    expect(resend.isConnected).toBe(true);
    expect(document.activeElement).toBe(resend);
  });

  // The ⋮ menu's Disable and Enable (the mockup's), through the route the owner's People page uses.
  it("the ⋮ menu disables an active person and enables a disabled one, and the list is read again", async () => {
    const mateo = person({ id: "usr_a", email: "mateo@example.com", display_name: "Mateo", role: "member", status: "active" });
    const gal = person({ id: "usr_g", email: "gal@example.com", display_name: "Gal", role: "member", status: "disabled" });
    const { calls } = stubFetch((c: Call) => {
      if (c.method === "PATCH") return { status: 200, body: { ...mateo, status: "disabled" } };
      return { status: 200, body: { items: [mateo, gal] } };
    });
    renderUsers();
    await screen.findByText("Mateo");
    // A pending person has neither: there is nothing to disable until it has verified.
    fireEvent.click(screen.getByRole("button", { name: "More actions for Mateo" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Disable/ }));
    await waitFor(() => expect(calls.find((c) => c.method === "PATCH")).toBeTruthy());
    const patch = calls.find((c) => c.method === "PATCH") as Call;
    expect(new URL(patch.url, "http://x").pathname).toBe("/api/v1/accounts/acc_1/users/usr_a");
    expect(patch.body).toEqual({ status: "disabled" });
    await waitFor(() => expect(calls.filter((c) => c.method === "GET" && c.url.includes("/users")).length).toBeGreaterThan(1));
    fireEvent.click(screen.getByRole("button", { name: "More actions for Gal" }));
    fireEvent.click(await screen.findByRole("menuitem", { name: /Enable/ }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(2));
    expect(calls.filter((c) => c.method === "PATCH")[1].body).toEqual({ status: "active" });
  });

  // Re-check item 2: the mockup's Remove from account, a danger entry of the ⋮ menu, through the route the owner's People
  // page uses (DELETE). Nothing is sent until the confirmation's danger button, in the dialog's footer, is pressed.
  it("the ⋮ menu removes a person after a confirmation, and the list is read again", async () => {
    const mateo = person({ id: "usr_a", email: "mateo@example.com", display_name: "Mateo", role: "member", status: "active" });
    const { calls } = stubFetch((c: Call) => {
      if (c.method === "DELETE") return { status: 204 };
      return { status: 200, body: { items: [mateo] } };
    });
    renderUsers();
    await screen.findByText("Mateo");
    fireEvent.click(screen.getByRole("button", { name: "More actions for Mateo" }));
    // The mockup's words, from the admin catalogue part.
    const entry = await screen.findByRole("menuitem", { name: "Remove from account…" });
    expect(entry.className).toContain("ant-dropdown-menu-item-danger");
    fireEvent.click(entry);
    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByText("Remove Mateo?")).toBeTruthy();
    expect(calls.some((c) => c.method === "DELETE")).toBe(false);
    const confirm = within(dialog).getByRole("button", { name: "Remove" });
    expect(confirm.className).toContain("ant-btn-dangerous");
    expect(confirm.closest(".ant-modal-footer")).not.toBeNull();
    fireEvent.click(confirm);
    await waitFor(() => expect(calls.find((c) => c.method === "DELETE")).toBeTruthy());
    expect(new URL((calls.find((c) => c.method === "DELETE") as Call).url, "http://x").pathname).toBe("/api/v1/accounts/acc_1/users/usr_a");
    expect(await screen.findByText("Mateo was removed.")).toBeTruthy();
    await waitFor(() => expect(calls.filter((c) => c.method === "GET" && c.url.includes("/users")).length).toBeGreaterThan(1));
  });

  it("a pending person can be sent the invitation again by email, and has no Disable", async () => {
    const dana = person({ id: "usr_d", email: "dana@example.com", display_name: "Dana", role: "member", status: "pending" });
    const { calls } = stubFetch((c: Call) => {
      if (c.url.includes("/verification")) return { status: 200, body: { ...dana, verification: { method: "email", sent: true } } };
      return { status: 200, body: { items: [dana] } };
    });
    renderUsers();
    await screen.findByText("Dana");
    // A new link is an admin's way in only for a pending owner (decision 41); a member gets the email only.
    expect(screen.queryByRole("button", { name: /New link/ })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Resend invitation: Dana" }));
    await waitFor(() => expect(calls.find((c) => c.url.includes("/verification"))?.body).toEqual({ method: "email" }));
    expect(await screen.findByText("The email was sent.")).toBeTruthy();
    // Resend and Reset are inline, and the ⋮ menu has Remove alone: Disable waits until the person has verified.
    expect(within(screen.getByRole("region", { name: "People" })).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual([
      "Resend invitation: Dana",
      "Reset password: Dana",
      "More actions for Dana",
    ]);
    fireEvent.click(screen.getByRole("button", { name: "More actions for Dana" }));
    expect((await screen.findAllByRole("menuitem")).map((m) => m.textContent)).toEqual(["Remove from account…"]);
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

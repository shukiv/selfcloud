// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { person, signedAs } from "../../test/adminFixtures";
import { expectEmptyTable } from "../../test/emptyTable";
import AdminsPage from "./AdminsPage";

function renderAdmins() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <App>
      <QueryClientProvider client={qc}>
        <AdminsPage />
      </QueryClientProvider>
    </App>,
  );
  return qc;
}

const path = (c: Call) => new URL(c.url, "http://x").pathname;

// hangingAdmins answers the list with one admin and never answers a write; each write's signal is recorded, and an abort
// rejects the write as the browser does.
function hangingAdmins(signals: AbortSignal[]) {
  return (_url: string, init: RequestInit = {}) => {
    if ((init.method ?? "GET") === "GET") {
      const body = { items: [person()] };
      return Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { "Content-Type": "application/json" } }));
    }
    return new Promise<Response>((_resolve, reject) => {
      const signal = init.signal as AbortSignal;
      signals.push(signal);
      signal.addEventListener("abort", () => reject(new DOMException("aborted", "AbortError")));
    });
  };
}

// The admin's session as the server sends it: mail_configured says whether mail can carry an invitation to a new admin.
const adminSession = (mailConfigured: boolean) => ({ ...signedAs("admin"), mailConfigured }) as Session;

// These tests are about a desktop: the inline icons show there, and below md they move into the ⋮ menu.
const desktop = (query: string) => {
  const m = /min-width: (\d+)px/.exec(query);
  return { matches: m ? 1200 >= Number(m[1]) : false, media: query, onchange: null, addListener() {}, removeListener() {}, addEventListener() {}, removeEventListener() {}, dispatchEvent: () => false };
};
let savedMatchMedia: typeof window.matchMedia;
beforeEach(() => {
  session.set(adminSession(true));
  savedMatchMedia = window.matchMedia;
  window.matchMedia = desktop as unknown as typeof window.matchMedia;
});
afterEach(() => {
  session.set(null);
  window.matchMedia = savedMatchMedia;
});

// openNew opens the new admin dialog once the list has loaded.
async function openNew() {
  await screen.findByRole("region", { name: "Admins" });
  fireEvent.click(screen.getByRole("button", { name: "New admin" }));
}

describe("the new admin dialog's name is not an ambiguous Name", () => {
  it("labels the field Admin's name", async () => {
    stubFetch(() => ({ status: 200, body: { items: [person()] } }));
    renderAdmins();
    await openNew();
    expect(document.querySelector('label[for="admin-name"]')?.textContent).toBe("Admin's name");
  });
});

describe("the new admin dialog offers only what the server accepts for an admin (decision 41)", () => {
  // A link to a new admin is refused to an admin's session (linkAllowed takes only a pending owner), so the dialog offers
  // the email invitation only. The question says who gets in: the admin, not the owner.
  it("offers no link, and says how the admin gets in", async () => {
    stubFetch(() => ({ status: 200, body: { items: [person()] } }));
    renderAdmins();
    await openNew();
    expect(screen.getByText("How the admin gets in")).toBeTruthy();
    expect(screen.queryByText("How the owner gets in")).toBeNull();
    expect(screen.queryByRole("radio", { name: /link/i })).toBeNull();
    expect(screen.queryByText("Show me a link to pass on")).toBeNull();
  });

  it("sends the invitation by email", async () => {
    const { calls } = stubFetch((c: Call) => (c.method === "POST" ? { status: 201, body: { ...person({ id: "usr_2" }), verification: { method: "email", sent: true } } } : { status: 200, body: { items: [person()] } }));
    renderAdmins();
    await openNew();
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "bo@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Send invitation" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    expect(calls.find((c) => c.method === "POST")?.body).toEqual({ email: "bo@example.com", display_name: "", verification: "email" });
  });

  // Without mail the server can send no email, and an admin's session may not take a link: the admin can only be made
  // with the server command. The dialog says so, and sends nothing.
  it("with mail not configured, says an admin can only be added with the server command, and sends nothing", async () => {
    session.set(adminSession(false));
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [person()] } }));
    renderAdmins();
    await openNew();
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "bo@example.com" } });
    expect(screen.getByRole("dialog").textContent).toContain("selfcloud admin create-admin");
    expect((screen.getByRole("button", { name: "Send invitation" }) as HTMLButtonElement).disabled).toBe(true);
    expect(calls.filter((c) => c.method === "POST")).toEqual([]);
  });
});

describe("who sees the admins page", () => {
  it.each(["owner", "member"] as const)("a %s sees the not-found view and no request is sent", async (role) => {
    session.set(signedAs(role));
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderAdmins();
    expect(await screen.findByText("Page not found")).toBeTruthy();
    expect(calls).toEqual([]);
  });
});

// tl1: the admins list is a table. The signed-in admin's own row says so and has no Remove icon (owner-approved).
describe("the admins table marks the signed-in admin and has an empty state", () => {
  it("the signed-in admin's row has a You badge and no Remove; another admin's row has a Remove icon", async () => {
    stubFetch(() => ({
      status: 200,
      body: { items: [person({ id: "admin_1", email: "me@example.com", display_name: "Me" }), person({ id: "usr_2", email: "other@example.com", display_name: "Other" })] },
    }));
    renderAdmins();
    const list = await screen.findByRole("region", { name: "Admins" });
    const mine = list.querySelector('tr[aria-label="Me"]') as HTMLElement;
    expect(within(mine).getByText("You")).toBeTruthy();
    expect(within(mine).queryByRole("button", { name: /^Remove/ })).toBeNull();
    const other = list.querySelector('tr[aria-label="Other"]') as HTMLElement;
    expect(within(other).queryByText("You")).toBeNull();
    expect(within(other).getByRole("button", { name: "Remove: Other" })).toBeTruthy();
  });

  // On a phone the inline icons move into the ⋮ menu, so another admin's Remove is reached through it.
  it("on a phone, another admin's Remove is in that row's ⋮ menu", async () => {
    window.matchMedia = ((query: string) => ({ ...desktop(query), matches: false })) as unknown as typeof window.matchMedia;
    stubFetch(() => ({ status: 200, body: { items: [person({ id: "usr_2", email: "other@example.com", display_name: "Other" })] } }));
    renderAdmins();
    await screen.findByRole("region", { name: "Admins" });
    fireEvent.click(screen.getByRole("button", { name: "More actions for Other" }));
    expect(await screen.findByRole("menuitem", { name: "Remove: Other" })).toBeTruthy();
  });

  // The owner's rule of 2026-11-09 (decision 114): the empty table keeps its headers, and its Empty offers New admin.
  it("an empty list keeps its header row, and its Empty in the table offers New admin, which opens the form", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderAdmins();
    const add = await expectEmptyTable("Admins", {
      headers: ["Name", "Status", "Last sign-in"],
      title: "No people yet",
      description: "An admin runs this installation and its tenants.",
      action: "New admin",
    });
    fireEvent.click(add as HTMLElement);
    expect(await screen.findByRole("dialog")).toBeTruthy();
    expect(document.getElementById("admin-email")).toBeTruthy();
  });

  // Review M3: the mockup's Last sign-in column, from last_sign_in_at on each admin; Never when the admin has not signed in.
  it("shows each admin's last sign-in as a date, and Never for one who has not signed in", async () => {
    stubFetch(() => ({
      status: 200,
      body: {
        items: [
          person({ id: "admin_1", display_name: "Me", last_sign_in_at: "2026-11-06T09:40:00Z" }),
          person({ id: "usr_3", email: "rina@example.com", display_name: "Rina", status: "pending", last_sign_in_at: null }),
        ],
      },
    }));
    renderAdmins();
    const list = await screen.findByRole("region", { name: "Admins" });
    const heads = within(list).getAllByRole("columnheader").map((h) => h.textContent);
    const col = heads.indexOf("Last sign-in");
    expect(col).toBeGreaterThan(0);
    const cell = (name: string) => (list.querySelector(`tr[aria-label="${name}"]`) as HTMLElement).querySelectorAll("td")[col];
    expect(cell("Me").querySelector("time")?.getAttribute("dateTime")).toBe("2026-11-06T09:40:00Z");
    expect(cell("Me").textContent).toContain("2026");
    expect(cell("Rina").textContent).toBe("Never");
  });

  // The mockup's own row: no Remove, and the ⋮ menu has Change password, a link to the change-password page.
  it("the signed-in admin's ⋮ menu has Change password, a link to its page", async () => {
    stubFetch(() => ({ status: 200, body: { items: [person({ id: "admin_1", display_name: "Me" })] } }));
    renderAdmins();
    await screen.findByRole("region", { name: "Admins" });
    fireEvent.click(screen.getByRole("button", { name: "More actions for Me" }));
    const entry = await screen.findByRole("menuitem", { name: "Change password" });
    expect(within(entry).getByRole("link").getAttribute("href")).toBe("/change-password");
  });
});

describe("the admins list", () => {
  it("a failed first load shows the error and no list (guard: firstFailed)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderAdmins();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByRole("region", { name: "Admins" })).toBeNull();
  });

  // The page does not decide who is the last admin: it sees only the pages loaded so far, and the server counts every admin
  // and the admin keys that do not expire. A removal is sent, and the server's refusal is shown (guard: no page-side check).
  it("a removal is sent even when the loaded page holds one active admin, and the server's refusal is shown", async () => {
    const seen: string[] = [];
    stubFetch((c) => {
      if (c.method === "DELETE") {
        seen.push(path(c));
        return fail(409, "conflict", { reason: "last_admin" });
      }
      return path(c) === "/api/v1/admins" ? { status: 200, body: { items: [person()], next_cursor: "more" } } : fail(404, "not_found");
    });
    renderAdmins();
    const remove = await screen.findByRole("button", { name: /Remove/ });
    expect((remove as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(remove);
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(within(screen.getByRole("dialog")).getByRole("alert").textContent).toContain("The last active admin cannot be removed."));
    expect(seen).toEqual(["/api/v1/admins/usr_1"]);
  });

  // A removal that never answers can be cancelled: the request is aborted and the confirmation closes (guard: Cancel on a busy confirm).
  it("Cancel on a busy removal aborts the request and closes the confirmation", async () => {
    const signals: AbortSignal[] = [];
    vi.stubGlobal("fetch", hangingAdmins(signals));
    renderAdmins();
    fireEvent.click(await screen.findByRole("button", { name: /Remove/ }));
    fireEvent.click(within(await screen.findByRole("dialog")).getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(signals).toHaveLength(1));
    fireEvent.click(within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }));
    expect(signals[0].aborted).toBe(true);
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    vi.unstubAllGlobals();
  });

  // The invitation is by email, so the answer carries no link: the page shows the notice and keeps no secret.
  it("an invitation by email closes the form and shows no link", async () => {
    stubFetch((c: Call) => {
      if (c.method === "POST") return { status: 201, body: { ...person({ id: "usr_2", email: "bo@example.com", display_name: "Bo" }), verification: { method: "email", sent: true } } };
      return { status: 200, body: { items: [person()] } };
    });
    renderAdmins();
    await openNew();
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "bo@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: "Send invitation" }));
    await waitFor(() => expect(document.getElementById("admin-email")).toBeNull());
    expect(document.body.textContent).not.toContain("#token=");
    expect(screen.queryByRole("button", { name: "I have copied it" })).toBeNull();
  });

  it("removal asks first; a double click sends one request; a refused one keeps the dialog (guard: useGuard, removeError)", async () => {
    let deletes = 0;
    stubFetch((c: Call) => {
      if (c.method === "DELETE") {
        deletes++;
        return fail(403, "forbidden", { text: "SERVER-TEXT" });
      }
      return { status: 200, body: { items: [person(), person({ id: "usr_2", email: "bo@example.com", display_name: "Bo" })] } };
    });
    renderAdmins();
    const [, removeBo] = await screen.findAllByRole("button", { name: /Remove/ });
    fireEvent.click(removeBo);
    const dialog = await screen.findByRole("dialog");
    const go = within(dialog).getByRole("button", { name: "Remove" });
    fireEvent.click(go);
    fireEvent.click(go);
    await waitFor(() => expect(within(screen.getByRole("dialog")).getByRole("alert").textContent).toContain("not allowed"));
    expect(deletes).toBe(1);
    expect(document.body.textContent).not.toContain("SERVER-TEXT");
  });

  // A refetch that fails keeps the admins that were read: the error goes above the list, and the list stays.
  it("a failed refetch keeps the admins on screen and shows the error (guard: list gated on data, not isSuccess)", async () => {
    let broken = false;
    stubFetch(() => (broken ? fail(500, "internal") : { status: 200, body: { items: [person()] } }));
    const qc = renderAdmins();
    await screen.findByText("Ana Admin");
    broken = true;
    await act(async () => {
      await qc.invalidateQueries();
    });
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.getByText("Ana Admin")).toBeTruthy();
  });
});

describe("the new admin form", () => {
  // A busy form can be cancelled: the request is aborted and the form closes, and the page says the admin may or may not
  // have been added (guard: busy forms can be cancelled).
  it("Cancel on the busy form aborts the request and closes the form", async () => {
    const signals: AbortSignal[] = [];
    vi.stubGlobal("fetch", hangingAdmins(signals));
    renderAdmins();
    await screen.findByRole("region", { name: "Admins" });
    fireEvent.click(screen.getByRole("button", { name: "New admin" }));
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "bo@example.com" } });
    fireEvent.click(document.querySelector('form button[type="submit"]') as HTMLButtonElement);
    await waitFor(() => expect(signals).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(signals[0].aborted).toBe(true);
    await waitFor(() => expect(document.getElementById("admin-email")).toBeNull());
    expect(await screen.findByText("Cancelled. The change may or may not have been saved; check the list before you try again.")).toBeTruthy();
    vi.unstubAllGlobals();
  });

  // The form holds what the admin typed, so a change of person closes it (guard: useOnSessionChange).
  it("a session change closes the form with what was typed (guard: the form closes on a session change)", async () => {
    stubFetch(() => ({ status: 200, body: { items: [person()] } }));
    renderAdmins();
    await screen.findByRole("region", { name: "Admins" });
    fireEvent.click(screen.getByRole("button", { name: "New admin" }));
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "typed@example.com" } });
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(document.getElementById("admin-email")).toBeNull());
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { LINK, person, signedAs } from "../../test/adminFixtures";
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

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => session.set(null));

describe("who sees the admins page", () => {
  it.each(["owner", "member"] as const)("a %s sees the not-found view and no request is sent", async (role) => {
    session.set(signedAs(role));
    const { calls } = stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderAdmins();
    expect(await screen.findByText("Page not found")).toBeTruthy();
    expect(calls).toEqual([]);
  });
});

describe("the admins list", () => {
  it("a failed first load shows the error and no list (guard: firstFailed)", async () => {
    stubFetch(() => fail(500, "internal"));
    renderAdmins();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByRole("list", { name: "Admins" })).toBeNull();
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

  it("an invitation link for a new admin is shown once and is gone when it closes", async () => {
    stubFetch((c: Call) => {
      if (c.method === "POST") return { status: 201, body: { ...person({ id: "usr_2", email: "bo@example.com", display_name: "Bo" }), verification: { method: "link", url: LINK } } };
      return { status: 200, body: { items: [person()] } };
    });
    const qc = renderAdmins();
    await screen.findByRole("list", { name: "Admins" });
    fireEvent.click(screen.getByRole("button", { name: "New admin" }));
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "bo@example.com" } });
    fireEvent.click(screen.getByRole("radio", { name: /link/i }));
    fireEvent.click(screen.getByRole("button", { name: "Send invitation" }));
    await screen.findByDisplayValue(LINK);
    expect(JSON.stringify(qc.getQueryCache().getAll().map((q) => q.state.data))).not.toContain("SECRET-ONE-TIME-VALUE");
    fireEvent.click(screen.getByRole("button", { name: "I have copied it" }));
    await waitFor(() => expect(screen.queryByDisplayValue(LINK)).toBeNull());
  });

  it("a session change clears the link (guard: useOnSessionChange)", async () => {
    stubFetch((c: Call) => {
      if (c.method === "POST") return { status: 201, body: { ...person({ id: "usr_2", email: "bo@example.com" }), verification: { method: "link", url: LINK } } };
      return { status: 200, body: { items: [] } };
    });
    renderAdmins();
    await screen.findByText(/No admins|Ana Admin|admin/i, { exact: false }).catch(() => undefined);
    fireEvent.click(screen.getByRole("button", { name: "New admin" }));
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "bo@example.com" } });
    fireEvent.click(screen.getByRole("radio", { name: /link/i }));
    fireEvent.click(screen.getByRole("button", { name: "Send invitation" }));
    await screen.findByDisplayValue(LINK);
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(screen.queryByDisplayValue(LINK)).toBeNull());
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
    await screen.findByRole("list", { name: "Admins" });
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
    await screen.findByRole("list", { name: "Admins" });
    fireEvent.click(screen.getByRole("button", { name: "New admin" }));
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "typed@example.com" } });
    act(() => session.set(signedAs("admin", "admin_2")));
    await waitFor(() => expect(document.getElementById("admin-email")).toBeNull());
  });
});

// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import { LINK, person, signedAs } from "../../test/adminFixtures";
import AdminsPage from "./AdminsPage";

function renderAdmins() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <AdminsPage />
    </QueryClientProvider>,
  );
  return qc;
}

const path = (c: Call) => new URL(c.url, "http://x").pathname;

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

  it("the last active admin cannot be removed, and the page says why (guard: isLastActiveAdmin)", async () => {
    stubFetch((c) => (path(c) === "/api/v1/admins" ? { status: 200, body: { items: [person()] } } : fail(404, "not_found")));
    renderAdmins();
    const remove = await screen.findByRole("button", { name: /Remove/ });
    expect((remove as HTMLButtonElement).disabled).toBe(true);
    expect(screen.getByText("The last active admin cannot be removed.")).toBeTruthy();
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
  // The request may still add the admin, so the form stays open until the answer comes (guard: busy on the form).
  it("Cancel, Escape and the close button do nothing while the request is out (guard: busy)", async () => {
    let answer!: (a: { status: number; body?: unknown }) => void;
    stubFetch((c: Call) =>
      c.method === "POST" ? new Promise((r) => (answer = r)) : { status: 200, body: { items: [person()] } },
    );
    renderAdmins();
    await screen.findByRole("list", { name: "Admins" });
    fireEvent.click(screen.getByRole("button", { name: "New admin" }));
    fireEvent.change(document.getElementById("admin-email") as HTMLInputElement, { target: { value: "bo@example.com" } });
    fireEvent.click(document.querySelector('form button[type="submit"]') as HTMLButtonElement);
    await waitFor(() => expect(answer).toBeDefined());
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.keyDown(document.querySelector(".ant-modal") as HTMLElement, { key: "Escape" });
    const close = document.querySelector(".ant-modal-close");
    if (close) fireEvent.click(close);
    expect(document.getElementById("admin-email")).not.toBeNull();
    answer({ status: 201, body: { ...person({ id: "usr_2", email: "bo@example.com", display_name: "Bo" }), verification: { method: "link", url: LINK } } });
    expect(await screen.findByDisplayValue(LINK)).toBeTruthy();
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

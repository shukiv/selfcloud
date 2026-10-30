// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { limitBytes } from "../../lib/quota";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch } from "../../test/fetchStub";
import StoragePage from "./StoragePage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

const ACCOUNT_STORAGE = {
  account: { used_bytes: 5_000_000_000, reserved_bytes: 0, quota_bytes: 50_000_000_000, available_bytes: 45_000_000_000, user_quota_bytes: 0 },
  folders: [{ id: "fld_1", name: "Plans", kind: "team", used_bytes: 100, used_files: 1, reserved_bytes: 0 }],
  users: [
    { id: "usr_a", display_name: "Ada", email: "ada@example.com", status: "active", used_bytes: 10, reserved_bytes: 0, quota_bytes: null, effective_quota_bytes: null },
    { id: "usr_b", display_name: "Bo", email: "bo@example.com", status: "active", used_bytes: 900, reserved_bytes: 0, quota_bytes: 1_000_000_000, effective_quota_bytes: 1_000_000_000 },
  ],
  users_next_cursor: null,
};

function renderPage() {
  const qc = createQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <StoragePage />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

describe("manage storage", () => {
  it("a failed first load shows the error, never the empty states (guard: error before the empty states)", async () => {
    // Only the account read fails here: the largest-files read answers, so its own alert cannot stand in for this one.
    stubFetch((c) => {
      if (c.url.includes("/storage/largest")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/storage")) return fail(403, "forbidden");
      return fail(404, "not_found");
    });
    renderPage();
    const alerts = await screen.findAllByRole("alert");
    expect(alerts.every((a) => a.className.includes("ant-alert-error"))).toBe(true);
    expect(screen.queryByText("No people yet.")).toBeNull();
    expect(screen.queryByText("No team spaces yet.")).toBeNull();
    expect(screen.queryByText("No team spaces yet.")).toBeNull();
  });

  it("people are listed by use, and a failed read of the largest files keeps the rest (guard: each section has its own error)", async () => {
    stubFetch((c) => {
      if (c.url.includes("/storage/largest")) return fail(403, "forbidden");
      if (c.url.includes("/storage")) return { status: 200, body: ACCOUNT_STORAGE };
      return fail(404, "not_found");
    });
    renderPage();
    const rows = await screen.findAllByRole("row");
    expect(rows[1].textContent).toContain("Bo");
    expect(screen.getByText("Plans")).toBeTruthy();
    const alerts = await screen.findAllByRole("alert");
    expect(alerts.some((a) => a.className.includes("ant-alert-error"))).toBe(true);
  });

  it("an invalid default limit is refused with no request (guard: the invalid check before the PATCH)", async () => {
    const { calls } = stubFetch((c) => (c.url.includes("/storage") && !c.url.includes("/largest") ? { status: 200, body: ACCOUNT_STORAGE } : c.url.includes("/largest") ? { status: 200, body: { items: [] } } : fail(404, "not_found")));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Change limit" }));
    fireEvent.change(screen.getByLabelText("Amount"), { target: { value: "lots" } });
    fireEvent.click(screen.getByRole("button", { name: /Save limit/ }));
    expect(await screen.findByText(/Enter an amount as a positive number/)).toBeTruthy();
    expect(calls.filter((c) => c.method === "PATCH")).toEqual([]);
  });

  it("the default limit is saved as bytes, to the storage policy (guard: setPolicyDefault with limitBytes)", async () => {
    const { calls } = stubFetch((c) => {
      if (c.method === "PATCH") return { status: 200, body: { user_quota_bytes: limitBytes("2", "GB") } };
      if (c.url.includes("/largest")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/storage")) return { status: 200, body: ACCOUNT_STORAGE };
      return fail(404, "not_found");
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Change limit" }));
    fireEvent.change(screen.getByLabelText("Amount"), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: /Save limit/ }));
    await waitFor(() => expect(calls.filter((c) => c.method === "PATCH")).toHaveLength(1));
    expect(calls.find((c) => c.method === "PATCH")?.url).toBe("/api/v1/accounts/acc_1/storage-policy");
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ user_quota_bytes: limitBytes("2", "GB") });
  });

  // A refused limit is said where the owner is looking: in the editor, which stays open so the owner can try again.
  it("a refused default limit shows the refusal in the editor, and the dialog stays open (guard: rethrow to the editor)", async () => {
    stubFetch((c) => {
      if (c.method === "PATCH") return fail(403, "forbidden");
      if (c.url.includes("/largest")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/storage")) return { status: 200, body: ACCOUNT_STORAGE };
      return fail(404, "not_found");
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Change limit" }));
    fireEvent.change(screen.getByLabelText("Amount"), { target: { value: "2" } });
    fireEvent.click(screen.getByRole("button", { name: /Save limit/ }));
    const dialog = await screen.findByRole("dialog");
    expect(await within(dialog).findByRole("alert")).toBeTruthy();
    expect(within(dialog).getByRole("alert").textContent).toContain("not allowed");
  });
});

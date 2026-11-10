// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { limitBytes } from "../../lib/quota";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch } from "../../test/fetchStub";
import { phoneViewport } from "../../test/viewport";
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
    expect(screen.queryByText("No people yet")).toBeNull();
    expect(screen.queryByText("No team spaces yet")).toBeNull();
  });

  // Decision 122 (tl3): the three storage tables are the one table. Each keeps its headers, its empty body holds the title, the
  // team spaces sort by name on the client (the list is whole) with aria-sort, and the people keep their order by use.
  it("each storage table is a region with its headers, and an empty table keeps them", async () => {
    stubFetch((c) => {
      if (c.url.includes("/storage/largest")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/storage")) return { status: 200, body: { ...ACCOUNT_STORAGE, folders: [], users: [] } };
      return fail(404, "not_found");
    });
    renderPage();
    const people = await screen.findByRole("region", { name: "People, by use" });
    // An empty list has no row icons, so no Actions column: it comes with the first row's Change icon.
    expect(within(people).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Person", "Used", "Limit"]);
    expect(people.querySelector("tbody .empty-title")?.textContent).toBe("No people yet");
    const spaces = screen.getByRole("region", { name: "Team spaces" });
    expect(within(spaces).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Team space", "Size", "Files"]);
    expect(spaces.querySelector("tbody .empty-title")?.textContent).toBe("No team spaces yet");
    const largest = screen.getByRole("region", { name: "Largest files in team spaces" });
    expect(within(largest).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Team space", "Size", "Modified"]);
    expect(largest.querySelector("tbody .empty-title")?.textContent).toBe("No files yet");
  });

  // Round 2 (H1, design section 4): at 375 px each storage table shows its name column, and the other columns join its second line.
  it("at 375 px each storage table shows its name column, and the other columns join it", async () => {
    stubFetch((c) => {
      if (c.url.includes("/storage/largest")) {
        return {
          status: 200,
          body: { items: [{ id: "nod_1", name: "Big.bin", size: 5, versions_bytes: 0, in_trash: false, modified_at: "2026-10-01T00:00:00Z", folder_id: "fld_1", folder_name: "Plans" }] },
        };
      }
      if (c.url.includes("/storage")) return { status: 200, body: ACCOUNT_STORAGE };
      return fail(404, "not_found");
    });
    const undo = phoneViewport();
    try {
      renderPage();
      const people = await screen.findByRole("region", { name: "People, by use" });
      expect(within(people).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Person", "Actions"]);
      expect(people.querySelector("tbody tr td")?.textContent).toContain("Follows the default");
      const spaces = screen.getByRole("region", { name: "Team spaces" });
      expect(within(spaces).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Team space"]);
      const largest = await screen.findByRole("region", { name: "Largest files in team spaces" });
      expect(within(largest).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name"]);
      expect(largest.querySelector("tbody tr td")?.textContent).toContain("Plans");
    } finally {
      undo();
    }
  });

  it("the team spaces sort by name, ascending first, and the header's click reverses it with aria-sort", async () => {
    stubFetch((c) => {
      if (c.url.includes("/storage/largest")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/storage")) {
        return {
          status: 200,
          body: {
            ...ACCOUNT_STORAGE,
            folders: [
              { id: "fld_z", name: "Zeta", kind: "team", used_bytes: 1, used_files: 1, reserved_bytes: 0 },
              { id: "fld_a", name: "Alpha", kind: "team", used_bytes: 1, used_files: 1, reserved_bytes: 0 },
            ],
          },
        };
      }
      return fail(404, "not_found");
    });
    renderPage();
    const spaces = await screen.findByRole("region", { name: "Team spaces" });
    const names = () => [...spaces.querySelectorAll("tbody tr[data-id]")].map((r) => r.getAttribute("aria-label"));
    expect(names()).toEqual(["Alpha", "Zeta"]);
    const head = [...spaces.querySelectorAll("thead th")].find((th) => th.textContent?.startsWith("Team space")) as HTMLElement;
    expect(head.getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(head.querySelector("button") as HTMLElement);
    await waitFor(() => expect(names()).toEqual(["Zeta", "Alpha"]));
    expect(head.getAttribute("aria-sort")).toBe("descending");
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

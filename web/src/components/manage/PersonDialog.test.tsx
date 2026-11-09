// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { formatBytes } from "../../lib/files/format";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import type { Person } from "../../lib/manage/types";
import PersonDialog from "./PersonDialog";

const person: Person = {
  id: "usr_ada",
  external_id: null,
  email: "ada@example.com",
  display_name: "Ada",
  locale: "en",
  role: "member",
  status: "active",
  verified_at: null,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
  quota_bytes: null,
  effective_quota_bytes: null,
};

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

function renderDialog(answer: (c: Call) => { status: number; body?: unknown }) {
  stubFetch(answer);
  render(
    <QueryClientProvider client={createQueryClient()}>
      <PersonDialog account="acc_1" person={person} names={{ person: () => undefined, space: () => undefined }} onclose={() => undefined} />
    </QueryClientProvider>,
  );
}

describe("the person dialog", () => {
  it("a failed first load shows the error, not the empty lists (guard: error before the empty sections)", async () => {
    renderDialog((c) => (c.url.includes("/users/usr_ada") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    expect((await screen.findByRole("alert")).className).toContain("ant-alert-error");
    expect(screen.queryByText("No team spaces.")).toBeNull();
  });

  it("an empty limit saves null, which follows the default (guard: the limit editor for a person)", async () => {
    const calls: Call[] = [];
    renderDialog((c) => {
      calls.push(c);
      if (c.method === "PATCH") return { status: 200, body: {} };
      if (c.url.includes("/users/usr_ada")) return { status: 200, body: { ...person, effective_quota_bytes: null, quota_bytes: null } };
      return { status: 200, body: { items: [] } };
    });
    fireEvent.click(await screen.findByRole("button", { name: "Change limit" }));
    fireEvent.click(screen.getByRole("button", { name: /Save limit/ }));
    await waitFor(() => expect(calls.some((c) => c.method === "PATCH")).toBe(true));
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ quota_bytes: null });
  });

  // The person's own limit is on the list, so the limit line shows at once, before the detail has loaded (guard: the limit
  // at once). The detail here never answers.
  it("shows the limit from the list at once, while the detail is still loading (guard: limit at once)", async () => {
    const GB = 1024 ** 3;
    stubFetch((c: Call) => {
      if (c.url.includes("/users/usr_ada")) return new Promise<{ status: number }>(() => undefined);
      return { status: 200, body: { items: [] } };
    });
    render(
      <QueryClientProvider client={createQueryClient()}>
        <PersonDialog account="acc_1" person={{ ...person, quota_bytes: 2 * GB }} names={{ person: () => undefined, space: () => undefined }} onclose={() => undefined} />
      </QueryClientProvider>,
    );
    expect(await screen.findByText(formatBytes(2 * GB, "en"))).toBeTruthy();
  });

  // The save and the re-read are two steps. A kept save whose re-read fails is still reported as saved, with a softer note
  // that the page could not read the person again, and the value shown is the one that was saved (guard: a failed re-read
  // is not a failed save).
  it("a kept limit whose re-read fails is reported as saved, with a softer note (guard: save and re-read are separate)", async () => {
    const GB = 1024 ** 3;
    let saved = false;
    renderDialog((c) => {
      if (c.method === "PATCH") {
        saved = true;
        return { status: 200, body: {} };
      }
      if (c.url.includes("/users/usr_ada")) {
        return saved ? fail(500, "internal") : { status: 200, body: { ...person, quota_bytes: GB, effective_quota_bytes: GB } };
      }
      return { status: 200, body: { items: [] } };
    });
    fireEvent.click(await screen.findByRole("button", { name: "Change limit" }));
    fireEvent.change(screen.getByLabelText("Amount"), { target: { value: "4" } });
    fireEvent.click(screen.getByRole("button", { name: /Save limit/ }));
    expect(await screen.findByText("Limit saved.")).toBeTruthy();
    // The unit typed is GB (decimal), so the saved value is 4 x 1000^3 bytes, shown as the value that was saved.
    expect(screen.getByText(formatBytes(4 * 1000 ** 3, "en"))).toBeTruthy();
    expect(screen.getByText("The limit is saved. The page could not read the person again. Reload the page to see the latest.")).toBeTruthy();
    expect(screen.queryByText(/The server had a problem/)).toBeNull();
  });

  // After a save the dialog shows the value the server applies. It reads the person again before it closes the editor, so the
  // old value and the "Limit saved" notice never show together (guard: no old value after save).
  it("after a limit save the old value is not shown next to the saved notice (guard: re-read before the dialog closes)", async () => {
    const GB = 1024 ** 3;
    let saved = false;
    const seen: string[] = [];
    const observer = new MutationObserver(() => seen.push(document.body.textContent ?? ""));
    renderDialog((c) => {
      if (c.method === "PATCH") {
        saved = true;
        return { status: 200, body: {} };
      }
      if (c.url.includes("/users/usr_ada")) {
        const quota = saved ? 4 * GB : GB;
        return { status: 200, body: { ...person, quota_bytes: quota, effective_quota_bytes: quota } };
      }
      return { status: 200, body: { items: [] } };
    });
    observer.observe(document.body, { subtree: true, childList: true, characterData: true });
    fireEvent.click(await screen.findByRole("button", { name: "Change limit" }));
    fireEvent.change(screen.getByLabelText("Amount"), { target: { value: "4" } });
    fireEvent.click(screen.getByRole("button", { name: /Save limit/ }));
    expect(await screen.findByText(formatBytes(4 * GB, "en"))).toBeTruthy();
    observer.disconnect();
    expect(seen.some((s) => s.includes("Limit saved.") && s.includes(formatBytes(GB, "en")))).toBe(false);
  });

  it("an empty team spaces list and an empty groups list are antd's compact Empty, with their words (decision 111)", async () => {
    renderDialog((c) => (c.url.includes("/users/usr_ada") ? { status: 200, body: { ...person, effective_quota_bytes: null, quota_bytes: null } } : { status: 200, body: { items: [] } }));
    await screen.findByText("No team spaces.");
    const spaces = document.querySelector('section[aria-labelledby="person-spaces"] .ant-empty.ant-empty-normal .ant-empty-description');
    expect(spaces?.textContent).toBe("No team spaces.");
    const groups = document.querySelector('section[aria-labelledby="person-groups"] .ant-empty.ant-empty-normal .ant-empty-description');
    expect(groups?.textContent).toBe("No groups.");
  });
});

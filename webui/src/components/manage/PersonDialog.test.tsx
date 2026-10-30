// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
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
});

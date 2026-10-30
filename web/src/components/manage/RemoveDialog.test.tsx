// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import type { Person } from "../../lib/manage/types";
import RemoveDialog from "./RemoveDialog";

const person: Person = {
  id: "usr_gone",
  external_id: null,
  email: "gone@example.com",
  display_name: "Gone Person",
  locale: "en",
  role: "member",
  status: "active",
  verified_at: null,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
};

const other: Person = { ...person, id: "usr_other", email: "other@example.com", display_name: "Other Member" };

function signedAs(): Session {
  return {
    user: { id: "usr_owner", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

function renderDialog(onclose = vi.fn(), ondone = vi.fn()) {
  const qc = createQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <RemoveDialog account="acc_1" person={person} candidates={[person, other]} onclose={onclose} ondone={ondone} />
    </QueryClientProvider>,
  );
  return { onclose, ondone };
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

const isDelete = (c: Call) => c.method === "DELETE" && c.url.startsWith("/api/v1/accounts/acc_1/users/usr_gone");

describe("removing a person", () => {
  it("sends one request for a double click on the confirmation (guard: the once-at-a-time check in go)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((r) => {
      release = r;
    });
    const { calls } = stubFetch((c) => (isDelete(c) ? held : fail(404, "not_found")));
    renderDialog();
    const remove = screen.getByRole("button", { name: "Remove" });
    // Both presses land in one batch, before React could disable the button: only the once-guard can stop the second.
    act(() => {
      fireEvent.click(remove);
      fireEvent.click(remove);
    });
    await waitFor(() => expect(calls.filter(isDelete)).toHaveLength(1));
    release({ status: 204 });
  });

  it("a conflict with no reason is the files question: the files step opens and asks for a choice", async () => {
    const { calls } = stubFetch((c) => (isDelete(c) && !c.url.includes("delete_files") && !c.url.includes("transfer_to") ? fail(409, "conflict", {}) : { status: 204 }));
    const { ondone } = renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByText("Gone Person has files in My files")).toBeTruthy();
    expect(ondone).not.toHaveBeenCalled();
    fireEvent.click(screen.getByLabelText("Delete them for good"));
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    await waitFor(() => expect(ondone).toHaveBeenCalledTimes(1));
    expect(calls.filter(isDelete).at(-1)?.url).toContain("delete_files=true");
  });

  it("a refused removal keeps the dialog open and shows the error, never the server text (guard: the error stays in the dialog)", async () => {
    stubFetch((c) => (isDelete(c) ? { status: 403, body: { error: { code: "forbidden", message: "SERVER-SECRET-TEXT" } } } : fail(404, "not_found")));
    const { ondone, onclose } = renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toContain("SERVER-SECRET-TEXT");
    expect(ondone).not.toHaveBeenCalled();
    expect(onclose).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /Remove/ })).toBeTruthy();
  });

  it("a session that ends while the removal is out does not report it (guard: sessionChanged before the await)", async () => {
    let release!: (a: Answer) => void;
    const held = new Promise<Answer>((r) => {
      release = r;
    });
    stubFetch((c) => (isDelete(c) ? held : fail(404, "not_found")));
    const { ondone } = renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    session.set(null);
    release({ status: 204 });
    await new Promise((r) => setTimeout(r, 30));
    expect(ondone).not.toHaveBeenCalled();
  });
});

describe("the files question of the remove dialog", () => {
  // The radio group is named by the question it answers, so a screen reader announces the choice with its question (guard: the group name).
  it("the choice of what to do with the files is a named radio group", async () => {
    stubFetch((c) => (isDelete(c) ? fail(409, "conflict", {}) : { status: 204 }));
    renderDialog();
    fireEvent.click(screen.getByRole("button", { name: "Remove" }));
    expect(await screen.findByRole("radiogroup", { name: "My files are private, so they are not listed here. Choose what happens to them." })).toBeTruthy();
  });
});

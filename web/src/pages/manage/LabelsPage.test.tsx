// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import LabelsPage from "./LabelsPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

const NOTE = { id: "lbf_note", name: "Note", type: "text", restricted: false, choices: [], values: 0 };
const STATUS = {
  id: "lbf_status",
  name: "Status",
  type: "choice",
  restricted: true,
  choices: [{ id: "lbc_open", name: "Open", archived: false }],
  values: 3,
};

function renderPage() {
  const qc = createQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <LabelsPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

const isDelete = (c: Call) => c.method === "DELETE" && c.url === "/api/v1/accounts/acc_1/label-fields/lbf_status";

describe("labels page", () => {
  it("a failed first load shows the error, never the empty message", async () => {
    stubFetch(() => fail(500, "internal"));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("No label fields yet.")).toBeNull();
  });

  it("lists each field with its type, its restriction and how many items hold a value", async () => {
    stubFetch(() => ({ status: 200, body: { items: [NOTE, STATUS] } }));
    renderPage();
    const list = await screen.findByRole("list", { name: "Labels" });
    expect(within(list).getByText("Note")).toBeTruthy();
    expect(within(list).getByText("Restricted")).toBeTruthy();
    expect(within(list).getByText("Items with a value: 3")).toBeTruthy();
  });

  it("deleting a field states how many items lose their value, then sends the delete once", async () => {
    const { calls } = stubFetch((c) => {
      if (isDelete(c)) return { status: 204 };
      return { status: 200, body: { items: [NOTE, STATUS] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete: Status/ }));
    expect(await screen.findByText(/on 3 items are removed/)).toBeTruthy();
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /^Delete$/ }));
    await waitFor(() => expect(calls.filter(isDelete)).toHaveLength(1));
  });

  it("the delete dialog counts the values as they are when it opens, not as the list showed them", async () => {
    let loads = 0;
    stubFetch((c) => {
      if (isDelete(c)) return { status: 204 };
      loads += 1;
      // The first load shows 3 values; a value set since then makes 5 by the time Delete is clicked.
      return { status: 200, body: { items: [NOTE, loads > 1 ? { ...STATUS, values: 5 } : STATUS] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete: Status/ }));
    expect(await screen.findByText(/on 5 items are removed/)).toBeTruthy();
  });

  it("creating a field sends its name, type and restriction", async () => {
    const { calls } = stubFetch((c) => {
      if (c.method === "POST") return { status: 201, body: { ...NOTE, id: "lbf_new", name: "Due", type: "date" } };
      return { status: 200, body: { items: [NOTE] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "New field" }));
    fireEvent.change(await screen.findByLabelText("Name"), { target: { value: "Due" } });
    fireEvent.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(calls.some((c) => c.method === "POST")).toBe(true));
    const post = calls.find((c) => c.method === "POST");
    expect(post?.url).toBe("/api/v1/accounts/acc_1/label-fields");
    expect(post?.body).toEqual({ name: "Due", type: "text", restricted: false });
  });
});

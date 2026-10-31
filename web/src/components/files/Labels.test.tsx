// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import type { Node } from "../../lib/files/types";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import Labels from "./Labels";

function signedAs(): Session {
  return {
    user: { id: "usr_ann", email: "ann@example.com", display_name: "Ann", role: "member", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-ann",
    publicLinks: false,
  } as unknown as Session;
}

function nodeWith(role: string): Node {
  return { id: "nod_1", kind: "file", role, name: "report.pdf", size: 1, folder_id: "fld_1", parent_id: null } as unknown as Node;
}

const FIELDS = {
  items: [
    { id: "lbf_note", name: "Note", type: "text", restricted: false, choices: [] },
    { id: "lbf_cost", name: "Cost", type: "number", restricted: false, choices: [] },
    {
      id: "lbf_status",
      name: "Status",
      type: "choice",
      restricted: false,
      choices: [
        { id: "lbc_open", name: "Open", archived: false },
        { id: "lbc_old", name: "Old", archived: true },
      ],
    },
  ],
};

function renderLabels(node: Node) {
  const qc = createQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <Labels node={node} />
    </QueryClientProvider>,
  );
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

const isSet = (field: string) => (c: Call) => c.method === "PUT" && c.url === `/api/v1/nodes/nod_1/labels/${field}`;
const isClear = (field: string) => (c: Call) => c.method === "DELETE" && c.url === `/api/v1/nodes/nod_1/labels/${field}`;

describe("labels in the details", () => {
  it("a reader sees the values as text and no control; a restricted field it was not sent is absent", async () => {
    stubFetch((c) => {
      if (c.url.endsWith("/label-fields")) return { status: 200, body: FIELDS };
      return {
        status: 200,
        body: {
          items: [
            { field_id: "lbf_note", name: "Note", type: "text", restricted: false, value: "Ask Dana" },
            { field_id: "lbf_cost", name: "Cost", type: "number", restricted: false, value: 1234.5 },
            { field_id: "lbf_status", name: "Status", type: "choice", restricted: false, value: { id: "lbc_old", name: "Old", archived: true } },
          ],
        },
      };
    });
    renderLabels(nodeWith("viewer"));
    expect(await screen.findByText("Ask Dana")).toBeTruthy();
    expect(screen.getByText("1,234.5")).toBeTruthy();
    expect(screen.getByText(/Old/)).toBeTruthy();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: /Save/ })).toBeNull();
  });

  it("an editor saves a text value, and clears it", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.endsWith("/label-fields")) return { status: 200, body: FIELDS };
      if (c.method === "PUT" || c.method === "DELETE") return { status: c.method === "PUT" ? 204 : 204 };
      return { status: 200, body: { items: [{ field_id: "lbf_note", name: "Note", type: "text", restricted: false, value: "old" }] } };
    });
    renderLabels(nodeWith("editor"));
    const box = await screen.findByLabelText("Value for Note");
    fireEvent.change(box, { target: { value: "new text" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);
    await waitFor(() => expect(calls.some(isSet("lbf_note"))).toBe(true));
    expect(calls.find(isSet("lbf_note"))?.body).toEqual({ value: "new text" });
    fireEvent.click(screen.getAllByRole("button", { name: "Clear" })[0]);
    await waitFor(() => expect(calls.some(isClear("lbf_note"))).toBe(true));
  });

  it("a number that is not a number is refused on the page, and nothing is sent", async () => {
    const { calls } = stubFetch((c) => {
      if (c.url.endsWith("/label-fields")) return { status: 200, body: { items: [FIELDS.items[1]] } };
      return { status: 200, body: { items: [{ field_id: "lbf_cost", name: "Cost", type: "number", restricted: false, value: null }] } };
    });
    renderLabels(nodeWith("editor"));
    fireEvent.change(await screen.findByLabelText("Value for Cost"), { target: { value: "twelve" } });
    fireEvent.click(screen.getAllByRole("button", { name: "Save" })[0]);
    expect(await screen.findByText("Enter a number.")).toBeTruthy();
    expect(calls.some(isSet("lbf_cost"))).toBe(false);
  });

  it("a failed load of the values shows the error, not an empty section", async () => {
    stubFetch((c) => (c.url.endsWith("/label-fields") ? { status: 200, body: FIELDS } : fail(500, "internal")));
    renderLabels(nodeWith("viewer"));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByRole("region", { name: "Labels" })).toBeNull();
  });
});

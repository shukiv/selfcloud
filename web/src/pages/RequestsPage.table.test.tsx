// SPDX-License-Identifier: AGPL-3.0-or-later

// Access requests on the one table (tl5, decision 138): the columns, the client sort on Who and Asks, the phone layout (Item and
// Message on a second line, Approve and Deny in the ⋮ menu), the empty table body, and the row's class that the e2e checks read.
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { loadRequestsText } from "../lib/requests/text";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { desktopViewport, PHONE, setViewport } from "../test/viewport";
import RequestsPage from "./RequestsPage";

function signedAs(id: string): Session {
  return {
    user: { id, email: `${id}@example.com`, display_name: id, role: "owner", locale: "en", status: "active" },
    account: null,
    csrf: `csrf-${id}`,
    publicLinks: false,
  } as unknown as Session;
}

const request = (id: string, role: string, user_name: string, message = "") => ({
  id,
  node_id: "nod_item",
  user_id: `usr_${id}`,
  user_name,
  role,
  message,
  status: "pending",
  created_at: "2026-10-01T00:00:00Z",
  decided_at: null,
  decided_by: "",
});

const node = {
  id: "nod_item",
  folder_id: "fld_a",
  parent_id: null,
  name: "Plans",
  kind: "dir",
  size: 0,
  mime_type: "",
  created_at: "2026-10-01T00:00:00Z",
  modified_at: "2026-10-01T00:00:00Z",
  role: "manager",
  owner_id: "usr_owner",
  no_download: false,
  editors_can_share: true,
};

// Zoe asks for viewer (first in the server's order) and Ann for editor (second).
const zoe = request("arq_1", "viewer", "Zoe Ng", "Please");
const ann = request("arq_2", "editor", "Ann Lee");

function answer(items: unknown[]) {
  return stubFetch((c: Call) => {
    if (c.url === "/api/v1/access-requests") return { status: 200, body: { items } };
    if (c.url === "/api/v1/nodes/nod_item") return { status: 200, body: node };
    return fail(404, "not_found");
  });
}

function renderPage() {
  const qc = createQueryClient();
  return render(
    <QueryClientProvider client={qc}>
      <RequestsPage />
    </QueryClientProvider>,
  );
}

const order = (region: HTMLElement) => [...region.querySelectorAll<HTMLElement>("tbody tr.request")].map((r) => r.dataset.id);

let undoView: () => void = () => undefined;

beforeAll(async () => {
  await loadRequestsText("en");
});

beforeEach(() => {
  undoView = desktopViewport();
  session.set(signedAs("olivia"));
});

afterEach(() => {
  undoView();
  session.set(null);
});

describe("access requests are one table (tl5, decision 138)", () => {
  it("draws the requests as the rows of one table, under the columns Who, Item, Asks, Message and Grant", async () => {
    answer([zoe, ann]);
    renderPage();
    const region = await screen.findByRole("region", { name: "Access requests" });
    await waitFor(() => expect(order(region)).toEqual(["arq_1", "arq_2"]));
    const heads = within(region)
      .getAllByRole("columnheader")
      .map((h) => h.textContent ?? "");
    expect(heads).toEqual(expect.arrayContaining(["Who", "Item", "Asks", "Message", "Grant"]));
    expect(document.querySelector(".requests tr.request[data-id='arq_1']")).toBeTruthy();
  });

  it("keeps the server's order until a header is pressed, then sorts Who on this page, and turns the sort over on a second press", async () => {
    answer([zoe, ann]);
    renderPage();
    const region = await screen.findByRole("region", { name: "Access requests" });
    await waitFor(() => expect(order(region)).toEqual(["arq_1", "arq_2"]));
    fireEvent.click(within(region).getByRole("button", { name: "Who" }));
    expect(order(region)).toEqual(["arq_2", "arq_1"]);
    expect(within(region).getByRole("columnheader", { name: /Who/ }).getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(within(region).getByRole("button", { name: "Who" }));
    expect(order(region)).toEqual(["arq_1", "arq_2"]);
    expect(within(region).getByRole("columnheader", { name: /Who/ }).getAttribute("aria-sort")).toBe("descending");
  });

  it("sorts Asks by the role's rank, lowest first", async () => {
    answer([ann, zoe]);
    renderPage();
    const region = await screen.findByRole("region", { name: "Access requests" });
    await waitFor(() => expect(order(region)).toEqual(["arq_2", "arq_1"]));
    fireEvent.click(within(region).getByRole("button", { name: "Asks" }));
    expect(order(region)).toEqual(["arq_1", "arq_2"]);
    fireEvent.click(within(region).getByRole("button", { name: "Asks" }));
    expect(order(region)).toEqual(["arq_2", "arq_1"]);
  });

  it("on a phone, Item and Message are the second line, Grant stays a control, and Approve and Deny are in the ⋮ menu", async () => {
    undoView();
    undoView = setViewport(PHONE);
    answer([zoe]);
    renderPage();
    const row = await screen.findByRole("row", { name: "Zoe Ng" });
    const second = row.querySelector(".row-second")?.textContent ?? "";
    expect(second).toContain("Plans");
    expect(second).toContain("Please");
    expect(within(row).getByRole("combobox")).toBeTruthy();
    expect(within(row).queryByRole("button", { name: "Approve: Zoe Ng" })).toBeNull();
    expect(within(row).getByRole("button", { name: "More actions for Zoe Ng" })).toBeTruthy();
  });

  it("keeps the table on screen when no request waits, with its title and the sentence in the body", async () => {
    answer([]);
    renderPage();
    const region = await screen.findByRole("region", { name: "Access requests" });
    expect(within(region).getByRole("columnheader", { name: "Who" })).toBeTruthy();
    expect(within(region).getByText("Nothing to decide")).toBeTruthy();
    expect(within(region).getByText("No access request is waiting for you.")).toBeTruthy();
  });

  it("makes Approve and Deny icon buttons with no words, named after the asker", async () => {
    answer([zoe]);
    renderPage();
    const row = await screen.findByRole("row", { name: "Zoe Ng" });
    expect(within(row).getByRole("button", { name: "Approve: Zoe Ng" }).textContent).toBe("");
    expect(within(row).getByRole("button", { name: "Deny: Zoe Ng" }).textContent).toBe("");
  });
});

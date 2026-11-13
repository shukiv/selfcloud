// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { expectEmptyTable } from "../../test/emptyTable";
import { DESKTOP, PHONE, setViewport } from "../../test/viewport";
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
    <App>
      <QueryClientProvider client={qc}>
        <LabelsPage />
      </QueryClientProvider>
    </App>,
  );
}

let restoreViewport: () => void = () => undefined;
beforeEach(() => {
  session.set(signedAs());
  restoreViewport = setViewport(DESKTOP);
});
afterEach(() => {
  session.set(null);
  restoreViewport();
});

const isDelete = (c: Call) => c.method === "DELETE" && c.url === "/api/v1/accounts/acc_1/label-fields/lbf_status";

// The rows of the labels table, in the order they are drawn, by their ids.
const rowIDs = (region: HTMLElement) => [...region.querySelectorAll<HTMLElement>("tbody tr[data-id]")].map((r) => r.dataset.id);

describe("labels page", () => {
  it("a failed first load shows the error, never the empty message", async () => {
    stubFetch(() => fail(500, "internal"));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("No labels yet")).toBeNull();
  });

  // The owner's rule of 2026-11-09 (decision 114), and the mockup's "No labels yet": the table keeps its headers.
  it("an empty list keeps its table and headers, with the title, description and New field in the body", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderPage();
    await expectEmptyTable("Labels", {
      headers: ["Name", "Type", "Restricted", "Items with a value"],
      title: "No labels yet",
      description: "Add a field, such as a date or a choice, to describe items.",
      action: "New field",
    });
  });

  it("lists each field as a row, with its type, its restriction and how many items hold a value", async () => {
    stubFetch(() => ({ status: 200, body: { items: [NOTE, STATUS] } }));
    renderPage();
    const region = await screen.findByRole("region", { name: "Labels" });
    const status = await within(region).findByRole("row", { name: "Status" });
    expect(within(status).getByText("Restricted").className).toContain("ant-tag");
    expect(status.querySelectorAll("td")[3]?.textContent).toBe("3");
    const note = await within(region).findByRole("row", { name: "Note" });
    expect(note.querySelectorAll("td")[1]?.textContent).toBe("Text");
  });

  // The list is loaded whole, so Name sorts it here. The first order is by name, A to Z; a second click reverses it.
  it("sorts by name: A to Z first, then Z to A, with aria-sort on the Name header", async () => {
    stubFetch(() => ({ status: 200, body: { items: [STATUS, NOTE] } }));
    renderPage();
    const region = await screen.findByRole("region", { name: "Labels" });
    await waitFor(() => expect(rowIDs(region)).toEqual(["lbf_note", "lbf_status"]));
    const head = (await within(region).findAllByRole("columnheader"))[0];
    expect(head.getAttribute("aria-sort")).toBe("ascending");
    fireEvent.click(within(head).getByRole("button", { name: "Name" }));
    await waitFor(() => expect(rowIDs(region)).toEqual(["lbf_status", "lbf_note"]));
    expect(head.getAttribute("aria-sort")).toBe("descending");
  });

  // Only the sortable header is a button: the other headers are plain, so a click on them does nothing.
  it("only the Name header is a button", async () => {
    stubFetch(() => ({ status: 200, body: { items: [NOTE] } }));
    renderPage();
    const region = await screen.findByRole("region", { name: "Labels" });
    const heads = await within(region).findAllByRole("columnheader");
    expect(heads.slice(1).every((h) => within(h).queryByRole("button") === null)).toBe(true);
  });

  // On a phone the type, the restriction and the count are the name's second line, and the row's actions are ⋮.
  it("on a phone the type, restriction and count are the name's second line, and the actions are one ⋮ menu", async () => {
    restoreViewport();
    restoreViewport = setViewport(PHONE);
    stubFetch(() => ({ status: 200, body: { items: [STATUS] } }));
    renderPage();
    const region = await screen.findByRole("region", { name: "Labels" });
    const row = await within(region).findByRole("row", { name: "Status" });
    expect(row.querySelector(".row-second")?.textContent).toBe("Choice · Restricted · 3");
    expect(within(row).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual(["More actions for Status"]);
  });

  it("a delete that never answers: Cancel closes the dialog and aborts the request (fu4)", async () => {
    const { calls } = stubFetch((c) => {
      if (isDelete(c)) return new Promise<Answer>(() => undefined);
      return { status: 200, body: { items: [NOTE, STATUS] } };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete: Status/ }));
    const dialog = await screen.findByRole("dialog");
    fireEvent.click(within(dialog).getByRole("button", { name: /^Delete$/ }));
    await waitFor(() => expect(calls.filter(isDelete)).toHaveLength(1));
    fireEvent.click(within(dialog).getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.find(isDelete)?.signal?.aborted).toBe(true);
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

  // Edit opens the field's form with its current name and type.
  it("Edit opens the form for the field, with its name in it", async () => {
    stubFetch((c) => {
      if (c.method === "GET") return { status: 200, body: { items: [STATUS] } };
      return { status: 404, body: {} };
    });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Edit: Status" }));
    await waitFor(() => expect((document.getElementById("label-form-name") as HTMLInputElement | null)?.value).toBe("Status"));
  });
});

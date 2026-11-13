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
import SpacesPage from "./SpacesPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

const SPACE = {
  id: "fld_1",
  name: "Plans",
  kind: "team",
  root_node_id: "nod_1",
  members_only: false,
  sharing_managers_only: false,
  no_download_for_viewers: false,
  allow_links: true,
  created_at: "2026-10-01T00:00:00Z",
  updated_at: "2026-10-01T00:00:00Z",
};

function renderPage() {
  const qc = createQueryClient();
  render(
    <App>
      <QueryClientProvider client={qc}>
        <SpacesPage />
      </QueryClientProvider>
    </App>,
  );
  return qc;
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

const isSpaceDelete = (c: Call) => c.method === "DELETE" && c.url === "/api/v1/accounts/acc_1/folders/fld_1";

// answerFor serves the team space list, its totals and the deletion the test chooses.
function answerFor(del: (c: Call) => { status: number; body?: unknown } | null) {
  return (c: Call) => {
    const d = del(c);
    if (d) return d;
    if (c.url.startsWith("/api/v1/accounts/acc_1/folders/fld_1") && c.method === "GET") return { status: 200, body: { ...SPACE, files: 2, bytes: 1024 } };
    if (c.url.startsWith("/api/v1/accounts/acc_1/folders")) return { status: 200, body: { items: [SPACE] } };
    return fail(404, "not_found");
  };
}

// openDelete opens the space's delete confirmation. Delete is the fourth action, so it is in the row's ⋮ menu.
async function openDelete() {
  fireEvent.click(await screen.findByRole("button", { name: "More actions for Plans" }));
  fireEvent.click(await screen.findByRole("menuitem", { name: "Delete: Plans" }));
}

describe("team spaces", () => {
  // The owner's rule of 2026-11-09 (decision 114): the empty table keeps its headers, with the Empty and New team space in the body.
  it("an empty list keeps its table and headers, with the title, description and New team space in the body", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderPage();
    await expectEmptyTable("Team spaces", {
      headers: ["Name", "Access"],
      title: "No team spaces yet",
      description: "Create one to share files.",
      action: "New team space",
    });
  });

  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c) => (c.url.includes("/folders") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText(/No team spaces yet/)).toBeNull();
  });

  // Share, Settings and Rename are inline icons on a desktop; the fourth action, Delete, is in the ⋮ menu.
  it("a space has three icons inline on a desktop, and Delete in its ⋮ menu", async () => {
    stubFetch(answerFor(() => null));
    renderPage();
    const row = await screen.findByRole("row", { name: "Plans" });
    expect(within(row).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual([
      "Share: Plans",
      "Settings: Plans",
      "Rename: Plans",
      "More actions for Plans",
    ]);
  });

  // below md the flags are the name's second line, and the actions are the ⋮ menu alone.
  it("on a phone the flags are the name's second line, and the actions are one ⋮ menu", async () => {
    restoreViewport();
    restoreViewport = setViewport(PHONE);
    stubFetch(answerFor(() => null));
    renderPage();
    const row = await screen.findByRole("row", { name: "Plans" });
    expect(within(row).getAllByRole("button").map((b) => b.getAttribute("aria-label"))).toEqual(["More actions for Plans"]);
    const headers = within(screen.getByRole("region", { name: "Team spaces" })).getAllByRole("columnheader").map((h) => h.textContent);
    expect(headers).not.toContain("Access");
  });

  // The Access column shows each flag the space has as a Tag on a desktop.
  it("a space's flags are Tags in its Access cell on a desktop", async () => {
    stubFetch((c) => (c.url.startsWith("/api/v1/accounts/acc_1/folders") ? { status: 200, body: { items: [{ ...SPACE, members_only: true }] } } : fail(404, "not_found")));
    renderPage();
    const row = await screen.findByRole("row", { name: "Plans" });
    expect(within(row).getByText("Members only").className).toContain("ant-tag");
  });

  // Below md the flags are the name's second line, in muted text.
  it("on a phone the flags are the name's second line", async () => {
    restoreViewport();
    restoreViewport = setViewport(PHONE);
    stubFetch((c) => (c.url.startsWith("/api/v1/accounts/acc_1/folders") ? { status: 200, body: { items: [{ ...SPACE, members_only: true }] } } : fail(404, "not_found")));
    renderPage();
    const row = await screen.findByRole("row", { name: "Plans" });
    expect(row.querySelector(".row-second")?.textContent).toBe("Members only");
  });

  it("a delete that never answers: Cancel closes the dialog and aborts the request (fu4, item 2)", async () => {
    const { calls } = stubFetch((c) => (isSpaceDelete(c) ? new Promise<Answer>(() => undefined) : answerFor(() => null)(c)));
    renderPage();
    await openDelete();
    await screen.findByRole("button", { name: /Delete team space/ });
    fireEvent.change(document.getElementById("space-delete-name") as HTMLInputElement, { target: { value: "Plans" } });
    fireEvent.click(await screen.findByRole("button", { name: /Delete team space/ }));
    await waitFor(() => expect(calls.filter(isSpaceDelete)).toHaveLength(1));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(calls.find(isSpaceDelete)?.signal?.aborted).toBe(true);
  });

  it("deleting a space needs its exact name typed; the button stays off until it matches (guard: requireName on the confirmation)", async () => {
    const { calls } = stubFetch(answerFor(() => null));
    renderPage();
    await openDelete();
    const go = await screen.findByRole("button", { name: /Delete team space/ });
    expect((go as HTMLButtonElement).disabled).toBe(true);
    const input = document.getElementById("space-delete-name") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "plans" } });
    expect((screen.getByRole("button", { name: /Delete team space/ }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.change(input, { target: { value: "Plans" } });
    expect((screen.getByRole("button", { name: /Delete team space/ }) as HTMLButtonElement).disabled).toBe(false);
    fireEvent.click(screen.getByRole("button", { name: /Delete team space/ }));
    await waitFor(() => expect(calls.filter(isSpaceDelete)).toHaveLength(1));
    expect(await screen.findByText("Plans was deleted.")).toBeTruthy();
  });

  it("a refused deletion keeps the confirmation open with the error (guard: the dialog is kept on failure)", async () => {
    stubFetch(
      answerFor((c) => (isSpaceDelete(c) ? { status: 403, body: { error: { code: "forbidden", message: "SERVER-ONLY" } } } : null)),
    );
    renderPage();
    await openDelete();
    await screen.findByRole("button", { name: /Delete team space/ });
    fireEvent.change(document.getElementById("space-delete-name") as HTMLInputElement, { target: { value: "Plans" } });
    fireEvent.click(screen.getByRole("button", { name: /Delete team space/ }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toContain("SERVER-ONLY");
    expect(document.getElementById("space-delete-name")).toBeTruthy();
  });
});

// The Share, Settings and Rename icons, each tested on its own.
describe("a space's Share, Settings and Rename icons", () => {
  it("Share reads the space's root node, which the share dialog works on", async () => {
    const { calls } = stubFetch(answerFor(() => null));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Share: Plans" }));
    await waitFor(() => expect(calls.some((c) => c.method === "GET" && c.url === "/api/v1/nodes/nod_1")).toBe(true));
  });

  it("Settings opens the space's settings dialog, titled with its name", async () => {
    stubFetch(answerFor(() => null));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Settings: Plans" }));
    const dialog = await screen.findByRole("dialog");
    expect(dialog.textContent).toContain("Settings for Plans");
  });

  it("Rename opens the rename dialog with the space's current name in it", async () => {
    stubFetch(answerFor(() => null));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Rename: Plans" }));
    await waitFor(() => expect((document.getElementById("space-rename-name") as HTMLInputElement | null)?.value).toBe("Plans"));
  });
});

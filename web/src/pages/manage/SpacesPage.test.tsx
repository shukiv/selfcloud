// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
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
    <QueryClientProvider client={qc}>
      <SpacesPage />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

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

describe("team spaces", () => {
  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c) => (c.url.includes("/folders") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText(/No team spaces yet/)).toBeNull();
  });

  it("deleting a space needs its exact name typed; the button stays off until it matches (guard: requireName on the confirmation)", async () => {
    const { calls } = stubFetch(answerFor(() => null));
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Plans/ }));
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
    fireEvent.click(await screen.findByRole("button", { name: /Delete.*Plans/ }));
    await screen.findByRole("button", { name: /Delete team space/ });
    fireEvent.change(document.getElementById("space-delete-name") as HTMLInputElement, { target: { value: "Plans" } });
    fireEvent.click(screen.getByRole("button", { name: /Delete team space/ }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).not.toContain("SERVER-ONLY");
    expect(document.getElementById("space-delete-name")).toBeTruthy();
  });
});

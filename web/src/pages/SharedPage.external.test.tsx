// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../lib/query";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import { desktopViewport } from "../test/viewport";
import SharedPage from "./SharedPage";

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
  window.history.replaceState(null, "", "/");
});
afterEach(() => {
  undoView();
  session.set(null);
  window.history.replaceState(null, "", "/");
});

function signedAs(externalSharing: boolean, emailProved = false): Session {
  return {
    user: { id: "usr_olivia", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-olivia",
    publicLinks: false,
    accountExternalSharing: externalSharing,
    emailProved,
  } as unknown as Session;
}

const externalItem = {
  id: "nod_ext0001",
  name: "Report.pdf",
  kind: "file",
  size: 10,
  modified_at: "2026-11-02T00:00:00Z",
  mime: "application/pdf",
  account_name: "Globex",
  shared_by_name: "Mia Outside",
  role: "viewer",
  expires_at: null,
};

// stub answers the two shared lists and the proof route. proved flips when the proof mail is asked for and the server
// answers 200, as it would once the address is proved.
function stub(proved: boolean, opts: { proof?: { status: number; body?: unknown }; items?: unknown[] } = {}) {
  let isProved = proved;
  const { calls } = stubFetch((c: Call) => {
    if (c.url === "/api/v1/shared/external") return { status: 200, body: { items: opts.items ?? [externalItem], email_proved: isProved } };
    if (c.url.startsWith("/api/v1/shared")) return { status: 200, body: { items: [], next_cursor: null } };
    if (c.url === "/api/v1/me/email-proof" && c.method === "POST") {
      const answer = opts.proof ?? { status: 202, body: { proved: false, sent: true } };
      if (answer.status === 200) isProved = true;
      return answer;
    }
    return fail(404, "not_found");
  });
  return { calls };
}

function renderPage() {
  const qc = createQueryClient();
  const view = render(
    <QueryClientProvider client={qc}>
      <SharedPage />
    </QueryClientProvider>,
  );
  return { qc, ...view };
}

describe("the From other accounts section", () => {
  it("is absent while the account's switch is off, and reads nothing for it", async () => {
    session.set(signedAs(false));
    const { calls } = stub(true);
    renderPage();
    await screen.findByRole("region", { name: "Shared with me" });
    expect(screen.queryByRole("heading", { name: "From other accounts" })).toBeNull();
    expect(calls.some((c) => c.url === "/api/v1/shared/external")).toBe(false);
  });

  it("an unproved address is asked to prove it, with the send button", async () => {
    session.set(signedAs(true, false));
    stub(false);
    renderPage();
    expect(await screen.findByText("Prove your email address to see what people in other accounts share with you.")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Send me a link" })).toBeTruthy();
    expect(screen.queryByText("Report.pdf")).toBeNull();
  });

  it("the send button posts the proof, and a sent mail says where it went", async () => {
    session.set(signedAs(true, false));
    const { calls } = stub(false);
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Send me a link" }));
    await waitFor(() => expect(calls.some((c) => c.url === "/api/v1/me/email-proof" && c.method === "POST")).toBe(true));
    expect(await screen.findByText("We sent a link to olivia@example.com. Open it on this device or any other.")).toBeTruthy();
  });

  it("a mail the server could not send says so", async () => {
    session.set(signedAs(true, false));
    stub(false, { proof: { status: 202, body: { proved: false, sent: false } } });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Send me a link" }));
    expect(await screen.findByText("The proof email could not be sent. Try again later.")).toBeTruthy();
  });

  it("too many proof asks say the rate limit, not the server's text", async () => {
    session.set(signedAs(true, false));
    stub(false, { proof: { status: 429, body: { error: { code: "rate_limited", details: {} } } } });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Send me a link" }));
    expect(await screen.findByText("Too many attempts. Try again in 1 minute.")).toBeTruthy();
  });

  it("a proof the server answers as already proved refetches and shows the items", async () => {
    session.set(signedAs(true, false));
    stub(false, { proof: { status: 200, body: { proved: true, sent: false } } });
    renderPage();
    fireEvent.click(await screen.findByRole("button", { name: "Send me a link" }));
    expect(await screen.findByText("Report.pdf")).toBeTruthy();
  });

  it("a proved address sees the items with their sharer and account, and no thumbnails are asked for", async () => {
    session.set(signedAs(true, true));
    const { calls } = stub(true);
    renderPage();
    const section = await screen.findByRole("region", { name: "From other accounts" });
    const rows = within(section).getAllByRole("row").filter((r) => r.hasAttribute("data-id"));
    expect(rows).toHaveLength(1);
    expect(rows[0].textContent).toContain("Report.pdf");
    expect(rows[0].textContent).toContain("Mia Outside · Globex");
    expect(calls.some((c) => c.url.includes("/thumbnail"))).toBe(false);
  });

  it("a row opens the item at /x/{id}, by click and by Enter", async () => {
    session.set(signedAs(true, true));
    stub(true);
    renderPage();
    const section = await screen.findByRole("region", { name: "From other accounts" });
    const row = within(section).getAllByRole("row").filter((r) => r.hasAttribute("data-id"))[0];
    fireEvent.click(row);
    await waitFor(() => expect(window.location.pathname).toBe("/x/nod_ext0001"));
    window.history.replaceState(null, "", "/");
    row.focus();
    fireEvent.keyDown(row, { key: "Enter", keyCode: 13 });
    await waitFor(() => expect(window.location.pathname).toBe("/x/nod_ext0001"));
  });

  it("nothing shared from other accounts shows the empty state inside the table", async () => {
    session.set(signedAs(true, true));
    stub(true, { items: [] });
    renderPage();
    expect(await screen.findByText("Nothing from other accounts is shared with you yet.")).toBeTruthy();
    expect(document.querySelector(".empty-table")).not.toBeNull();
  });
});
// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import AuditPage from "./AuditPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

const ENTRY = {
  id: "aud_1",
  action: "folder.create",
  actor: { kind: "user", id: "usr_owen" },
  target: "fld_1",
  details: {},
  time: "2026-10-01T00:00:00Z",
};

function renderPage() {
  const qc = createQueryClient();
  render(
    <QueryClientProvider client={qc}>
      <AuditPage />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

describe("the audit log", () => {
  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c: Call) => (c.url.includes("/audit") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect((await screen.findByRole("alert")).className).toContain("ant-alert-error");
    expect(screen.queryByText("Nothing recorded yet.")).toBeNull();
  });

  it("a failed read of the names says so, and the list still shows (guard: names failure is not silent)", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/audit")) return { status: 200, body: { items: [ENTRY] } };
      return fail(403, "forbidden");
    });
    renderPage();
    expect(await screen.findByText("folder.create")).toBeTruthy();
    expect(await screen.findByText(/The account has too many people to look up every name here/)).toBeTruthy();
  });

  it("the action filter is sent to the server (guard: action in the request)", async () => {
    const { calls } = stubFetch((c: Call) => (c.url.includes("/audit") ? { status: 200, body: { items: [ENTRY] } } : { status: 200, body: { items: [] } }));
    renderPage();
    await screen.findByText("folder.create");
    const selector = document.getElementById("audit-action")!.closest(".ant-select")!.querySelector(".ant-select-selector") as HTMLElement;
    fireEvent.mouseDown(selector);
    const options = await screen.findAllByText("user.create");
    fireEvent.click(options[options.length - 1]);
    await waitFor(() => expect(calls.some((c) => c.url.includes("action=user.create"))).toBe(true));
  });
});

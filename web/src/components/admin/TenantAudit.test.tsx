// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import TenantAudit from "./TenantAudit";
import { signedAs } from "../../test/adminFixtures";

function renderAudit() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  render(
    <QueryClientProvider client={qc}>
      <TenantAudit account="acc_1" />
    </QueryClientProvider>,
  );
}

beforeEach(() => session.set(signedAs("admin")));
afterEach(() => session.set(null));

// tl1: the audit is a table, newest first. When carries aria-sort descending with no button; Event holds the sentence and its code.
describe("tenant audit table", () => {
  it("is a region whose When header says descending and has no button, and whose rows show the action code", async () => {
    stubFetch((c: Call) =>
      c.url.includes("/audit")
        ? { status: 200, body: { items: [{ id: "e1", time: "2026-11-01T10:00:00Z", action: "key.revoke", actor: "usr_1", subject: "key_1" }] } }
        : { status: 200, body: { items: [] } },
    );
    renderAudit();
    const region = await screen.findByRole("region", { name: "Audit log" });
    const when = region.querySelector('th[aria-sort="descending"]') as HTMLElement;
    expect(when).not.toBeNull();
    expect(when.querySelector("button")).toBeNull();
    expect(region.querySelector("tbody")?.textContent).toContain("key.revoke");
  });
});

describe("tenant audit log", () => {
  it("a failed first load shows the error and no empty state (guard: firstFailed)", async () => {
    stubFetch((c: Call) => (c.url.includes("/audit") ? fail(500, "internal") : { status: 200, body: { items: [] } }));
    renderAudit();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByText("Nothing recorded yet.")).toBeNull();
  });

  it("the action filter is sent to the server", async () => {
    const { calls } = stubFetch((c: Call) => (c.url.includes("/audit") ? { status: 200, body: { items: [] } } : { status: 200, body: { items: [] } }));
    renderAudit();
    await screen.findByText("Nothing recorded yet.");
    const selector = document.getElementById("tenant-audit-action")!.closest(".ant-select")!.querySelector(".ant-select-selector") as HTMLElement;
    fireEvent.mouseDown(selector);
    const options = await screen.findAllByText("key.revoke", { selector: ".ant-select-item-option-content" });
    fireEvent.click(options[options.length - 1]);
    await waitFor(() => expect(calls.some((c) => c.url.includes("action=key.revoke"))).toBe(true));
  });
});

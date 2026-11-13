// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import TenantAudit from "./TenantAudit";
import { signedAs } from "../../test/adminFixtures";
import { expectEmptyTable } from "../../test/emptyTable";

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
    // The first render is the phone layout until antd knows the breakpoint, and the data arrives later: the When header is awaited.
    const when = (await waitFor(() => {
      const th = region.querySelector('th[aria-sort="descending"]');
      if (th === null) throw new Error("the When header is not drawn yet");
      return th as HTMLElement;
    })) as HTMLElement;
    expect(when.querySelector("button")).toBeNull();
    expect(region.querySelector("tbody")?.textContent).toContain("key.revoke");
    // The event column is named as the filter above it names what it filters: Action.
    await waitFor(() => expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Action", "When"]));
  });

  // A screen reader hears a row as its sentence, not as the action's code.
  it("names each row by its sentence, not by the action code", async () => {
    stubFetch((c: Call) =>
      c.url.includes("/audit")
        ? { status: 200, body: { items: [{ id: "e1", time: "2026-11-01T10:00:00Z", action: "key.revoke", actor: "usr_1", subject: "key_1" }] } }
        : { status: 200, body: { items: [] } },
    );
    renderAudit();
    const region = await screen.findByRole("region", { name: "Audit log" });
    const row = region.querySelector("tbody tr[data-id]") as HTMLElement;
    const label = row.getAttribute("aria-label") ?? "";
    expect(label).not.toBe("key.revoke");
    expect(label.length).toBeGreaterThan(5);
    expect((row.querySelector("td")?.textContent ?? "").startsWith(label)).toBe(true);
  });
});

describe("tenant audit log", () => {
  it("a failed first load shows the error and no empty state (guard: firstFailed)", async () => {
    stubFetch((c: Call) => (c.url.includes("/audit") ? fail(500, "internal") : { status: 200, body: { items: [] } }));
    renderAudit();
    expect((await screen.findByRole("alert")).textContent).toContain("The server had a problem");
    expect(screen.queryByText("Nothing recorded yet")).toBeNull();
  });

  // The owner's rule of 2026-11-09 (decision 114): the empty table keeps its headers; the audit log has no create action.
  it("an empty log keeps its header row, and its Empty in the table has no action", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderAudit();
    await expectEmptyTable("Audit log", {
      headers: ["Action", "When"],
      title: "Nothing recorded yet",
      description: "Sign-ins, changes to people and keys, and other actions show here.",
    });
  });

  it("the action filter is sent to the server", async () => {
    const { calls } = stubFetch((c: Call) => (c.url.includes("/audit") ? { status: 200, body: { items: [] } } : { status: 200, body: { items: [] } }));
    renderAudit();
    await screen.findByText("Nothing recorded yet");
    const selector = document.getElementById("tenant-audit-action")!.closest(".ant-select")!.querySelector(".ant-select-selector") as HTMLElement;
    fireEvent.mouseDown(selector);
    const options = await screen.findAllByText("key.revoke", { selector: ".ant-select-item-option-content" });
    fireEvent.click(options[options.length - 1]);
    await waitFor(() => expect(calls.some((c) => c.url.includes("action=key.revoke"))).toBe(true));
  });
});

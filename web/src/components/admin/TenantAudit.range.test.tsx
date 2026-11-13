// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { session } from "../../lib/session";
import { stubFetch, type Answer, type Call } from "../../test/fetchStub";
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

const ENTRY = { id: "e1", time: "2026-11-01T10:00:00Z", action: "key.revoke", actor: "usr_1", subject: "key_1" };

const isAudit = (c: Call) => c.url.includes("/audit") && !c.url.includes("/audit/export");
const param = (url: string, key: string) => new URL(url, "https://drive.example.com").searchParams.get(key);
const auditCount = (calls: Call[]) => calls.filter(isAudit).length;

const savedTZ = process.env.TZ;
beforeEach(() => session.set(signedAs("admin")));
afterEach(() => {
  session.set(null);
  vi.unstubAllGlobals();
  if (savedTZ === undefined) delete process.env.TZ;
  else process.env.TZ = savedTZ;
});

describe("the tenant audit log's date range and export", () => {
  it("sends the chosen days' edges in the viewer's time zone, from the first page", async () => {
    process.env.TZ = "Asia/Jerusalem";
    const { calls } = stubFetch((c: Call) => (isAudit(c) ? { status: 200, body: { items: [ENTRY] } } : { status: 200, body: { items: [] } }));
    renderAudit();
    await screen.findByText("key.revoke");

    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-10-25" } });
    await waitFor(() => expect(calls.some((c) => param(c.url, "since") === "2026-10-24T21:00:00.000Z")).toBe(true));

    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-10-26" } });
    await waitFor(() =>
      expect(calls.some((c) => param(c.url, "since") === "2026-10-24T21:00:00.000Z" && param(c.url, "until") === "2026-10-26T22:00:00.000Z")).toBe(true),
    );

    const last = calls.filter(isAudit).at(-1)!;
    expect(param(last.url, "cursor")).toBeNull();
  });

  it("a To before From shows the error, disables Export, and sends nothing new", async () => {
    process.env.TZ = "UTC";
    const { calls } = stubFetch((c: Call) => (isAudit(c) ? { status: 200, body: { items: [ENTRY] } } : { status: 200, body: { items: [] } }));
    renderAudit();
    await screen.findByText("key.revoke");
    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-10-05" } });
    await waitFor(() => expect(calls.some((c) => param(c.url, "since") === "2026-10-05T00:00:00.000Z")).toBe(true));
    const before = auditCount(calls);

    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-10-04" } });
    expect(await screen.findByText("The To date is before the From date")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Export CSV" }) as HTMLButtonElement).disabled).toBe(true);
    await new Promise((r) => setTimeout(r, 80));
    expect(auditCount(calls)).toBe(before);
  });

  it("Export's link carries exactly the action and days shown", async () => {
    process.env.TZ = "UTC";
    const { calls } = stubFetch((c: Call) => (isAudit(c) ? { status: 200, body: { items: [ENTRY] } } : { status: 200, body: { items: [] } }));
    renderAudit();
    await screen.findByText("key.revoke");

    const selector = document.getElementById("tenant-audit-action")!.closest(".ant-select")!.querySelector(".ant-select-selector") as HTMLElement;
    fireEvent.mouseDown(selector);
    const options = await screen.findAllByText("key.revoke", { selector: ".ant-select-item-option-content" });
    fireEvent.click(options[options.length - 1]);
    await waitFor(() => expect(calls.some((c) => param(c.url, "action") === "key.revoke")).toBe(true));

    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-10-01" } });
    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-10-03" } });
    await waitFor(() =>
      expect(calls.some((c) => param(c.url, "action") === "key.revoke" && param(c.url, "since") === "2026-10-01T00:00:00.000Z" && param(c.url, "until") === "2026-10-04T00:00:00.000Z")).toBe(true),
    );
    await waitFor(() => expect(screen.queryByRole("button", { name: "Export CSV" })).not.toBeNull());

    let anchor: HTMLAnchorElement | undefined;
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (this: HTMLAnchorElement) {
      anchor = this;
    });
    try {
      fireEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    } finally {
      click.mockRestore();
    }
    expect(anchor).toBeDefined();
    const url = new URL(anchor!.getAttribute("href")!, "https://drive.example.com");
    expect(url.pathname).toBe("/api/v1/accounts/acc_1/audit/export");
    expect(url.searchParams.get("format")).toBe("csv");
    expect(url.searchParams.get("action")).toBe("key.revoke");
    expect(url.searchParams.get("since")).toBe("2026-10-01T00:00:00.000Z");
    expect(url.searchParams.get("until")).toBe("2026-10-04T00:00:00.000Z");
    expect(url.searchParams.get("actor")).toBeNull();
    expect(anchor!.getAttribute("download")).toBe("");
  });

  it("Export appears once the log has loaded, named and tipped Export CSV", async () => {
    let release!: (a: Answer) => void;
    const pending = new Promise<Answer>((resolve) => {
      release = resolve;
    });
    stubFetch((c: Call) => (isAudit(c) ? pending : { status: 200, body: { items: [] } }));
    renderAudit();
    expect(screen.queryByRole("button", { name: "Export CSV" })).toBeNull();

    release({ status: 200, body: { items: [ENTRY] } });
    const button = await screen.findByRole("button", { name: "Export CSV" });
    fireEvent.pointerEnter(button.parentElement as HTMLElement, { pointerType: "mouse" });
    await waitFor(() => expect(document.querySelector('[role="tooltip"]')?.textContent).toBe("Export CSV"));
  });
});

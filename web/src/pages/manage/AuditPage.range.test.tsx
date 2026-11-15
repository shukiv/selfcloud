// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { loadAudit } from "../../lib/audit/i18n";
import { desktopViewport } from "../../test/viewport";
import { session, type Session } from "../../lib/session";
import { stubFetch, type Answer, type Call } from "../../test/fetchStub";
import AuditPage from "./AuditPage";

beforeAll(async () => {
  await loadAudit("en");
});

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
}

const isAudit = (c: Call) => c.url.includes("/audit") && !c.url.includes("/audit/export");
const param = (url: string, key: string) => new URL(url, "https://drive.example.com").searchParams.get(key);
const auditCount = (calls: Call[]) => calls.filter(isAudit).length;

const savedTZ = process.env.TZ;
let restoreView: () => void = () => undefined;
beforeEach(() => {
  restoreView = desktopViewport();
  session.set(signedAs());
});
afterEach(() => {
  restoreView();
  session.set(null);
  vi.unstubAllGlobals();
  if (savedTZ === undefined) delete process.env.TZ;
  else process.env.TZ = savedTZ;
});

describe("the audit log's date range and export", () => {
  // The chosen day is the viewer's day: the request carries its first and last instants in UTC, so a 23- or 25-hour day
  // is covered whole. A new date reloads from the first page, with no cursor.
  it("sends the chosen days' edges in the viewer's time zone, from the first page", async () => {
    process.env.TZ = "Asia/Jerusalem";
    const { calls } = stubFetch((c: Call) => (isAudit(c) ? { status: 200, body: { items: [ENTRY] } } : { status: 200, body: { items: [] } }));
    renderPage();
    await screen.findByText("folder.create");

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
    renderPage();
    await screen.findByText("folder.create");
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
    renderPage();
    await screen.findByText("folder.create");

    const selector = document.getElementById("audit-action")!.closest(".ant-select")!.querySelector(".ant-select-selector") as HTMLElement;
    fireEvent.mouseDown(selector);
    const options = await screen.findAllByText("user.create");
    fireEvent.click(options[options.length - 1]);
    await waitFor(() => expect(calls.some((c) => param(c.url, "action") === "user.create")).toBe(true));

    fireEvent.change(screen.getByLabelText("From"), { target: { value: "2026-10-01" } });
    fireEvent.change(screen.getByLabelText("To"), { target: { value: "2026-10-03" } });
    await waitFor(() =>
      expect(calls.some((c) => param(c.url, "action") === "user.create" && param(c.url, "since") === "2026-10-01T00:00:00.000Z" && param(c.url, "until") === "2026-10-04T00:00:00.000Z")).toBe(true),
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
    expect(url.searchParams.get("action")).toBe("user.create");
    expect(url.searchParams.get("since")).toBe("2026-10-01T00:00:00.000Z");
    expect(url.searchParams.get("until")).toBe("2026-10-04T00:00:00.000Z");
    expect(url.searchParams.get("actor")).toBeNull();
    expect(anchor!.getAttribute("download")).toBe("");
  });

  it("Export is disabled for five seconds after a click", async () => {
    process.env.TZ = "UTC";
    stubFetch((c: Call) => (isAudit(c) ? { status: 200, body: { items: [ENTRY] } } : { status: 200, body: { items: [] } }));
    renderPage();
    await screen.findByText("folder.create");
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    try {
      vi.useFakeTimers();
      const button = screen.getByRole("button", { name: "Export CSV" }) as HTMLButtonElement;
      fireEvent.click(button);
      expect(button.disabled).toBe(true);
      act(() => vi.advanceTimersByTime(4999));
      expect(button.disabled).toBe(true);
      act(() => vi.advanceTimersByTime(1));
      expect(button.disabled).toBe(false);
    } finally {
      vi.useRealTimers();
      click.mockRestore();
    }
  });

  it("Export appears once the log has loaded, named and tipped Export CSV", async () => {
    let release!: (a: Answer) => void;
    const pending = new Promise<Answer>((resolve) => {
      release = resolve;
    });
    stubFetch((c: Call) => (isAudit(c) ? pending : { status: 200, body: { items: [] } }));
    renderPage();
    expect(screen.queryByRole("button", { name: "Export CSV" })).toBeNull();

    release({ status: 200, body: { items: [ENTRY] } });
    const button = await screen.findByRole("button", { name: "Export CSV" });
    fireEvent.pointerEnter(button.parentElement as HTMLElement, { pointerType: "mouse" });
    await waitFor(() => expect(document.querySelector('[role="tooltip"]')?.textContent).toBe("Export CSV"));
  });
});

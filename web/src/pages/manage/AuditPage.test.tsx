// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { desktopViewport, phoneViewport } from "../../test/viewport";
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

// The desktop columns are the default here; a test of the phone layout asks for it.
let restoreView: () => void = () => undefined;
beforeEach(() => {
  restoreView = desktopViewport();
  session.set(signedAs());
});
afterEach(() => {
  restoreView();
  session.set(null);
});

// Decision 122 (tl3): the audit log is the one table. Its When header is descending with no button, its rows are named by
// their sentence, and an empty log keeps its header row with the title in the body.
describe("the audit log is one table (decision 122)", () => {
  it("is a region whose When header says descending with no button, and whose rows are named by their sentence", async () => {
    stubFetch((c: Call) => (c.url.includes("/audit") ? { status: 200, body: { items: [ENTRY] } } : { status: 200, body: { items: [] } }));
    renderPage();
    const region = await screen.findByRole("region", { name: "Audit log" });
    // The first render is the phone layout (antd's breakpoint is not known yet), so the desktop headers are awaited.
    const when = (await waitFor(() => {
      const th = region.querySelector('th[aria-sort="descending"]');
      if (th === null) throw new Error("the When header is not drawn yet");
      return th as HTMLElement;
    })) as HTMLElement;
    expect(when.querySelector("button")).toBeNull();
    await waitFor(() => expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Action", "When"]));
    const row = region.querySelector("tbody tr[data-id]") as HTMLElement;
    expect(row.getAttribute("aria-label")).not.toBe("folder.create");
    expect(row.getAttribute("aria-label")?.length ?? 0).toBeGreaterThan(5);
  });

  // Round 2 (H1, design section 4): at 375 px only the sentence column shows; When joins its second line.
  it("at 375 px the log shows only the Action column, and the When joins its name cell", async () => {
    stubFetch((c: Call) => (c.url.includes("/audit") ? { status: 200, body: { items: [ENTRY] } } : { status: 200, body: { items: [] } }));
    const undo = phoneViewport();
    try {
      renderPage();
      const region = await screen.findByRole("region", { name: "Audit log" });
      expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Action"]);
      const cell = region.querySelector("tbody tr[data-id] td") as HTMLElement;
      expect(cell.textContent).toContain("folder.create");
      expect(cell.querySelector("time")).not.toBeNull();
    } finally {
      undo();
    }
  });

  it("an empty log keeps its header row, and its title is in the table body", async () => {
    stubFetch(() => ({ status: 200, body: { items: [] } }));
    renderPage();
    const region = await screen.findByRole("region", { name: "Audit log" });
    await waitFor(() => expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Action", "When"]));
    expect(region.querySelector("tbody .empty-title")?.textContent).toBe("Nothing recorded yet");
    expect(region.querySelector("tbody .ant-empty")).not.toBeNull();
    // Round 2 (M3): the audit log's description is the admin catalogue's admin.empty.audit, as the tenant's log has it.
    await waitFor(() => expect(region.querySelector("tbody .empty-desc")?.textContent).toBe("Sign-ins, changes to people and keys, and other actions show here."));
  });
});

describe("the audit log", () => {
  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c: Call) => (c.url.includes("/audit") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect((await screen.findByRole("alert")).className).toContain("ant-alert-error");
    expect(screen.queryByText("Nothing recorded yet")).toBeNull();
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

  // Round 2 (M5): Load more appends the next page of the log; a failed next page keeps the first one, with the error.
  it("Load more appends the next page of the log", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/audit")) {
        return c.url.includes("c2") ? { status: 200, body: { items: [{ ...ENTRY, id: "aud_2", action: "user.create" }] } } : { status: 200, body: { items: [ENTRY], next_cursor: "c2" } };
      }
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Audit log" });
    expect(await within(region).findByText("folder.create")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await within(region).findByText("user.create")).toBeTruthy();
    expect(within(region).getByText("folder.create")).toBeTruthy();
  });

  it("a failed next page keeps the first one and shows the error", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/audit")) {
        return c.url.includes("c2") ? fail(403, "forbidden") : { status: 200, body: { items: [ENTRY], next_cursor: "c2" } };
      }
      return { status: 200, body: { items: [] } };
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Audit log" });
    expect(await within(region).findByText("folder.create")).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Load more" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(within(region).getByText("folder.create")).toBeTruthy();
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

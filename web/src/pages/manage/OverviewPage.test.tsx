// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { desktopViewport, phoneViewport } from "../../test/viewport";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import OverviewPage from "./OverviewPage";

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: true,
  } as unknown as Session;
}

// One activity row, for the table's rows (round 2, M8).
const ACTIVITY = { id: "aud_1", action: "folder.create", actor: { kind: "user", id: "usr_owen" }, target: "fld_1", details: {}, time: "2026-10-01T00:00:00Z" };

const USAGE ={ quota_bytes: null, used_bytes: 0, reserved_bytes: 0, available_bytes: null, files: 0, folders: 0, users_active: 1, users_pending: 0 };

function renderPage() {
  render(
    <QueryClientProvider client={createQueryClient()}>
      <OverviewPage />
    </QueryClientProvider>,
  );
}

// The desktop columns are the default here; a test of the phone layout asks for it (round 2, H1).
let restoreView: () => void = () => undefined;
beforeEach(() => {
  restoreView = desktopViewport();
  session.set(signedAs());
});
afterEach(() => {
  restoreView();
  session.set(null);
});

describe("the owner overview", () => {
  it("a new account with one person and no team space gets the next steps (guard: the fresh rule)", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/usage")) return { status: 200, body: USAGE };
      if (c.url.includes("/folders")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/audit")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/users")) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderPage();
    expect(await screen.findByText("Next steps")).toBeTruthy();
    expect(screen.getByText("Invite your first person")).toBeTruthy();
    expect(screen.getByText("Nothing has happened yet")).toBeTruthy();
  });

  // Decision 122 (tl3): the recent activity is the audit table, with its headers kept when nothing has happened.
  // Round 2 (H1, design section 4): at 375 px only the Action column shows; When joins its name cell.
  it("at 375 px the activity shows only the Action column, and the When joins its name cell", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/usage")) return { status: 200, body: USAGE };
      if (c.url.includes("/folders")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/audit")) return { status: 200, body: { items: [ACTIVITY] } };
      if (c.url.includes("/users")) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    const undo = phoneViewport();
    try {
      renderPage();
      const region = await screen.findByRole("region", { name: "Recent activity" });
      expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Action"]);
      expect((region.querySelector("tbody tr[data-id] td") as HTMLElement).querySelector("time")).not.toBeNull();
    } finally {
      undo();
    }
  });

  it("the recent activity is a table whose empty body keeps the headers (decision 122)", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/usage")) return { status: 200, body: USAGE };
      if (c.url.includes("/folders")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/audit")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/users")) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderPage();
    const region = await screen.findByRole("region", { name: "Recent activity" });
    expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Action", "When"]);
    expect(region.querySelector("tbody .empty-title")?.textContent).toBe("Nothing has happened yet");
  });

  it("a failed first load shows the error and no figures (guard: error before the figures)", async () => {
    stubFetch((c: Call) => (c.url.includes("/usage") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect((await screen.findByRole("alert")).className).toContain("ant-alert-error");
    expect(screen.queryByText("Usage")).toBeNull();
  });

  // The names are read on their own, as the People, Audit and Groups pages read them: a failed names read leaves the figures
  // and the activity, and the names fall back to the generic words (guard: names read apart from the overview).
  it("a failed names read keeps the figures and the activity (guard: names read apart)", async () => {
    stubFetch((c: Call) => {
      if (c.url.includes("/usage")) return { status: 200, body: { ...USAGE, users_active: 2 } };
      if (c.url.includes("/users")) return fail(500, "internal");
      if (c.url.includes("/folders")) return { status: 200, body: { items: [] } };
      if (c.url.includes("/audit")) return { status: 200, body: { items: [] } };
      return fail(404, "not_found");
    });
    renderPage();
    expect(await screen.findByText("Storage")).toBeTruthy();
    // The overview's own failure text is not shown (other lazy parts of the page may show alerts of their own).
    expect(screen.queryByText(/The server had a problem/)).toBeNull();
  });
});

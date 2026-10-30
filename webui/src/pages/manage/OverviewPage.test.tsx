// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
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

const USAGE = { quota_bytes: null, used_bytes: 0, reserved_bytes: 0, available_bytes: null, files: 0, folders: 0, users_active: 1, users_pending: 0 };

function renderPage() {
  render(
    <QueryClientProvider client={createQueryClient()}>
      <OverviewPage />
    </QueryClientProvider>,
  );
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

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
    expect(screen.getByText("Nothing has happened yet.")).toBeTruthy();
  });

  it("a failed first load shows the error and no figures (guard: error before the figures)", async () => {
    stubFetch((c: Call) => (c.url.includes("/usage") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect((await screen.findByRole("alert")).className).toContain("ant-alert-error");
    expect(screen.queryByText("Usage")).toBeNull();
  });
});

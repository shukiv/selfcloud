// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, within } from "@testing-library/react";
import { QueryClientProvider } from "@tanstack/react-query";
import { App } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { stubFetch } from "../../test/fetchStub";
import { DESKTOP, setViewport } from "../../test/viewport";
import LabelsPage from "./LabelsPage";
import SpacesPage from "./SpacesPage";

// The admin catalogue has not loaded: its hook answers false, as it does before the chunk arrives or after it fails. A Manage page
// for a non-admin owner must still name its columns, so the column titles may not come from that catalogue.
vi.mock("../../lib/manage/adminText", () => ({
  useAdminText: () => false,
  loadAdminText: async () => undefined,
}));

function signedAs(): Session {
  return {
    user: { id: "usr_owen", email: "owen@example.com", display_name: "Owen", role: "owner", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

function renderIn(page: "labels" | "spaces") {
  const qc = createQueryClient();
  render(
    <App>
      <QueryClientProvider client={qc}>{page === "labels" ? <LabelsPage /> : <SpacesPage />}</QueryClientProvider>
    </App>,
  );
}

let restoreViewport: () => void = () => undefined;
beforeEach(() => {
  session.set(signedAs());
  restoreViewport = setViewport(DESKTOP);
});
afterEach(() => {
  session.set(null);
  restoreViewport();
});

const NOTE = { id: "lbf_note", name: "Note", type: "text", restricted: false, choices: [], values: 0 };
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

describe("the column titles of a Manage list, before the admin catalogue has loaded", () => {
  it("Labels names its columns in words, never with a key", async () => {
    stubFetch(() => ({ status: 200, body: { items: [NOTE] } }));
    renderIn("labels");
    const region = await screen.findByRole("region", { name: "Labels" });
    expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Type", "Restricted", "Items with a value", "Actions"]);
  });

  it("Team spaces names its columns in words, never with a key", async () => {
    stubFetch((c) => (c.url.startsWith("/api/v1/accounts/acc_1/folders") ? { status: 200, body: { items: [SPACE] } } : { status: 404, body: {} }));
    renderIn("spaces");
    const region = await screen.findByRole("region", { name: "Team spaces" });
    expect(within(region).getAllByRole("columnheader").map((h) => h.textContent)).toEqual(["Name", "Access", "Actions"]);
  });
});

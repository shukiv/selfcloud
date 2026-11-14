// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient, queryKey } from "../lib/query";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Call } from "../test/fetchStub";
import ExternalItemPage from "./ExternalItemPage";

function signedIn(): Session {
  return {
    user: { id: "usr_olivia", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-olivia",
    publicLinks: false,
    accountExternalSharing: true,
    emailProved: true,
  } as unknown as Session;
}

const dir = {
  id: "nod_ext00000000000000000000001",
  name: "Shared folder",
  kind: "dir",
  size: 0,
  modified_at: "2026-11-02T00:00:00Z",
  created_at: "2026-11-01T00:00:00Z",
  no_download: false,
  editors_can_share: false,
};

const file = { ...dir, id: "nod_ext00000000000000000000002", name: "Report.pdf", kind: "file", mime_type: "application/pdf", size: 10 };

const child = { ...dir, id: "nod_chd00000000000000000000001", name: "Notes", kind: "dir" };

function renderAt(path: string, qc = createQueryClient()) {
  const view = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <ExternalItemPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { qc, ...view };
}

beforeEach(() => session.set(signedIn()));
afterEach(() => session.set(null));

describe("the external item page", () => {
  it("an item the caller cannot reach shows the gone empty state", async () => {
    stubFetch(() => fail(404, "not_found"));
    renderAt("/x/nod_ext00000000000000000000001");
    expect(await screen.findByText("This item isn't shared with you, or it is no longer available.")).toBeTruthy();
  });

  it("an address that is not an item's id shows the same, and reads nothing", async () => {
    const { calls } = stubFetch(() => fail(404, "not_found"));
    renderAt("/x/nope");
    expect(await screen.findByText("This item isn't shared with you, or it is no longer available.")).toBeTruthy();
    expect(calls).toHaveLength(0);
  });

  it("a folder opens through the signed-in reads: no visitor credential, no thumbnails", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/nodes/nod_ext00000000000000000000001") return { status: 200, body: dir };
      if (c.url.startsWith("/api/v1/nodes/nod_ext00000000000000000000001/children")) return { status: 200, body: { items: [child], next_cursor: null } };
      return fail(404, "not_found");
    });
    renderAt("/x/nod_ext00000000000000000000001");
    expect(await screen.findByRole("heading", { name: "Shared folder" })).toBeTruthy();
    expect(screen.getByRole("row", { name: "Notes" })).toBeTruthy();
    expect(calls.every((c) => c.auth === null)).toBe(true);
    expect(calls.some((c) => c.url.includes("/thumbnail"))).toBe(false);
    expect(calls.some((c) => c.url.startsWith("/api/v1/share"))).toBe(false);
  });

  it("a file's Download asks the signed link only on the click", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/nodes/nod_ext00000000000000000000002") return { status: 200, body: file };
      if (c.url === "/api/v1/nodes/nod_ext00000000000000000000002/links" && c.method === "POST") {
        return { status: 200, body: { url: "https://content.example/report", expires_at: new Date(Date.now() + 300_000).toISOString() } };
      }
      return fail(404, "not_found");
    });
    const realClick = HTMLAnchorElement.prototype.click;
    HTMLAnchorElement.prototype.click = function () {};
    try {
      renderAt("/x/nod_ext00000000000000000000002");
      const button = await screen.findByRole("button", { name: "Download" });
      expect(calls.some((c) => c.url.endsWith("/links"))).toBe(false);
      fireEvent.click(button);
      await waitFor(() => expect(calls.some((c) => c.url === "/api/v1/nodes/nod_ext00000000000000000000002/links" && c.method === "POST")).toBe(true));
      const post = calls.find((c) => c.url === "/api/v1/nodes/nod_ext00000000000000000000002/links");
      expect((post?.body as { disposition: string }).disposition).toBe("attachment");
    } finally {
      HTMLAnchorElement.prototype.click = realClick;
    }
  });

  it("the byline under the name comes from the cached shared-external answer only", async () => {
    stubFetch((c: Call) => {
      if (c.url === "/api/v1/nodes/nod_ext00000000000000000000001") return { status: 200, body: dir };
      if (c.url.startsWith("/api/v1/nodes/nod_ext00000000000000000000001/children")) return { status: 200, body: { items: [], next_cursor: null } };
      return fail(404, "not_found");
    });
    const qc = createQueryClient();
    qc.setQueryData(queryKey("shared-external"), {
      items: [{ ...file, id: "nod_ext00000000000000000000001", account_name: "Globex", shared_by_name: "Mia Outside", role: "viewer", expires_at: null }],
      email_proved: true,
    });
    const { calls } = { calls: [] as Call[] };
    renderAt("/x/nod_ext00000000000000000000001", qc);
    expect(await screen.findByText("Mia Outside · Globex")).toBeTruthy();
    // Nothing was read for the byline: the page asked only the item's own reads.
    expect(calls).toHaveLength(0);
  });

  it("with no cached answer there is no byline, and nothing is asked for one", async () => {
    const { calls } = stubFetch((c: Call) => {
      if (c.url === "/api/v1/nodes/nod_ext00000000000000000000001") return { status: 200, body: dir };
      if (c.url.startsWith("/api/v1/nodes/nod_ext00000000000000000000001/children")) return { status: 200, body: { items: [], next_cursor: null } };
      return fail(404, "not_found");
    });
    renderAt("/x/nod_ext00000000000000000000001");
    expect(await screen.findByRole("heading", { name: "Shared folder" })).toBeTruthy();
    expect(screen.queryByText(/ · /)).toBeNull();
    expect(calls.some((c) => c.url === "/api/v1/shared/external")).toBe(false);
  });
});
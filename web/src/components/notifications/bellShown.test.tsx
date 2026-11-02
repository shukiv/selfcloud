// SPDX-License-Identifier: AGPL-3.0-or-later

import { render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createMemoryRouter, RouterProvider } from "react-router-dom";
import { beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { session, type Session } from "../../lib/session";
import { ThemeProvider } from "../../theme/theme";
import { Direction } from "../../App";
import { loadNotifications } from "../../lib/notifications/i18n";
import { stubFetch, type Call } from "../../test/fetchStub";
import Shell from "../Shell";

// The bell is in the top bar of the drive only. These tests say when it reads the inbox and when it does not: an
// installation admin has no inbox, and a person signed in as another (an impersonation) must not have theirs read.
// The bell is a lazy chunk, so a negative test first waits for the shell to settle, then checks that no unread read was
// made. Without that wait, a guard that was removed would pass the test because the bell had not loaded yet.
const mocks = vi.hoisted(() => ({
  loadFolders: vi.fn(async () => []),
  loadHiddenFolders: vi.fn(async () => []),
  listAccessRequests: vi.fn(async () => ({ items: [] as unknown[] })),
}));
vi.mock("../../lib/files/folders", async () => {
  const { userWritable } = await import("../../lib/store");
  return {
    folders: userWritable([]),
    hiddenFolders: userWritable([]),
    loadFolders: mocks.loadFolders,
    loadHiddenFolders: mocks.loadHiddenFolders,
  };
});
vi.mock("../../lib/sharing/api", () => ({ setHidden: vi.fn(async () => undefined), listAccessRequests: mocks.listAccessRequests }));

beforeAll(async () => {
  await loadNotifications("en");
});

function signIn(role: "owner" | "member" | "admin", impersonation: Session["impersonation"] = null) {
  const s = {
    user: { id: "usr_olivia", email: "olivia@example.com", display_name: "Olivia", role, locale: "en" },
    account: role === "admin" ? null : { id: "acc_1", name: "Acme" },
    csrf: "csrf",
    publicLinks: false,
    impersonation,
  } as unknown as Session;
  session.set(s);
}

function renderShell() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const router = createMemoryRouter([{ path: "*", element: <Shell><p>page body</p></Shell> }], { initialEntries: ["/files"] });
  return render(
    <QueryClientProvider client={qc}>
      <ThemeProvider>
        <Direction>
          <RouterProvider router={router} />
        </Direction>
      </ThemeProvider>
    </QueryClientProvider>,
  );
}

const isUnread = (c: Call) => c.method === "GET" && c.url === "/api/v1/notifications/unread";
let calls: Call[] = [];

beforeEach(() => {
  calls = stubFetch((c) => (isUnread(c) ? { status: 200, body: { count: 0 } } : { status: 404 })).calls;
});

// settle waits long enough for the lazy bell chunk to load and for its first read to be sent.
const settle = () => new Promise((r) => setTimeout(r, 500));

describe("the bell in the top bar", () => {
  it("is drawn for a member of the drive, and reads the count", async () => {
    signIn("member");
    renderShell();
    await waitFor(() => expect(screen.getByRole("button", { name: "Notifications" })).toBeTruthy());
    expect(calls.filter(isUnread).length).toBeGreaterThan(0);
  });

  it("is not drawn for an installation admin, and the admin's inbox is not read", async () => {
    signIn("admin");
    renderShell();
    await screen.findByText("page body");
    await settle();
    expect(screen.queryByRole("button", { name: /^Notifications/ })).toBeNull();
    expect(calls.filter(isUnread)).toEqual([]);
  });

  it("is not drawn while the person is signed in as another, and their inbox is not read", async () => {
    signIn("member", { endsAt: "2026-10-31T13:00:00Z" } as Session["impersonation"]);
    renderShell();
    await screen.findByTestId("impersonation-banner");
    await settle();
    expect(screen.queryByRole("button", { name: /^Notifications/ })).toBeNull();
    expect(calls.filter(isUnread)).toEqual([]);
  });
});

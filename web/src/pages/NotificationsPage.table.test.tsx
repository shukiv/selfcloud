// SPDX-License-Identifier: AGPL-3.0-or-later

// The notifications on the one table (tl5, decision 138): the columns, the newest first mark on When, a row that opens its notice
// once, the notice's button that opens it once too, the sentinel that asks for the next page once, and the phone layout.
import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App as AntdApp } from "antd";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../test/fetchStub";
import { desktopViewport, PHONE, setViewport } from "../test/viewport";
import { loadNotifications } from "../lib/notifications/i18n";
import NotificationsPage from "./NotificationsPage";

beforeAll(async () => {
  await loadNotifications("en");
});

function signedIn(): Session {
  return {
    user: { id: "usr_olivia", email: "olivia@example.com", display_name: "Olivia", role: "member", locale: "en", status: "active" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf",
    publicLinks: false,
  } as unknown as Session;
}

function client(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
}

function Where() {
  const l = useLocation();
  return <div data-testid="where">{l.pathname + l.search}</div>;
}

function mount(qc = client()) {
  return render(
    <QueryClientProvider client={qc}>
      <AntdApp>
        <MemoryRouter initialEntries={["/notifications"]}>
          <Where />
          <Routes>
            <Route path="/notifications" element={<NotificationsPage />} />
          </Routes>
        </MemoryRouter>
      </AntdApp>
    </QueryClientProvider>,
  );
}

const ok = (body: unknown): Answer => ({ status: 200, body });
const isList = (c: Call) => c.method === "GET" && c.url.startsWith("/api/v1/notifications?") || (c.method === "GET" && c.url === "/api/v1/notifications");
const isSettings = (c: Call) => c.url === "/api/v1/notifications/settings";
const isRead = (c: Call) => c.method === "POST" && c.url.endsWith("/read");

const mention = {
  id: "ntf_abcdefgh",
  kind: "mention",
  node_id: "nod_abcdefgh",
  item_name: "plan.txt",
  actor_name: "Ann",
  created_at: "2026-11-01T10:00:00Z",
};
const reply = {
  id: "ntf_ijklmnop",
  kind: "reply",
  node_id: "nod_ijklmnop",
  item_name: "Notes.md",
  actor_gone: true,
  created_at: "2026-10-31T09:00:00Z",
  read_at: "2026-10-31T10:00:00Z",
};

let undoView: () => void = () => undefined;
beforeEach(() => {
  undoView = desktopViewport();
  session.set(signedIn());
});
afterEach(() => {
  undoView();
  session.set(null);
});

const region = () => screen.findByRole("region", { name: "Notifications" });

describe("notifications are one table (tl5, decision 138)", () => {
  it("draws Message, Status and When as its columns, and marks When newest first", async () => {
    stubFetch((c) => (isList(c) ? ok({ items: [mention, reply] }) : isSettings(c) ? ok({ email: true }) : fail(404, "not_found")));
    mount();
    const r = await region();
    const when = await within(r).findByRole("columnheader", { name: "When" });
    expect(when.getAttribute("aria-sort")).toBe("descending");
    expect(within(r).getByRole("columnheader", { name: "Message" })).toBeTruthy();
    expect(within(r).getByRole("columnheader", { name: "Status" })).toBeTruthy();
  });

  it("makes each notice a row with the notice's test id, and says its status as text", async () => {
    stubFetch((c) => (isList(c) ? ok({ items: [mention, reply] }) : isSettings(c) ? ok({ email: true }) : fail(404, "not_found")));
    mount();
    await region();
    const rows = await waitFor(() => {
      const found = document.querySelectorAll<HTMLElement>('tbody tr[data-testid="notice"]');
      expect(found.length).toBe(2);
      return found;
    });
    expect(within(rows[0]!).getByText("Unread")).toBeTruthy();
    expect(within(rows[1]!).getByText("Read")).toBeTruthy();
  });

  it("opens a row with Enter once, goes to its file and marks it read", async () => {
    const { calls } = stubFetch((c) => {
      if (isRead(c)) return ok(null);
      return isList(c) ? ok({ items: [mention] }) : isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    const row = (await waitFor(() => {
      const found = document.querySelector<HTMLElement>('tbody tr[data-testid="notice"]');
      expect(found).toBeTruthy();
      return found;
    })) as HTMLElement;
    row.focus();
    fireEvent.keyDown(row, { key: "Enter" });
    await waitFor(() => expect(screen.getByTestId("where").textContent).toBe("/n/nod_abcdefgh?details=comments"));
    expect(calls.filter(isRead)).toHaveLength(1);
  });

  it("opens a notice once and marks it read once when its button is clicked", async () => {
    const { calls } = stubFetch((c) => {
      if (isRead(c)) return ok(null);
      return isList(c) ? ok({ items: [mention] }) : isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    const button = await screen.findByRole("button", { name: /Ann mentioned you in plan\.txt/ });
    fireEvent.click(button);
    await waitFor(() => expect(screen.getByTestId("where").textContent).toBe("/n/nod_abcdefgh?details=comments"));
    expect(calls.filter(isRead)).toHaveLength(1);
  });

  it("asks for the next page through the sentinel once, and a failed next page is not asked again by itself", async () => {
    const before = (window as unknown as { IntersectionObserver?: unknown }).IntersectionObserver;
    // Each observer reports its element as in view at once, which is what the sentinel does for a short page.
    let watched = 0;
    class InView {
      constructor(private cb: (entries: { isIntersecting: boolean }[]) => void) {}
      observe() {
        watched++;
        this.cb([{ isIntersecting: true }]);
      }
      disconnect() {}
      unobserve() {}
    }
    (window as unknown as { IntersectionObserver: unknown }).IntersectionObserver = InView;
    try {
      const { calls } = stubFetch(async (c) => {
        if (isList(c)) {
          if (!c.url.includes("cursor=")) return ok({ items: [mention], next_cursor: "c1" });
          // The page takes time to fail, as a network does, so the list is seen loading before it is seen failed.
          await new Promise((r) => setTimeout(r, 20));
          return fail(500, "internal");
        }
        return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
      });
      mount();
      await screen.findByText(/next notifications could not be loaded/);
      // The effects of the commit that showed the failure have run: the end of the list is not watched again.
      await act(async () => {});
      expect(watched).toBe(1);
      expect(calls.filter((c) => c.url.includes("cursor=c1"))).toHaveLength(1);
    } finally {
      (window as unknown as { IntersectionObserver?: unknown }).IntersectionObserver = before;
    }
  });

  it("lays out at a phone's width with Status on the second line of the message", async () => {
    undoView();
    undoView = setViewport(PHONE);
    stubFetch((c) => (isList(c) ? ok({ items: [mention] }) : isSettings(c) ? ok({ email: true }) : fail(404, "not_found")));
    mount();
    const row = await screen.findByRole("row", { name: /Ann mentioned you in plan\.txt/ });
    expect(row.querySelector(".row-second")?.textContent).toContain("Unread");
  });
});

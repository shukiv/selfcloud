// SPDX-License-Identifier: AGPL-3.0-or-later

import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { beforeAll, afterEach, beforeEach, describe, expect, it } from "vitest";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../../test/fetchStub";
import { loadNotifications } from "../../lib/notifications/i18n";
import Bell from "./Bell";

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

beforeEach(() => session.set(signedIn()));
afterEach(() => session.set(null));

// A client that does not retry, so a failed answer shows at once.
function client(): QueryClient {
  return new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
}

// setVisibility says whether the document is shown or hidden, as a browser does for a background tab.
function setVisibility(state: "hidden" | "visible") {
  Object.defineProperty(document, "visibilityState", { configurable: true, get: () => state });
}
afterEach(() => {
  delete (document as unknown as Record<string, unknown>).visibilityState;
});

function Where() {
  return <div data-testid="where">{useLocation().pathname}</div>;
}

function mount(qc = client()) {
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/files"]}>
        <Bell />
        <Routes>
          <Route path="*" element={<Where />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

// unread answers the count of the bell's read and nothing else.
const unread = (count: number): Answer => ({ status: 200, body: { count } });
const isUnread = (c: Call) => c.method === "GET" && c.url === "/api/v1/notifications/unread";

describe("the bell", () => {
  it("does not read the count once the person has signed out", async () => {
    const { calls } = stubFetch((c) => (isUnread(c) ? unread(2) : fail(404, "not_found")));
    mount();
    await waitFor(() => expect(calls.filter(isUnread).length).toBe(1));
    // A sign-out clears the session before the page leaves; the bell must not ask again, which would be refused (401).
    act(() => session.set(null));
    await new Promise((r) => setTimeout(r, 50));
    expect(calls.filter(isUnread).length).toBe(1);
  });

  it("shows the unread count, and the screen reader hears it", async () => {
    stubFetch((c) => (isUnread(c) ? unread(2) : fail(404, "not_found")));
    mount();
    const live = await screen.findByRole("status");
    await waitFor(() => expect(live.textContent).toBe("2 unread"));
    expect(screen.getByText("2")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Notifications, 2 unread" })).toBeTruthy();
  });

  it("shows no count and says so when nothing is unread", async () => {
    stubFetch((c) => (isUnread(c) ? unread(0) : fail(404, "not_found")));
    mount();
    const live = await screen.findByRole("status");
    await waitFor(() => expect(live.textContent).toBe("No unread notifications"));
    expect(screen.queryByText("0")).toBeNull();
  });

  it("keeps the last count, and shows no error, when a later read fails", async () => {
    let reads = 0;
    const { calls } = stubFetch((c) => {
      if (!isUnread(c)) return fail(404, "not_found");
      reads++;
      return reads === 1 ? unread(2) : fail(500, "internal");
    });
    const qc = client();
    mount(qc);
    const live = await screen.findByRole("status");
    await waitFor(() => expect(live.textContent).toBe("2 unread"));
    await qc.refetchQueries();
    await waitFor(() => expect(calls.filter(isUnread).length).toBeGreaterThanOrEqual(2));
    expect(live.textContent).toBe("2 unread");
    expect(screen.getByText("2")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });

  it("shows 99+ in the badge, the name and the live region when the server stopped counting", async () => {
    stubFetch((c) => (isUnread(c) ? { status: 200, body: { count: 0, more: true } } : fail(404, "not_found")));
    mount();
    const live = await screen.findByRole("status");
    await waitFor(() => expect(live.textContent).toBe("99+ unread"));
    expect(screen.getByText("99+")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Notifications, 99+ unread" })).toBeTruthy();
  });

  it("makes no request in a tab that is hidden when it appears, and reads when the tab is shown", async () => {
    setVisibility("hidden");
    const { calls } = stubFetch((c) => (isUnread(c) ? unread(2) : fail(404, "not_found")));
    mount();
    await new Promise((r) => setTimeout(r, 300));
    expect(calls.filter(isUnread)).toEqual([]);
    setVisibility("visible");
    document.dispatchEvent(new Event("visibilitychange"));
    await waitFor(() => expect(calls.filter(isUnread).length).toBeGreaterThan(0));
    expect(await screen.findByText("2")).toBeTruthy();
  });

  it("opens the notifications page when pressed", async () => {
    stubFetch((c) => (isUnread(c) ? unread(1) : fail(404, "not_found")));
    mount();
    fireEvent.click(await screen.findByRole("button", { name: /^Notifications/ }));
    await waitFor(() => expect(screen.getByTestId("where").textContent).toBe("/notifications"));
  });
});

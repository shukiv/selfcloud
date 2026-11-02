// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { App as AntdApp } from "antd";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeAll, beforeEach, describe, expect, it } from "vitest";

import { session, type Session } from "../lib/session";
import { fail, stubFetch, type Answer, type Call } from "../test/fetchStub";
import { loadNotifications } from "../lib/notifications/i18n";
import NotificationsPage from "./NotificationsPage";
import { viewFor } from "../App";

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

describe("the notifications page: the list", () => {
  it("shows an error when the first load fails", async () => {
    stubFetch((c) => (isList(c) ? fail(500, "internal") : isSettings(c) ? ok({ email: true }) : fail(404, "not_found")));
    mount();
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("could not be loaded");
  });

  it("lists the notices newest first, as sentences, and shows no raw ID", async () => {
    stubFetch((c) =>
      isList(c) ? ok({ items: [mention, reply] }) : isSettings(c) ? ok({ email: true }) : fail(404, "not_found"),
    );
    const { container } = mount();
    const first = await screen.findByRole("button", { name: /Ann mentioned you in plan\.txt/ });
    const second = screen.getByRole("button", { name: /A former member replied to your comment on Notes\.md/ });
    expect(first.compareDocumentPosition(second) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(container.textContent).not.toMatch(/ntf_|nod_/);
    expect(within(first).getByText("Unread")).toBeTruthy();
    expect(within(second).getByText("Read")).toBeTruthy();
  });

  it("keeps the list, and says so, when a refresh fails", async () => {
    let fresh = false;
    const { calls } = stubFetch((c) => {
      if (isList(c)) return fresh ? fail(500, "internal") : ok({ items: [mention] });
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    const qc = client();
    mount(qc);
    await screen.findByRole("button", { name: /Ann mentioned you/ });
    fresh = true;
    await qc.invalidateQueries();
    await waitFor(() => expect(calls.filter(isList).length).toBeGreaterThanOrEqual(2));
    await waitFor(() => expect(screen.getByRole("alert").textContent).toContain("could not be refreshed"));
    expect(screen.getByRole("button", { name: /Ann mentioned you in plan\.txt/ })).toBeTruthy();
  });

  it("follows the cursor for the next page", async () => {
    const { calls } = stubFetch((c) => {
      if (isList(c)) return c.url.includes("cursor=") ? ok({ items: [reply] }) : ok({ items: [mention], next_cursor: "c1" });
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Show more" }));
    await screen.findByRole("button", { name: /A former member replied/ });
    expect(calls.some((c) => c.url.includes("cursor=c1"))).toBe(true);
  });
});

describe("the notifications page: opening an item", () => {
  it("marks it read, goes to its file, and opens its comments", async () => {
    const { calls } = stubFetch((c) => {
      if (c.method === "POST" && c.url === "/api/v1/notifications/ntf_abcdefgh/read") return { status: 204 };
      if (isList(c)) return ok({ items: [mention] });
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: /Ann mentioned you in plan\.txt/ }));
    await waitFor(() => expect(screen.getByTestId("where").textContent).toBe("/n/nod_abcdefgh?details=comments"));
    expect(calls.some((c) => c.method === "POST" && c.url === "/api/v1/notifications/ntf_abcdefgh/read")).toBe(true);
  });

  it("does not open an item whose address is not valid, and says so", async () => {
    const bad = { ...mention, node_id: "nod_../../etc" };
    const { calls } = stubFetch((c) => {
      if (isList(c)) return ok({ items: [bad] });
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: /Ann mentioned you in plan\.txt/ }));
    await screen.findByText("This notification has no file to open.");
    expect(screen.getByTestId("where").textContent).toBe("/notifications");
    expect(calls.some((c) => c.method === "POST")).toBe(false);
  });
});

describe("the notifications page: states that are said as they are", () => {
  const realMatchMedia = window.matchMedia;
  afterEach(() => {
    window.matchMedia = realMatchMedia;
  });

  // coarseFinger makes the page see a finger (a coarse pointer), or a mouse.
  function coarseFinger(on: boolean) {
    window.matchMedia = ((q: string) => ({
      matches: on && q.includes("coarse"),
      media: q,
      onchange: null,
      addListener: () => undefined,
      removeListener: () => undefined,
      addEventListener: () => undefined,
      removeEventListener: () => undefined,
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia;
  }

  it("says a failed next page as such, and not as a failed refresh", async () => {
    stubFetch((c) => {
      if (isList(c)) return c.url.includes("cursor=") ? fail(500, "internal") : ok({ items: [mention], next_cursor: "c1" });
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Show more" }));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("The next notifications could not be loaded");
    expect(screen.queryByText(/could not be refreshed/)).toBeNull();
    expect(screen.getByRole("button", { name: /Ann mentioned you in plan\.txt/ })).toBeTruthy();
  });

  it("gives the email setting a way to try again after a failed read", async () => {
    let reads = 0;
    stubFetch((c) => {
      if (isList(c)) return ok({ items: [] });
      if (isSettings(c)) {
        reads++;
        return reads === 1 ? fail(500, "internal") : ok({ email: true });
      }
      return fail(404, "not_found");
    });
    mount();
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await waitFor(() => expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("true"));
    expect(reads).toBe(2);
  });

  it("makes the buttons and the line targets 44 px on a finger, and the usual size with a mouse", async () => {
    coarseFinger(true);
    stubFetch((c) => {
      if (isList(c)) return ok({ items: [mention], next_cursor: "c1" });
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    const more = (await screen.findByRole("button", { name: "Show more" })) as HTMLElement;
    expect(more.style.minHeight).toBe("44px");
    const line = screen.getByRole("button", { name: /Ann mentioned you in plan\.txt/ }) as HTMLElement;
    expect(line.style.minHeight).toBe("44px");
  });

  it("keeps the usual size of the buttons with a mouse", async () => {
    coarseFinger(false);
    stubFetch((c) => {
      if (isList(c)) return ok({ items: [mention], next_cursor: "c1" });
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    const more = (await screen.findByRole("button", { name: "Show more" })) as HTMLElement;
    expect(more.style.minHeight).toBe("");
  });

  it("says while the person is signed in as another, and reads no inbox", async () => {
    session.set({ ...signedIn(), impersonation: { endsAt: "2026-10-31T13:00:00Z" } } as unknown as Session);
    const { calls } = stubFetch(() => fail(404, "not_found"));
    mount();
    expect(await screen.findByText("Notifications are not shown while you are signed in as another person.")).toBeTruthy();
    await new Promise((r) => setTimeout(r, 300));
    expect(calls.filter((c) => c.url.startsWith("/api/v1/notifications")).length).toBe(0);
  });
});

describe("the notifications address", () => {
  it("is the notifications view, for the drive and not for the admin's pages", () => {
    expect(viewFor("/notifications")).toBe("notifications");
  });
});

describe("the notifications page: the email switch", () => {
  it("saves a new choice", async () => {
    const { calls } = stubFetch((c) => {
      if (isList(c)) return ok({ items: [] });
      if (c.method === "PATCH" && isSettings(c)) return ok({ email: false });
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    const sw = await screen.findByRole("switch");
    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("true"));
    fireEvent.click(sw);
    await waitFor(() => expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("false"));
    expect(calls.find((c) => c.method === "PATCH")?.body).toEqual({ email: false });
  });

  it("puts the switch back, and says so, when the save is refused", async () => {
    stubFetch((c) => {
      if (isList(c)) return ok({ items: [] });
      if (c.method === "PATCH") return fail(500, "internal");
      return isSettings(c) ? ok({ email: true }) : fail(404, "not_found");
    });
    mount();
    const sw = await screen.findByRole("switch");
    await waitFor(() => expect(sw.getAttribute("aria-checked")).toBe("true"));
    fireEvent.click(sw);
    await screen.findByText("The email setting could not be saved. It is back as it was.");
    await waitFor(() => expect(screen.getByRole("switch").getAttribute("aria-checked")).toBe("true"));
  });
});

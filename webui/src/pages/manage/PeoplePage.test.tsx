// SPDX-License-Identifier: AGPL-3.0-or-later

import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { createQueryClient } from "../../lib/query";
import { session, type Session } from "../../lib/session";
import { fail, stubFetch, type Call } from "../../test/fetchStub";
import PeoplePage from "./PeoplePage";

const ME = { id: "usr_owen", email: "owen@example.com", display_name: "Owen Owner", role: "owner", status: "active" };

function person(id: string, status: string, role = "member") {
  return {
    id,
    external_id: null,
    email: `${id}@example.com`,
    display_name: `Name ${id}`,
    locale: "en",
    role,
    status,
    verified_at: null,
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
  };
}

function signedAs(): Session {
  return {
    user: { ...ME, locale: "en" },
    account: { id: "acc_1", name: "Acme" },
    csrf: "csrf-owen",
    publicLinks: false,
  } as unknown as Session;
}

function renderPage(qc: QueryClient = createQueryClient()) {
  render(
    <QueryClientProvider client={qc}>
      <PeoplePage />
    </QueryClientProvider>,
  );
  return qc;
}

beforeEach(() => session.set(signedAs()));
afterEach(() => session.set(null));

describe("the people list", () => {
  it("a failed first load shows the error, never the empty message (guard: error before the empty state)", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? fail(403, "forbidden") : { status: 200, body: { items: [] } }));
    renderPage();
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByText("No people yet. Invite the first person to start.")).toBeNull();
  });

  it("a failed refetch keeps the rows already shown, with a warning (guard: list stays on a failed refetch)", async () => {
    let broken = false;
    stubFetch((c: Call) => {
      if (c.url.includes("/users")) return broken ? fail(403, "forbidden") : { status: 200, body: { items: [person("usr_a", "active")] } };
      return { status: 200, body: { items: [] } };
    });
    const qc = renderPage();
    expect(await screen.findByText("Name usr_a")).toBeTruthy();
    broken = true;
    await act(async () => {
      await qc.refetchQueries({ type: "all" });
    });
    await waitFor(() => expect(screen.getByRole("alert").className).toContain("ant-alert-warning"));
    expect(screen.getByText("Name usr_a")).toBeTruthy();
  });

  it("the menu offers only what the person's state allows: a pending person is resent, not disabled (guard: the status rules in the menu)", async () => {
    stubFetch((c: Call) => (c.url.includes("/users") ? { status: 200, body: { items: [person("usr_p", "pending")] } } : { status: 200, body: { items: [] } }));
    renderPage();
    await screen.findByText("Name usr_p");
    fireEvent.click(screen.getByRole("button", { name: /Actions for Name usr_p|Actions/ }));
    const menu = await screen.findByRole("menu");
    expect(within(menu).getByText("Resend invitation")).toBeTruthy();
    expect(within(menu).queryByText("Disable")).toBeNull();
    expect(within(menu).getByText("Remove…")).toBeTruthy();
  });
});

describe("inviting a person", () => {
  it("the verification link in the answer is never shown or kept in the cache (guard: the reply is not stored)", async () => {
    const SECRET = "tok-SECRET-LINK-0123456789";
    stubFetch((c: Call) => {
      if (c.method === "POST" && c.url.endsWith("/users")) {
        return {
          status: 201,
          body: { ...person("usr_new", "pending"), verification: { method: "link", url: `https://x.example/verify/${SECRET}` } },
        };
      }
      if (c.url.includes("/users")) return { status: 200, body: { items: [] } };
      return { status: 200, body: { items: [] } };
    });
    const qc = renderPage();
    fireEvent.click(await screen.findByRole("button", { name: /Invite person/ }));
    fireEvent.change(screen.getByLabelText("Email"), { target: { value: "new@example.com" } });
    fireEvent.click(screen.getByRole("button", { name: /Send invitation/ }));
    expect(await screen.findByText("The email was sent.")).toBeTruthy();
    expect(new XMLSerializer().serializeToString(document)).not.toContain(SECRET);
    expect(JSON.stringify(qc.getQueryCache().getAll().map((q) => q.state.data ?? null))).not.toContain(SECRET);
  });
});
